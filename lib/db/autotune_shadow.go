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

type ShadowRule struct {
	Signal      string
	Query       string
	Key         string
	Direction   string
	SafeRange   [2]float64
	Auto        string
	SuccessPair string
	ObsWindowD  int
}

type ShadowObs struct {
	ReadEventsN int
	ZeroN       int
	ReclassN    int
}

type ShadowVerdict struct {
	Fired    bool
	SampleOk bool
	Ratio    int
	Detail   string
}

type ShadowVerdictFn func(rule ShadowRule, obs ShadowObs, th store.Thresholds) ShadowVerdict

var (
	shadowRulesFn   func() []ShadowRule
	shadowVerdictFn ShadowVerdictFn
)

func RegisterShadowCircuit(rules func() []ShadowRule, verdict ShadowVerdictFn) {
	shadowRulesFn = rules
	shadowVerdictFn = verdict
}

const shadowGateKey = "last_shadow_at"

var shadowGateCheckedAt atomic.Int64

const shadowGateCacheTTL = time.Hour

func shadowGateProbe(now time.Time) bool {
	if last := shadowGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < shadowGateCacheTTL {
		return false
	}
	shadowGateCheckedAt.Store(now.Unix())
	return true
}

func ResetShadowGateCacheForTest() {
	shadowGateCheckedAt.Store(0)
}

var (
	shadowQueryRowFn = func(ctx context.Context, r *Repo, query string, args ...any) pgx.Row {
		return r.pool.QueryRow(ctx, query, args...)
	}
	shadowExecFn = func(ctx context.Context, r *Repo, query string, args ...any) error {
		_, err := r.pool.Exec(ctx, query, args...)
		return err
	}
)

func shadowProposal(rule ShadowRule, cur float64) (float64, bool) {
	bound := rule.SafeRange[0]
	if rule.Direction == "up" {
		bound = rule.SafeRange[1]
	}
	return bound, bound != cur
}

func shadowMoveDir(cur, new float64) int {
	switch {
	case new < cur:
		return -1
	case new > cur:
		return 1
	}
	return 0
}

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

func shadowValueText(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func shadowBumpCase(keys ...string) string {
	branches := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		branches = append(branches,
			fmt.Sprintf("WHEN meta.key = %s AND meta.value ~ '^[0-9]{1,18}$' THEN (meta.value::bigint + 1)::text", k),
			fmt.Sprintf("WHEN meta.key = %s THEN '1'", k))
	}
	return "CASE " + strings.Join(branches, " ") + " ELSE EXCLUDED.value END"
}

type shadowRuleState struct {
	curText, streakText, prevNewText, prevCurText string
}

type shadowRoundState struct {
	mode, last, zmin, zwarn string
	rules                   []shadowRuleState
}

func (r *Repo) maybeShadowRound(ctx context.Context) {
	rulesSrc, verdict := shadowRulesFn, shadowVerdictFn
	if rulesSrc == nil || verdict == nil {
		return
	}
	if !shadowGateProbe(time.Now().UTC()) {
		return
	}

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
		return
	}
	if state.mode != "shadow" {
		return
	}
	if !pruneGateDue(state.last, time.Now().UTC()) {
		return
	}
	th := shadowThresholds(state.zmin, state.zwarn)
	stamp := time.Now().UTC().Format(time.RFC3339)
	for i, rule := range eligible {
		r.shadowRoundRule(ctx, rule, state.rules[i], verdict, th, stamp)
	}
}

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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

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

func shadowDefaultText(key string) string {
	for _, m := range wire.ThresholdMetas() {
		if m.Key == key {
			return thresholdValueText(m.Default)
		}
	}
	return ""
}

func (r *Repo) shadowRoundRule(ctx context.Context, rule ShadowRule, rs shadowRuleState, verdict ShadowVerdictFn, th store.Thresholds, stamp string) {
	curText := rs.curText
	if curText == "" {
		curText = shadowDefaultText(rule.Key)
	}
	cur, err := strconv.ParseFloat(curText, 64)
	if err != nil {
		return
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

		_ = shadowExecFn(ctx, r,
			`INSERT INTO meta (key, value) VALUES ($1, '1'), ($2, '1'), ($3, $4)
			 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$1", "$2"),
			firedKey, roundsKey, shadowGateKey, stamp)
		return
	}
	thisDir := shadowMoveDir(cur, new)

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

	if err := shadowExecFn(ctx, r,
		`INSERT INTO tuning_predictions
		   (trigger, key, cur, new, pred_zero_near_pct, pred_reclass_n, sample_n, rationale)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rule.Signal, rule.Key, shadowValueText(cur), newText,
		predPct, whatIf.ReclassN, whatIf.ReadEventsN, rationale); err != nil {
		return
	}

	next := shadowStreakNext(prevStreak, prevDir, thisDir, true, true)
	_ = shadowExecFn(ctx, r,
		`INSERT INTO meta (key, value) VALUES ($1, $2), ($3, '1'), ($4, '1'), ($5, $6)
		 ON CONFLICT (key) DO UPDATE SET value = `+shadowBumpCase("$3", "$4"),
		streakKey, strconv.Itoa(next), firedKey, roundsKey, shadowGateKey, stamp)
}
