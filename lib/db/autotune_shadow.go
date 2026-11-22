// autotune_shadow.go — the autonomous-tuning shadow circuit (v1.23.0
// schema v18, spec B2). Once a day (a 24h meta gate + a 1h in-process
// cache — the maybePruneAfterRead pattern), when meta auto_mode is
// "shadow", one round per whitelisted registry rule runs on the
// RecordReadEvent tail: it evaluates the rule's verdict query (the T3
// registry — never a re-implementation), and on a fire records ONE
// prediction row into tuning_predictions (append-only — nothing is ever
// applied; the apply path is the v1.24.0 gate) and updates the
// accumulation counters (meta auto_streak_<key>, fired_auto_<signal>).
// Every stamped round — fired or not — also bumps the round counter
// (meta auto_rounds_<key>, r100-1): rounds and fires are independent
// accumulation inputs, so a round must count even when the trigger does
// not fire (rounds ≥ 14 AND fires ≥ 5 are separate gate conditions).
// All failures pass quietly (the metering principle) and the DB gate is
// stamped only by a completed round, keeping the retry open.
//
// Registry access is an INJECTED SEAM: db can never import lib/ops (ops
// imports db — the only legal direction), so the composition root
// registers the ops.AutoRules projection + the ops.AutoTrigger verdict
// via RegisterShadowCircuit at startup (the cmd/flint blank-import
// injection precedent). Until registered — or while auto_mode sits at
// its off default, or while the whitelist is empty — the hook costs a
// nil/mode check and at most the single batched gate read — off costs
// 0 queries at steady state and 1 batched gate read per hour at cold
// start (the r100-2 correction of the v1.23.0 zero-cost wording: only
// an unregistered circuit is literally zero round trips).
package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ShadowRule — the db-side projection of the autotune code-registry row
// (lib/ops AutoRule). Carries only what the SQL round consumes: the
// ObsWindowD-interpolated verdict query ($1 binds the rule's safe-range
// floor — the AutoVerdictQuery contract), the tuning key and its
// direction, the closed safe range any proposal must stay inside, and
// the success-pair sentence (the mandatory "a hit is stability, not
// causality" footnote rides it — the rationale composes registry
// vocabulary and numbers only, never personal data).
type ShadowRule struct {
	Signal      string
	Query       string
	Key         string
	Direction   string // "down" | "up"
	SafeRange   [2]float64
	Auto        string // "shadow"
	SuccessPair string
	ObsWindowD  int
}

// ShadowObs — the one-row observation the verdict query returns (the
// ops.AutoTriggerObs projection): the window sample, its zero rows, and
// the reclass pool (zero rows with near_score >= the bound floor).
type ShadowObs struct {
	ReadEventsN int
	ZeroN       int
	ReclassN    int
}

// ShadowVerdict — the trigger verdict (the ops.AutoTriggerVerdict
// projection): Fired, the sample-floor pass, the reclass share (%), and
// the basis sentence (the audit trail — the circuit itself logs
// nothing).
type ShadowVerdict struct {
	Fired    bool
	SampleOk bool
	Ratio    int
	Detail   string
}

// ShadowVerdictFn — the injected pure trigger verdict (the real adapter
// wraps ops.AutoTrigger — thresholds stay the injected single source).
type ShadowVerdictFn func(rule ShadowRule, obs ShadowObs, th store.Thresholds) ShadowVerdict

// the injected circuit (nil = unregistered — the zero-cost default).
var (
	shadowRulesFn   func() []ShadowRule
	shadowVerdictFn ShadowVerdictFn
)

// RegisterShadowCircuit — installs the autotune circuit seam (rules
// source + verdict fn). The caller is the composition root on the ops
// side (ops imports db; db can never import ops — compile-verified).
// Passing nils unregisters (the test-fixture restore convention).
func RegisterShadowCircuit(rules func() []ShadowRule, verdict ShadowVerdictFn) {
	shadowRulesFn = rules
	shadowVerdictFn = verdict
}

// shadowGateKey — the 24h-gate meta key (the last_*_at RFC3339
// convention; registered in ops metaKnownKeys — self-reference defense).
const shadowGateKey = "last_shadow_at"

// shadowGateCheckedAt — the in-process gate-recheck cache (the
// pruneGateCheckedAt convention): 0 = unchecked; shared regardless of
// Repo instance, shrinking the read hot path to one gate SELECT per
// hour. The 24h DB gate itself always lives in meta.
var shadowGateCheckedAt atomic.Int64

// shadowGateCacheTTL — the in-process gate-cache lifetime (1h, shorter
// than the 24h DB gate — the existing 3-hook convention).
const shadowGateCacheTTL = time.Hour

// shadowGateProbe — the gate-cache verdict (the pruneGateProbe twin).
func shadowGateProbe(now time.Time) bool {
	if last := shadowGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < shadowGateCacheTTL {
		return false
	}
	shadowGateCheckedAt.Store(now.Unix())
	return true
}

// ResetShadowGateCacheForTest — zeroes the in-process shadow gate cache
// (the resetSnapshotGateCache convention, exported for the external
// db_test conformance run — the wire.ThresholdMetasAppendForTest
// precedent; never use on production paths).
func ResetShadowGateCacheForTest() {
	shadowGateCheckedAt.Store(0)
}

// the round-trip execution seams (the repo.go transitionExecFn
// convention) — every query the shadow round issues routes through
// them, so tests count the ≤5 round-trip budget by injection.
var (
	shadowQueryRowFn = func(ctx context.Context, r *Repo, query string, args ...any) pgx.Row {
		return r.pool.QueryRow(ctx, query, args...)
	}
	shadowExecFn = func(ctx context.Context, r *Repo, query string, args ...any) error {
		_, err := r.pool.Exec(ctx, query, args...)
		return err
	}
)

// shadowProposal — the proposal pure function: the safe-range BOUND in
// the registry direction (down → lo, up → hi) — the shadow asks "what
// if the key moved to the boundary the evidence supports"; intermediate
// stepping is an apply-phase (v1.24.0) concern. ok reports any strict
// move from cur. In the registry direction the move is the rule's own;
// when cur sits beyond the bound (an override dragged outside the
// range), the proposal is the bound itself — a range-restoring move
// that may OPPOSE the registry direction (the streak logic reads it as
// a reversal). The proposal is inside the closed safe range by
// construction, so no out-of-range value is ever predicted.
func shadowProposal(rule ShadowRule, cur float64) (float64, bool) {
	bound := rule.SafeRange[0]
	if rule.Direction == "up" {
		bound = rule.SafeRange[1]
	}
	return bound, bound != cur
}

// shadowMoveDir — a prediction's move direction: -1 down, +1 up, 0 flat.
func shadowMoveDir(cur, new float64) int {
	switch {
	case new < cur:
		return -1
	case new > cur:
		return 1
	}
	return 0
}

// shadowStreakNext — the consecutive-hit counter transition (the spec
// B2 streak rules — "a hit is round-over-round stability, not
// causality"). prevDir is the LAST PREDICTION's move direction (0 = no
// prior prediction for the key). (a) the first fire starts at 1; (b) a
// proposal VALUE change still hits while the direction is maintained;
// (c) a non-fired round with an adequate sample maintains the streak; a
// direction reversal resets to 1 (the new direction's first hit) and a
// sample-floor miss resets to 0 (no verdict without observation).
func shadowStreakNext(prevStreak, prevDir, thisDir int, fired, sampleOk bool) int {
	if !sampleOk {
		return 0
	}
	if !fired {
		return prevStreak
	}
	if prevDir == 0 || prevDir != thisDir {
		return 1
	}
	return prevStreak + 1
}

// shadowShadowValueText — the minimal-digit float notation (the
// thresholdValueText convention — cur/new ride the prediction row as
// the same notation the TUNING table shows).
func shadowValueText(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// shadowBumpCase — the ON CONFLICT CASE expression for the guarded
// counter increments (fired_auto_ · auto_rounds_): for each bumped key
// placeholder a numeric meta.value increments, and a corrupt, empty, or
// non-numeric text resets to '1' instead of failing the cast (r101-3 —
// a polluted counter must never fail the whole batch, which would
// unstamp the 24h gate and permanently block every later round). Keys
// outside the bump list take EXCLUDED.value (the plain set).
func shadowBumpCase(keys ...string) string {
	branches := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		branches = append(branches,
			fmt.Sprintf("WHEN meta.key = %s AND meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + 1)::text", k),
			fmt.Sprintf("WHEN meta.key = %s THEN '1'", k))
	}
	return "CASE " + strings.Join(branches, " ") + " ELSE EXCLUDED.value END"
}

// shadowRuleState — one rule's batched gate-read slice (trip 1): the
// current key value override text, the streak counter text, and the
// previous prediction's cur/new texts (the streak direction-comparison
// input).
type shadowRuleState struct {
	curText, streakText, prevNewText, prevCurText string
}

// shadowRoundState — the shared gate-read slice: auto_mode, the 24h
// gate stamp, the two verdict thresholds — plus the per-rule slices
// (parallel to the eligible-rules slice).
type shadowRoundState struct {
	mode, last, zmin, zwarn string
	rules                   []shadowRuleState
}

// maybeShadowRound — the RecordReadEvent tail hook (the 4th hook —
// predictions observe the post-cleanup state). Gate-checked per hour
// in-process, gated per day in the DB (last_shadow_at — no claim dance:
// a duplicate daily round is an append-only duplicate, not a corruption,
// unlike the snapshot's seed-once semantics). Round trips per fired
// round: gate batch 1 + verdict 1 + what-if 1 + INSERT 1 + meta batch 1
// = 5 (the spec ceiling); a non-fired round: gate 1 + verdict 1 + stamp
// 1 = 3; the off default: 1 (the batched gate+mode read) cold, 0 warm.
func (r *Repo) maybeShadowRound(ctx context.Context) {
	rulesSrc, verdict := shadowRulesFn, shadowVerdictFn
	if rulesSrc == nil || verdict == nil {
		return // circuit unregistered — zero cost (the composition root has not wired shadow)
	}
	if !shadowGateProbe(time.Now().UTC()) {
		return
	}
	// The whitelist gate precedes every query: rules whose Auto grade is
	// not "shadow", or whose key the threshold registry does not carry at
	// grade "shadow", are skipped (wire.ThresholdAutoGrade — the single
	// whitelist source). No eligible rule = 0 round trips.
	var eligible []ShadowRule
	for _, rule := range rulesSrc() {
		if rule.Auto == "shadow" && wire.ThresholdAutoGrade(rule.Key) == "shadow" {
			eligible = append(eligible, rule)
		}
	}
	if len(eligible) == 0 {
		return
	}
	state, err := r.shadowGateRead(ctx, eligible)
	if err != nil {
		return // quiet (the metering principle) — the gate stays unstamped, retry open
	}
	if state.mode != "shadow" {
		return // off (or corrupt enum text) — the unconfigured default, zero writes
	}
	if !pruneGateDue(state.last, time.Now().UTC()) {
		return // the 24h DB gate is closed
	}
	th := shadowThresholds(state.zmin, state.zwarn)
	stamp := time.Now().UTC().Format(time.RFC3339)
	for i, rule := range eligible {
		r.shadowRoundRule(ctx, rule, state.rules[i], verdict, th, stamp)
	}
}

// shadowGateRead — trip 1: ONE batched SELECT resolving everything the
// rounds need — auto_mode, the 24h gate stamp, the two verdict
// thresholds (zero_near_min_read·zero_near_warn_pct — the registry
// single source), and per rule the key's current override, the streak
// counter, and the previous prediction's cur/new (folding it here keeps
// the fired round at 5 round trips). Scalar subqueries keep it one
// round trip even though the values span two tables
// (meta·tuning_predictions). Each rule takes 4 distinct placeholders
// (the Doltgres extended-protocol placeholder-reuse caution — the
// bumpDailyFlat note).
func (r *Repo) shadowGateRead(ctx context.Context, rules []ShadowRule) (shadowRoundState, error) {
	q := `SELECT
 (SELECT value FROM meta WHERE key = 'auto_mode'),
 (SELECT value FROM meta WHERE key = $1),
 (SELECT value FROM meta WHERE key = 'zero_near_min_read'),
 (SELECT value FROM meta WHERE key = 'zero_near_warn_pct')`
	args := []any{shadowGateKey}
	for i, rule := range rules {
		base := 2 + i*4
		q += fmt.Sprintf(`,
 (SELECT value FROM meta WHERE key = $%d),
 (SELECT value FROM meta WHERE key = $%d),
 (SELECT new FROM tuning_predictions WHERE key = $%d ORDER BY ts DESC, id DESC LIMIT 1),
 (SELECT cur FROM tuning_predictions WHERE key = $%d ORDER BY ts DESC, id DESC LIMIT 1)`,
			base, base+1, base+2, base+3)
		args = append(args, rule.Key, "auto_streak_"+rule.Key, rule.Key, rule.Key)
	}
	scan := make([]any, 0, 4+len(rules)*4)
	var mode, last, zmin, zwarn *string
	scan = append(scan, &mode, &last, &zmin, &zwarn)
	per := make([][4]*string, len(rules))
	for i := range rules {
		scan = append(scan, &per[i][0], &per[i][1], &per[i][2], &per[i][3])
	}
	if err := shadowQueryRowFn(ctx, r, q, args...).Scan(scan...); err != nil {
		return shadowRoundState{}, err
	}
	st := shadowRoundState{mode: deref(mode), last: deref(last), zmin: deref(zmin), zwarn: deref(zwarn)}
	for i := range rules {
		st.rules = append(st.rules, shadowRuleState{
			curText: deref(per[i][0]), streakText: deref(per[i][1]),
			prevNewText: deref(per[i][2]), prevCurText: deref(per[i][3]),
		})
	}
	return st, nil
}

// deref — a scanned nullable text ("" when NULL).
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// shadowThresholds — the verdict thresholds from the batched gate read:
// absent·corrupt·non-positive texts fall back to the registry defaults
// (the diagThresholds unset-0 convention).
func shadowThresholds(zmin, zwarn string) store.Thresholds {
	def := store.DefaultThresholds()
	th := store.Thresholds{ZeroNearMinRead: def.ZeroNearMinRead, ZeroNearWarnPct: def.ZeroNearWarnPct}
	if n, err := strconv.Atoi(zmin); err == nil && n > 0 {
		th.ZeroNearMinRead = n
	}
	if n, err := strconv.Atoi(zwarn); err == nil && n > 0 {
		th.ZeroNearWarnPct = n
	}
	return th
}

// shadowDefaultText — the key's registry-default notation (the
// wire.ThresholdMetas single source — no db-side copy of defaults).
func shadowDefaultText(key string) string {
	for _, m := range wire.ThresholdMetas() {
		if m.Key == key {
			return thresholdValueText(m.Default)
		}
	}
	return ""
}

// shadowRoundRule — one rule's round (trips 2-5). trip 2 executes the
// registry verdict query at the safe-range floor; on a fire the proposal
// (the directional bound) is what-if re-evaluated by re-running the same
// query at the proposed value (trip 3 — a pure re-execution, no second
// query definition), the prediction row is appended (trip 4), and the
// accumulation counters + the gate stamp land in ONE batched statement
// (trip 5). A non-fired round stamps the gate (and writes the streak
// only on an actual sample-floor reset). Every failure returns quietly
// (the metering principle); a failure after the INSERT leaves the gate
// unstamped, so the retry re-inserts — an append-only duplicate, not a
// corruption (the snapshot claim dance is deliberately absent).
func (r *Repo) shadowRoundRule(ctx context.Context, rule ShadowRule, rs shadowRuleState, verdict ShadowVerdictFn, th store.Thresholds, stamp string) {
	curText := rs.curText
	if curText == "" {
		curText = shadowDefaultText(rule.Key)
	}
	cur, err := strconv.ParseFloat(curText, 64)
	if err != nil {
		return // an unparseable override — quiet skip (the metering principle)
	}
	var obs ShadowObs
	if err := shadowQueryRowFn(ctx, r, rule.Query, rule.SafeRange[0]).
		Scan(&obs.ReadEventsN, &obs.ZeroN, &obs.ReclassN); err != nil {
		return
	}
	v := verdict(rule, obs, th)

	streakKey := "auto_streak_" + rule.Key
	roundsKey := "auto_rounds_" + rule.Key
	firedKey := "fired_auto_" + rule.Signal
	prevStreak := 0
	if n, err := strconv.Atoi(rs.streakText); err == nil {
		prevStreak = n
	}
	prevDir := 0
	if pc, e1 := strconv.ParseFloat(rs.prevCurText, 64); e1 == nil {
		if pn, e2 := strconv.ParseFloat(rs.prevNewText, 64); e2 == nil {
			prevDir = shadowMoveDir(pc, pn)
		}
	}

	if !v.Fired {
		// A sample-floor miss resets the streak (written only on an actual
		// change — a maintained round writes nothing but the streak); a
		// sample-ok non-fire maintains it (spec rule c). Either way the
		// round ran: bump the round counter and stamp the gate (r100-1 —
		// a non-fired round still counts toward the rounds ≥ 14 gate).
		next := shadowStreakNext(prevStreak, prevDir, 0, false, v.SampleOk)
		if !v.SampleOk && next != prevStreak {
			_ = shadowExecFn(ctx, r,
				`INSERT INTO meta (key, value) VALUES ($1, $2), ($3, '1'), ($4, $5)
				 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$3"),
				streakKey, strconv.Itoa(next), roundsKey, shadowGateKey, stamp)
			return
		}
		_ = shadowExecFn(ctx, r,
			`INSERT INTO meta (key, value) VALUES ($1, '1'), ($2, $3)
			 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$1"),
			roundsKey, shadowGateKey, stamp)
		return
	}

	new, ok := shadowProposal(rule, cur)
	if !ok {
		// cur sits exactly at the directional bound — no strict move to
		// predict. The trigger still FIRED (the fired counter counts
		// trigger-verdict fires, whether or not a proposal exists — the
		// r100-3 alignment; prediction rows stay proposal-only per the
		// spec). The round also bumps the round counter and stamps the
		// gate; the streak is maintained (no directional move to judge).
		_ = shadowExecFn(ctx, r,
			`INSERT INTO meta (key, value) VALUES ($1, '1'), ($2, '1'), ($3, $4)
			 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$1", "$2"),
			firedKey, roundsKey, shadowGateKey, stamp)
		return
	}
	thisDir := shadowMoveDir(cur, new)

	// Trip 3 — the what-if at the proposed value: the same registry query
	// re-executed (a pure re-execution — the predicted reclass count and
	// the new near share read the real distribution, never a re-derived
	// formula).
	var whatIf ShadowObs
	if err := shadowQueryRowFn(ctx, r, rule.Query, new).
		Scan(&whatIf.ReadEventsN, &whatIf.ZeroN, &whatIf.ReclassN); err != nil {
		return
	}
	predPct := 0
	if whatIf.ReadEventsN > 0 {
		predPct = whatIf.ReclassN * 100 / whatIf.ReadEventsN
	}
	newText := shadowValueText(new)
	rationale := fmt.Sprintf("%s fired: %s; propose %s %s -> %s (shadow prediction — never applied); "+
		"predicted zero_near_pct %d%% (%d reclass of %d reads); success pair: %s",
		rule.Signal, v.Detail, rule.Key, shadowValueText(cur), newText,
		predPct, whatIf.ReclassN, whatIf.ReadEventsN, rule.SuccessPair)

	// Trip 4 — the append-only prediction row.
	if err := shadowExecFn(ctx, r,
		`INSERT INTO tuning_predictions
		   (trigger, key, cur, new, pred_zero_near_pct, pred_reclass_n, sample_n, rationale)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rule.Signal, rule.Key, shadowValueText(cur), newText,
		predPct, whatIf.ReclassN, whatIf.ReadEventsN, rationale); err != nil {
		return
	}

	// Trip 5 — ONE batched statement: the streak (set), the fired counter
	// and the round counter (guarded increments), and the gate stamp. A
	// corrupt existing counter text resets to '1' inside the CASE instead
	// of failing the cast and the whole batch (r101-3 — the retry-open
	// contract keeps the round reproducible without a pollution deadlock).
	next := shadowStreakNext(prevStreak, prevDir, thisDir, true, true)
	_ = shadowExecFn(ctx, r,
		`INSERT INTO meta (key, value) VALUES ($1, $2), ($3, '1'), ($4, '1'), ($5, $6)
		 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$3", "$4"),
		streakKey, strconv.Itoa(next), firedKey, roundsKey, shadowGateKey, stamp)
}
