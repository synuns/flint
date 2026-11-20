// the maintain command — the agent curation session entry point (spec §8).
// `flint maintain --check` (threshold reporting only), `--invalidate --id N --reason`,
// and `--merge --from N --to M`. Humans (agents) make the verdicts; the CLI only wires the update paths.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// MaintStore — the store operations maintain needs (Store + admin updates and tallies).
// State-transition updates (invalidate, supersede) delegate to the single db.Repo path —
// Invalidate (batch, with the rows-affected stewardship built in) and Supersede (recording the
// superseded_by lineage — the dual text-marker model abolished).
type MaintStore interface {
	wire.Store
	Counts(ctx context.Context) (db.Counts, error)
	DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error)
	Invalidate(ctx context.Context, ids []int, reason string) (int64, error)
	Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
	// the invalidated cleanup DELETE follows the same principle (the write guard —
	// writes_through_repo_test).
	PruneInvalidated(ctx context.Context, before time.Time) (int64, error)
	// the recall-event retention cleanup DELETE also delegates to the single Repo path
	// (recall_event_retention_days — R42-D).
	PruneRecallEvents(ctx context.Context, before time.Time) (int64, error)
	Merge(ctx context.Context, fromID, toID int) error
}

// ConflictStore — the extra lookup needed for contradiction-candidate reporting (groups with summaries).
// A conditional interface separate from MaintStore — existing fakes stay as-is.
type ConflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

// maintainValueFlags — all value-taking flags (mode-ownership validation targets).
var maintainValueFlags = []string{"--days", "--limit", "--id", "--from", "--to", "--reason",
	"--ids", "--supersede", "--by"}

// maintainModeFlags — the value flags allowed per mode. Unrelated flags are not silently
// ignored but rejected with an error (e.g. --days or --limit flowing under a non-stale mode).
var maintainModeFlags = map[string]map[string]bool{
	"--check":      {},
	"--meta-audit": {}, // meta key hygiene report (read-only)
	// --prune-invalidated N — cleans invalidated rows past N days (dry-run by default,
	// --commit confirms — inheriting the expire-pending convention. The days are the mode's value).
	"--prune-invalidated": {},
	"--dup-suspects":      {"--limit": true},
	"--conflict-suspects": {"--limit": true},
	"--stale":             {"--days": true, "--limit": true},
	"--invalidate-stale":  {"--days": true, "--limit": true},
	"--invalidate":        {"--id": true, "--reason": true, "--ids": true},
	"--merge":             {"--from": true, "--to": true},
	"--supersede":         {"--supersede": true, "--by": true},
	// --expire-pending N — the days are taken as the mode's value (not --days).
	"--expire-pending": {},
	// decay decay-candidate report (read-only — execution is delegated to --invalidate).
	"--decay-audit": {"--limit": true},
	// v1.13.0 T4: the stale-score report (read-only, dry-run by default —
	// disposition is delegated to --invalidate; no new execution flags).
	"--age-audit": {"--limit": true},
}

// maintainYesModes — the modes where --yes (execution confirmation) is meaningful. In the
// other modes, --yes is also rejected with an error instead of silent ignoring.
var maintainYesModes = map[string]bool{"--invalidate": true, "--invalidate-stale": true}

// maintainCommitModes — the modes where --commit (transition confirmation) is meaningful. In the
// other modes, --commit is rejected with an error by the same principle as --yes.
var maintainCommitModes = map[string]bool{"--expire-pending": true, "--prune-invalidated": true}

// MaintainRetryDelays — the Retry intervals for the maintenance write path (global constraint:
// writes come with 40001-handling Retry). Tests substitute immediate retries.
var MaintainRetryDelays = db.DefaultDelays

// MaintainRetryDelaysFn — the Retry-interval seam. When the cli maintain_v08_test injects
// immediate retries by swapping the cli var (re-exported via ops_alias_2), the wrapper reads it
// at call time and propagates (inheriting the composePsFn convention).
var MaintainRetryDelaysFn = func() []time.Duration { return MaintainRetryDelays }

// retryWrite — wraps a write (UPDATE + Commit) closure in Retry (the global constraint).
func retryWrite(ctx context.Context, fn func() error) error {
	return db.Retry(ctx, fn, MaintainRetryDelaysFn())
}

// maintainUsageLine — the allowed-flags guidance (the common suffix of the invalid-arg
// error message — for agent self-correction, alongside the error:"usage" transition).
const maintainUsageLine = "usage: flint maintain --check | --dup-suspects [--limit M] | --conflict-suspects [--limit M] | --stale [--days N] [--limit M] | --invalidate-stale [--yes] [--days N] [--limit M] | --invalidate (--id N --reason S | --ids 1,2,3 --yes) | --merge --from N --to M | --supersede OLD --by NEW | --expire-pending N [--commit] | --meta-audit | --prune-invalidated [N] [--commit] | --decay-audit [--limit M] | --age-audit [--limit M] | --tui (opt-in, run alone)"

// maintainUsageErrf — assembles the invalid-arg error (the approve.approveUsageErr
// convention — a UsageError type + the allowed-flags suffix. The exit code stays with the caller).
func maintainUsageErrf(format string, a ...any) *wire.UsageError {
	return wire.UsageErrorf(format+" — "+maintainUsageLine, a...)
}

// RunMaintain — parses the sub-options then runs per mode. --check only reports (exit 0);
// the update family succeeds with 0, missing targets (ErrNotFound) 1, else MapExit.
func RunMaintain(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	ms, ok := st.(MaintStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	// parsing uses the wire.ParseArgs spec table — semantics 100% preserved (always consume a
	// value, last mode wins, unknown exits 3). Value validation (numbers, reason guards, ids
	// parsing) is reproduced below in occurrence order (AllOccs) — the existing encounter-order
	// error precedence stays as-is.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--check"}, {Name: "--dup-suspects"}, {Name: "--conflict-suspects"},
			{Name: "--merge"}, {Name: "--stale"}, {Name: "--invalidate"},
			{Name: "--invalidate-stale"}, {Name: "--meta-audit"}, {Name: "--decay-audit"},
			{Name: "--age-audit"},
			// --expire-pending and --prune-invalidated are mode + value (N days).
			{Name: "--expire-pending", Value: true}, {Name: "--prune-invalidated", Value: true},
			{Name: "--days", Value: true}, {Name: "--limit", Value: true},
			{Name: "--id", Value: true}, {Name: "--from", Value: true}, {Name: "--to", Value: true},
			{Name: "--reason", Value: true}, {Name: "--supersede", Value: true}, {Name: "--by", Value: true},
			{Name: "--ids", Value: true},
			{Name: "--commit"}, {Name: "--yes"},
		},
		Usage: maintainUsageLine,
	})
	if perr != nil {
		// invalid flags follow the invalid-arg contract exit 3 (consistent with doctor, tuning,
		// diff, and import).
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	mode := ""
	id, from, to := 0, 0, 0
	old, by := 0, 0                  // --supersede OLD --by NEW
	days, limit := 0, 20             // --stale: days=0 -> the meta stale_days (default 90) fallback
	dupLimit := SuspectsDefaultLimit // the --dup-suspects default (spec R4: --limit M defaults to 10)
	expireDays := 0                  // --expire-pending N — no default (preventing accidental deletion)
	reason := ""
	yes, commit := res.Given("--yes"), res.Given("--commit")
	var idList []int // the --ids parse result (a snapshot — no reevaluation)
	// mode election: the last occurrence among the mode flags wins (inheriting the old loop overwrite).
	for _, name := range res.FlagOrder() {
		switch name {
		case "--check", "--dup-suspects", "--conflict-suspects", "--merge", "--stale",
			"--invalidate", "--invalidate-stale", "--meta-audit", "--expire-pending",
			"--prune-invalidated", "--supersede", "--decay-audit", "--age-audit":
			mode = name
		}
	}
	// value-flag validation and confirmation — in occurrence order (reproducing the old encounter order).
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--days", "--limit":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number", o.Name)))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				// an invalid --limit value is invalid-arg exit 3 like the read, brief,
				// and audit family (including a missing value).
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 3
			}
			if o.Name == "--days" {
				days = n
			} else {
				limit, dupLimit = n, n
			}
		case "--expire-pending", "--prune-invalidated": // common parsing of the N value
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number of days", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			expireDays = n
		case "--id", "--from", "--to":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			switch o.Name {
			case "--id":
				id = n
			case "--from":
				from = n
			case "--to":
				to = n
			}
		case "--reason":
			if !o.HasValue || o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --reason requires a value")))
				return 1
			}
			// a typo where the value absorbs a following flag ('--reason
			// ' --commit' -> the reason "--commit") is invalid-arg exit 3 via the shared guard
			// (the same contract as backup --out and restore --file).
			if err := knowledge.ValidateFlagValue("reason", o.Value); err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return 3
			}
			reason = o.Value
		case "--supersede":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			old = n
		case "--by":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			by = n
		case "--ids":
			if !o.HasValue || o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --ids requires a value")))
				return 1
			}
			ids, perr := parseIDList(o.Value)
			if perr != nil {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%w", perr)))
				return 1
			}
			idList = ids
		}
	}
	seen := map[string]bool{}
	for _, f := range maintainValueFlags {
		seen[f] = res.Given(f)
	}

	// mode-ownership validation: value flags the mode does not take are errors (no silent ignoring).
	if allowed, ok := maintainModeFlags[mode]; ok {
		for _, f := range maintainValueFlags {
			if seen[f] && !allowed[f] {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s is not valid with %s", f, mode)))
				return 1
			}
		}
	}
	// --yes is the execution-confirmation marker — flowing into a mode without it is an error (the same principle).
	if yes && !maintainYesModes[mode] {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --yes is not valid with %s", mode)))
		return 1
	}
	// --commit follows the same principle (the transition marker of dry-run-default modes).
	if commit && !maintainCommitModes[mode] {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --commit is not valid with %s", mode)))
		return 1
	}

	switch mode {
	case "--check":
		return maintainCheck(ctx, ms, stdout)
	case "--meta-audit":
		return maintainMetaAudit(ctx, ms, stdout)
	case "--decay-audit":
		return maintainDecayAudit(ctx, ms, stdout, limit)
	case "--age-audit":
		return maintainAgeAudit(ctx, ms, stdout, limit)
	case "--prune-invalidated":
		return maintainPruneInvalidated(ctx, ms, stdout, expireDays, commit)
	case "--dup-suspects":
		return maintainDupSuspects(ctx, ms, stdout, dupLimit)
	case "--conflict-suspects":
		cs, ok := ms.(ConflictStore)
		if !ok {
			fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
			return 1
		}
		return maintainConflictSuspects(ctx, cs, stdout, dupLimit)
	case "--stale":
		return maintainStale(ctx, ms, stdout, days, limit)
	case "--invalidate-stale":
		return maintainInvalidateStale(ctx, ms, stdout, days, limit, yes)
	case "--supersede":
		if old == 0 || by == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --supersede requires OLD and --by NEW"))))
			return 1
		}
		return maintainSupersede(ctx, ms, stdout, old, by)
	case "--invalidate":
		if len(idList) > 0 {
			if !yes { // spec §2: --ids requires --yes alongside (execution confirmation)
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate --ids requires --yes"))))
				return 1
			}
			return maintainInvalidateIDs(ctx, ms, stdout, idList)
		}
		if id == 0 || reason == "" {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate requires --id and --reason"))))
			return 1
		}
		// inheriting the idempotence contract of the original single path
		// (admin.go Invalidate). Already-invalidated ids are no-op successes with no re-update or
		// re-commit — re-stamping invalid_at with now() would defer the
		// prune-invalidated timing as a side effect. Missing ids are ErrNotFound (the original
		// ensureExists contract). The write itself stays on the single Repo.Invalidate path.
		chk, err := wire.QueryOnce(ctx, ms,
			`SELECT invalid_at FROM knowledge WHERE id = $1`, []any{id})
		if err != nil {
			return adminErrLine(stdout, err)
		}
		if len(chk) == 0 {
			return adminErrLine(stdout, fmt.Errorf("%w: invalidate: id=%d not found", db.ErrNotFound, id))
		}
		if chk[0][0] == nil {
			if _, err := ms.Invalidate(ctx, []int{id}, reason); err != nil {
				return adminErrLine(stdout, err)
			}
			if _, err := ms.Commit(ctx, fmt.Sprintf(
				"invalidate: id=%d reason: %s (invalidation path) [session:0]", id, reason)); err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
		}
		fmt.Fprintf(stdout, `{"status":"invalidated","id":%d}`+"\n", id)
		return 0
	case "--expire-pending":
		// no need for a second expireDays<=0 defense — the parser
		// already passes only positive values.
		return maintainExpirePending(ctx, ms, stdout, expireDays, commit)
	case "--merge":
		if from == 0 || to == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --merge requires --from and --to"))))
			return 1
		}
		if err := ms.Merge(ctx, from, to); err != nil {
			return adminErrLine(stdout, err)
		}
		fmt.Fprintf(stdout, `{"status":"merged","from":%d,"to":%d}`+"\n", from, to)
		return 0
	default:
		// bare usage (no mode given) is also unified to invalid-arg exit 3.
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", maintainUsageLine)))
		return 3
	}
}

// parseIDList — parses a "1,2,3"-style id snapshot (rejecting empty tokens, non-positive, and non-numeric).
func parseIDList(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("maintain: --ids invalid id %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// staleItem — a maintain --stale report item. Distinguishes the never_read bucket
// (read_count=0) as the reason (following Devin's "unused notes"). Reporting only.
type staleItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	NeverRead bool   `json:"never_read"`
	Reason    string `json:"reason"`     // never_read (unreferenced) | stale (elapsed)
	StaleDays int    `json:"stale_days"` // elapsed days from the reference time (last_read_at, else ingested_at)
}

// maintainDupSuspects — reports same-topic 2+-row groups among active (report-only,
// exit 0). Verdicts (merge, invalidate) are performed by the agent via the existing --merge
// and --invalidate — keeping the no-SQL-auto-verdict principle (spec R4).
func maintainDupSuspects(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	groups, err := ms.DupGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, DupSuspectsJSON(groups))
	return 0
}

// DupSuspectsJSON — the --dup-suspects one-line JSON output. To avoid null ids,
// it normalizes to an empty slice (nil would yield "ids":null, violating the shape contract).
func DupSuspectsJSON(groups []db.DupGroup) string {
	type dupGroupJSON struct {
		Topic string `json:"topic"`
		IDs   []int  `json:"ids"`
	}
	out := struct {
		Status string         `json:"status"`
		Groups []dupGroupJSON `json:"groups"`
	}{Status: "dup-suspects", Groups: make([]dupGroupJSON, 0, len(groups))}
	for _, g := range groups {
		ids := g.IDs
		if ids == nil {
			ids = []int{}
		}
		out.Groups = append(out.Groups, dupGroupJSON{Topic: render.Text(g.Topic), IDs: ids}) // via render.Text (inheriting output re-sanitization)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"dup-suspects","groups":[]}`
	}
	return string(b)
}

// ConflictDupThreshold — the contradiction classification criterion (ruling): a group whose
// summary similarity is at or above this value is a duplicate candidate (the existing --dup-suspects);
// below it, a contradiction candidate. An independent value with a different purpose from the
// suggestion threshold (0.15). The default's single source is the thresholds registry
// (store.DefaultThresholds); at runtime the meta conflict_dup_threshold override takes
// precedence via the classifyConflictAt injection. It is a var for the registry reference (the value-immutability contract).
var ConflictDupThreshold = store.DefaultThresholds().ConflictDupThreshold

// SuspectsDefaultLimit — the shared default cap of --dup-suspects and --conflict-suspects
// (spec R4: --limit M defaults to 10). The doctor conflicts check also
// bounds its group lookup with this cap — preventing unbounded scans.
const SuspectsDefaultLimit = 10

// MaintainTUILoadLimit — the explicit cap the maintain TUI loader uses when querying the
// 3 reports (--dup-suspects, --conflict-suspects, --stale). The defaults
// (dup/conflict SuspectsDefaultLimit 10, stale 20) are for human interactivity,
// but the TUI does not show truncation on screen — replaced with an explicit cap
// (abolishing invisible truncation). lib/tui's loadMaintainSuspects references it as the
// single source.
const MaintainTUILoadLimit = 500

// ConflictDice — the rune-bigram multiset Dice coefficient. The implementation delegates
// to lib/query's BigramDiceMultiset (it differs from the set version BigramDice in repeated-bigram
// counts — the 0.6 threshold is a multiset-based ruling, so the existing classification behavior is kept).
func ConflictDice(a, b string) float64 {
	return query.BigramDiceMultiset(a, b)
}

// ClassifyConflict — whether two summaries contradict (default threshold): similarity below
// ConflictDupThreshold means a contradiction candidate (different claims on the same topic),
// at or above a duplicate candidate. The override path is classifyConflictAt.
func ClassifyConflict(summA, summB string) bool {
	return classifyConflictAt(summA, summB, ConflictDupThreshold)
}

// classifyConflictAt — the threshold-injected variant (ClassifyConflict). Consumers
// (maintain --conflict-suspects, doctor conflicts) pass the loadThresholds
// meta override value.
func classifyConflictAt(summA, summB string, dupThreshold float64) bool {
	return ConflictDice(summA, summB) < dupThreshold
}

// ConflictSummaryPrefix — the first 40 chars of a summary for reporting (delegating to the
// single render.TruncateRunes implementation).
func ConflictSummaryPrefix(s string) string {
	return render.TruncateRunes(s, 40)
}

// IsConflictGroup — whether a group is a contradiction candidate: if any pair's similarity is
// below the threshold (dupThreshold), the group is taken to mix distinct claims (only all-pairs
// identical/similar counts as duplicate). The threshold is injected by the caller with the loadThresholds value.
func IsConflictGroup(g db.ConflictGroup, dupThreshold float64) bool {
	for i := 0; i < len(g.Summaries); i++ {
		for j := i + 1; j < len(g.Summaries); j++ {
			if classifyConflictAt(g.Summaries[i], g.Summaries[j], dupThreshold) {
				return true
			}
		}
	}
	return false
}

// maintainConflictSuspects — among same-topic active groups, reports only those with below-
// threshold summary similarity (contradiction candidates) (report-only, exit 0). Grouping inherits
// --dup-suspects; the output adds the first 40 chars of summaries to that shape. The classification
// threshold prefers the meta conflict_dup_threshold override (lookup failure falls back to the default).
// Verdicts (invalidate, promote) are performed by the agent via the existing --invalidate and
// --supersede — keeping the no-SQL-auto-verdict principle.
func maintainConflictSuspects(ctx context.Context, cs ConflictStore, stdout io.Writer, limit int) int {
	groups, err := cs.ConflictGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// the ConflictStore signature has no Query — if the concrete type implements Store (and
	// MaintStore), it reads the threshold from that (the same principle as the read path).
	th := store.DefaultThresholds()
	if ms, ok := cs.(wire.Store); ok {
		if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
			th = loaded
		}
	}
	out := make([]db.ConflictGroup, 0, len(groups))
	for _, g := range groups {
		if IsConflictGroup(g, th.ConflictDupThreshold) {
			out = append(out, g)
		}
	}
	fmt.Fprintln(stdout, conflictSuspectsJSON(out))
	return 0
}

// conflictSuspectsJSON — the --conflict-suspects one-line JSON output. Inheriting the
// dup-suspects shape + the first 40 chars of summaries. nil-slice normalization prevents null.
func conflictSuspectsJSON(groups []db.ConflictGroup) string {
	type conflictGroupJSON struct {
		Topic     string   `json:"topic"`
		IDs       []int    `json:"ids"`
		Summaries []string `json:"summaries"`
	}
	out := struct {
		Status string              `json:"status"`
		Groups []conflictGroupJSON `json:"groups"`
	}{Status: "conflict-suspects", Groups: make([]conflictGroupJSON, 0, len(groups))}
	for _, g := range groups {
		ids := g.IDs
		if ids == nil {
			ids = []int{}
		}
		sums := make([]string, 0, len(g.Summaries))
		for _, s := range g.Summaries {
			sums = append(sums, ConflictSummaryPrefix(render.Text(s))) // via render.Text (inheriting output re-sanitization)
		}
		out.Groups = append(out.Groups, conflictGroupJSON{Topic: render.Text(g.Topic), IDs: ids, Summaries: sums}) // via render.Text
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"conflict-suspects","groups":[]}`
	}
	return string(b)
}

// maintainStale — reports long-unreferenced items among active (report-only, exit 0).
// Adds a per-item reason and a summary (summary "N to invalidate").
func maintainStale(ctx context.Context, ms MaintStore, stdout io.Writer, daysOverride, limit int) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := staleReport(items, days)
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, out)
	return 0
}

// listStale — computes the stale targets (shared by the --stale report and --invalidate-stale application).
// Targets: (last_read_at < now-days) OR (read_count = 0 AND ingested_at < now-days).
// The boundary is strict — the exact cutoff moment is not included. Cron-friendly.
func listStale(ctx context.Context, ms MaintStore, daysOverride, limit int) ([]staleItem, int, error) {
	th, err := wire.LoadThresholds(ctx, ms)
	if err != nil {
		return nil, 0, err
	}
	days := th.StaleDays
	if daysOverride > 0 {
		days = daysOverride // --days N takes precedence over meta stale_days
	}
	// The cutoff is computed in Go and passed as a timestamptz parameter. An SQL
	// ($1 * interval) product makes Doltgres infer $1 as interval (OID 1186) and pgx fails the
	// int encoding (maintain --stale exit 1). The SQL and Go filters use the same cutoff,
	// so the boundaries match too.
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT id, topic, last_read_at, ingested_at, read_count
		FROM knowledge
		WHERE status = 'active' AND invalid_at IS NULL
		  AND ((last_read_at IS NOT NULL AND last_read_at < $1)
		   OR (read_count = 0 AND ingested_at < $1))
		ORDER BY COALESCE(last_read_at, ingested_at) ASC
		LIMIT $2`, []any{cutoff, limit})
	if err != nil {
		return nil, days, err
	}
	items := []staleItem{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		readCount, _ := render.CellToInt(r[4])
		ref, ok := AnyToTime(r[2]) // last_read_at first, else ingested_at
		if !ok || ref.IsZero() {
			ref, ok = AnyToTime(r[3])
		}
		if !ok || !ref.Before(cutoff) { // strictly before the cutoff — exclude rows at or after it (active)
			continue
		}
		reason := "stale"
		if readCount == 0 {
			reason = "never_read"
		}
		items = append(items, staleItem{
			ID:        id,
			Topic:     render.Text(topic), // via render.Text (inheriting output re-sanitization)
			NeverRead: readCount == 0,
			Reason:    reason,
			StaleDays: int(time.Since(ref).Hours() / 24),
		})
	}
	return items, days, nil
}

// staleReport — the dry-run (default) one-line JSON output. The --stale and --invalidate-stale
// dry runs share this shape (spec §2).
func staleReport(items []staleItem, days int) (string, error) {
	out, err := json.Marshal(struct {
		Status  string      `json:"status"`
		Days    int         `json:"days"`
		Count   int         `json:"count"`
		Items   []staleItem `json:"items"`
		Summary string      `json:"summary"`
	}{Status: "ok", Days: days, Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d to invalidate", len(items))})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// maintainInvalidateStale — batch invalidation (spec §2). Dry-run by default (without the
// marker, identical output to --stale, no updates). With --yes, all ids from the listing
// get UPDATE (status='invalidated', invalid_at=now) + Commit — the write comes with Retry.
func maintainInvalidateStale(ctx context.Context, ms MaintStore, stdout io.Writer,
	daysOverride, limit int, apply bool) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if !apply || len(items) == 0 {
		out, mErr := staleReport(items, days)
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, out)
		return 0
	}
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	code := invalidateIDs(ctx, ms, stdout, ids, "invalidate-stale")
	if code != 0 {
		return code
	}
	return 0
}

// maintainInvalidateIDs — the --invalidate --ids snapshot path. Applies the given ids
// without dry-run reevaluation (the plan-file pattern). Nonexistent ids exit 1.
func maintainInvalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int) int {
	return invalidateIDs(ctx, ms, stdout, ids, "invalidate --ids")
}

// invalidateIDs — the common batch invalidation: existence check -> transition (Repo
// delegation) + Commit (Retry). The UPDATE literal stays on the single db.Repo.Invalidate path.
func invalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int, op string) int {
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE id = ANY($1)`, []any{ids})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	found := map[int]bool{}
	for _, r := range rows {
		if id, ok := render.CellToInt(r[0]); ok {
			found[id] = true
		}
	}
	var missing []int
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: %s: nonexistent ids %v", op, missing)))
		return 1
	}
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Invalidate(ctx, ids, op); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"%s: invalidated %d rows (batch invalidation path) [session:0]", op, len(ids)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "invalidated", Count: len(ids), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// expireItem — an --expire-pending listing item. The pending elapsed days are measured
// from valid_at, and the summary is listed truncated to the first 40 chars (inheriting the conflict prefix).
type expireItem struct {
	ID      int    `json:"id"`
	Topic   string `json:"topic"`
	Summary string `json:"summary"`
	AgeDays int    `json:"age_days"` // elapsed days since valid_at
}

// listExpiredPending — computes the pending expiry targets: status='pending' AND
// invalid_at IS NULL AND valid_at < now-days. The cutoff is computed in Go and passed as a
// timestamptz parameter (inheriting the listStale convention — avoiding Doltgres interval
// inference), and the same boundary is rechecked with a Go filter.
// expireListLimit — the dry-run listing cap (unbounded-JSON defense).
const expireListLimit = 20

func listExpiredPending(ctx context.Context, ms MaintStore, days int) ([]expireItem, time.Time, error) {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	// the dry-run listing cap — the same LIMIT convention as listStale
	// (default 20), preventing an unbounded JSON listing.
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT id, topic, summary, valid_at
		FROM knowledge
		WHERE status = 'pending' AND invalid_at IS NULL AND valid_at < $1
		ORDER BY valid_at ASC
		LIMIT $2`, []any{cutoff, expireListLimit})
	if err != nil {
		return nil, cutoff, err
	}
	items := []expireItem{}
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		summary, _ := r[2].(string)
		va, ok := AnyToTime(r[3])
		if !ok || !va.Before(cutoff) { // strictly before the cutoff — exclude rows at or after it (not yet expired)
			continue
		}
		items = append(items, expireItem{
			ID:      id,
			Topic:   render.Text(topic),                          // via render.Text
			Summary: ConflictSummaryPrefix(render.Text(summary)), // via render.Text
			AgeDays: int(time.Since(va).Hours() / 24),
		})
	}
	return items, cutoff, nil
}

// maintainExpirePending — pending expiry. Dry-run by default: only lists the targets
// (inheriting the staleReport shape), exit 0. With --commit, all listed ids are
// invalidated (status='invalidated' and invalid_at=now recorded — the single
// db.Repo.Invalidate path) + Commit — inheriting the existing invalidation path (no new state,
// schema unchanged). The write comes with Retry.
func maintainExpirePending(ctx context.Context, ms MaintStore, stdout io.Writer, days int, apply bool) int {
	items, _, err := listExpiredPending(ctx, ms, days)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if !apply || len(items) == 0 {
		out, mErr := json.Marshal(struct {
			Status  string       `json:"status"`
			Days    int          `json:"days"`
			Count   int          `json:"count"`
			Items   []expireItem `json:"items"`
			Summary string       `json:"summary"`
		}{Status: "expire-pending", Days: days, Count: len(items), Items: items,
			Summary: fmt.Sprintf("%d to invalidate", len(items))})
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	// the update is limited to the listed id snapshot (instead of reevaluating the
	// invalidation condition) — the dry-run targets and the confirmed targets match. The UPDATE
	// literal stays on the single db.Repo.Invalidate path.
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Invalidate(ctx, ids, "expire-pending"); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"expire-pending: invalidated %d rows (pending expiry path) [session:0]", len(items)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// curation counter — the count of confirmed (invalidated) rows. Not counted in dry-run.
	knowledge.BumpCurationCounter(ctx, ms, "maintain_expire", len(items))
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Days   int    `json:"days"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "invalidated", Days: days, Count: len(items), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// maintainSupersede — `--supersede OLD --by NEW`(spec §2 superseded).
// Only when OLD is active: status='superseded'-style transition with superseded_by=NEW and
// invalid_at=NEW's valid_at (following Zep's t_invalid=t_valid). The write comes with Retry.
// NEW missing -> exit 1; OLD==NEW -> exit 3 (invalid spec); OLD non-active -> exit 1.
func maintainSupersede(ctx context.Context, ms MaintStore, stdout io.Writer, oldID, newID int) int {
	if oldID == newID {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede OLD and --by NEW are the same (id=%d)", oldID)))
		return 3
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT (SELECT status FROM knowledge WHERE id = $1),
		       (SELECT valid_at FROM knowledge WHERE id = $2)`,
		[]any{oldID, newID})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	var oldStatus string
	var newValidAt any
	if len(rows) > 0 && len(rows[0]) >= 2 {
		oldStatus, _ = rows[0][0].(string)
		newValidAt = rows[0][1]
	}
	if newValidAt == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: NEW id=%d not found", newID)))
		return 1
	}
	if oldStatus != "active" { // including absent (empty status) — only active gets promoted
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: id=%d is not active (status %q)", oldID, oldStatus)))
		return 1
	}
	at, ok := AnyToTime(newValidAt)
	if !ok {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: cannot read NEW valid_at id=%d", newID)))
		return 1
	}
	// The update UPDATE and the 0-row race stewardship stay on the single db.Repo.Supersede
	// path (rows-affected stewardship built in — inheriting the result-verification stewardship).
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Supersede(ctx, oldID, newID, at); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"supersede: old=%d by=%d (supersession path) [session:0]", oldID, newID))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	knowledge.BumpCurationCounter(ctx, ms, "maintain_supersede", 1) // curation counter — 1 on success
	fmt.Fprintf(stdout, `{"status":"superseded","old":%d,"superseded_by":%d}`+"\n",
		oldID, newID)
	return 0
}

// ── decay scoring formulas and the --decay-audit report ──

// decayNow — the decay reference-time seam (a var for test injection).
var decayNow = time.Now

// decayAuditItem — a --decay-audit listing item. Reporting only —
// archiving is performed by the existing --invalidate with its approval system (--yes/--reason)
// (not automatic — inheriting the no-SQL-auto-verdict principle).
type decayAuditItem struct {
	ID           int    `json:"id"`
	Score        int    `json:"score"`
	LastRecalled string `json:"last_recalled"` // RFC3339 (candidates all have recall history)
	Effective    int    `json:"effective"`
	Recommend    string `json:"recommend"` // "archive" | "" (no recommendation)
}

// maintainDecayAudit — the decay-candidate report (read-only, exit 0). Targets: among active,
// rows with recall history where decay has progressed (effective < cnt) — never-recalled rows
// have no events and are not candidates. The score source switched in v1.9.0 to the derived
// path: a recall_events aggregate join (per-knowledge event count cnt and last recall time
// last_at — see the single definition lib/knowledge RecallAggregateSQL) and it does not
// reference the v11 physical columns (recall_score and last_recalled_at — dropped in schema
// v12). decay_rate and archive_below prefer the meta overrides (an unset
// decay_rate=0 is a key not covered by ApplyThresholdsFallback, so the default 1 fallback is
// applied here — consumer-side completeness). The item cap is --limit (default 20, inheriting
// the stale convention).
func maintainDecayAudit(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	th := store.DefaultThresholds()
	if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
		th = loaded
	}
	if th.DecayRate <= 0 {
		th.DecayRate = store.DefaultThresholds().DecayRate
	}
	if limit <= 0 {
		limit = 20
	}
	// inheriting the Go-computed cutoff + parameter-passing convention. The sort is last recall
	// time ASC — the least recently recalled (= most decayed) rows come first. last_at IS NOT
	// NULL excludes history-less rows (LEFT JOIN misses) on the server side. v1.13.0 T2/T3:
	// k.tags rides along so the effective score goes through EffectiveScoreFor — a
	// :vol:immutable: row keeps its count (eff == cnt) and drops out below.
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, re.cnt, re.last_at, k.tags
		FROM knowledge k
		LEFT JOIN (`+knowledge.RecallAggregateSQL+`) re ON re.knowledge_id = k.id
		WHERE k.status = 'active' AND k.invalid_at IS NULL AND re.last_at IS NOT NULL
		ORDER BY re.last_at ASC
		LIMIT $1`, []any{limit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	now := decayNow()
	items := []decayAuditItem{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		score, _ := render.CellToInt(r[1])
		last, ok := AnyToTime(r[2])
		if !ok || last.IsZero() {
			continue
		}
		tags, _ := r[3].(string) // missing/NULL tags — no exemption (the pure path)
		eff := knowledge.EffectiveScoreFor(score, last, now, th.DecayRate, tags)
		if eff >= score {
			continue // no decay (elapsed under 24h, exempt, etc.) — not a candidate
		}
		rec := ""
		if th.ArchiveBelow > 0 && float64(eff) < th.ArchiveBelow {
			rec = "archive"
		}
		items = append(items, decayAuditItem{
			ID:           id,
			Score:        score,
			LastRecalled: last.Format(time.RFC3339),
			Effective:    eff,
			Recommend:    rec,
		})
		if len(items) >= limit {
			break
		}
	}
	out, mErr := json.Marshal(struct {
		Status       string           `json:"status"`
		DecayRate    float64          `json:"decay_rate"`
		ArchiveBelow float64          `json:"archive_below"`
		Count        int              `json:"count"`
		Items        []decayAuditItem `json:"items"`
		Summary      string           `json:"summary"`
	}{Status: "decay-audit", DecayRate: th.DecayRate, ArchiveBelow: th.ArchiveBelow,
		Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d decayed — apply via maintain --invalidate (approval required)", len(items))})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// ── v1.13.0 T3: the stale score (2 weighted signals — the --age-audit scoring source) ──

// The stale score formula (the gate ruling — weights are code constants,
// part of the formula, NOT tuning values; only the report marking threshold
// is a thresholds.go key, T5):
//
//	score = round(100 × (0.7 × min(1, ttl_days / 180)
//	                     + 0.3 × min(1, corr_count / 3)))
//
// ttl_days   — signal ①: integer days since COALESCE(last recall
//
//	(recall_events re.last_at), ingested_at) — the listStale
//	convention (never-recalled rows age from ingestion; 23h59m
//	= 0 days, the EventEffectiveScore unit).
//
// corr_count — signal ②: same-topic knowledge rows carrying the
//
//	:correction: tag (user correction frequency — strpos
//	':correction:', excluding the candidate itself).
//
// ttlSatDays — the TTL saturation days: the stale_ttl_days threshold
//
//	(T5 wiring — meta-overrideable, default 180). At/above it the
//	TTL component is fully lit; a non-positive value falls back to
//	the default (the unset-keeps-0 parse convention defense).
//
// 3          — correction saturation: 3+ same-topic corrections fully lit.
//
//	A code constant, part of the formula — not a tuning key.
const (
	staleScoreWeightTTL  = 0.7
	staleScoreWeightCorr = 0.3
	staleCorrSat         = 3.0
)

// StaleScore — the pure weighted-sum formula, 0..100, monotone in each
// signal, saturated at the caps above (the scoring single definition).
func StaleScore(ttlDays, corrCount, ttlSatDays int) int {
	if ttlSatDays <= 0 {
		ttlSatDays = store.DefaultThresholds().StaleTTLDays // unset-0 parse convention defense
	}
	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	v := 100 * (staleScoreWeightTTL*clamp01(float64(ttlDays)/float64(ttlSatDays)) +
		staleScoreWeightCorr*clamp01(float64(corrCount)/staleCorrSat))
	return int(v + 0.5)
}

// staleScoredItem — one scored stale candidate (the --age-audit listing
// item material, T4). Score carries only the 2 weighted signals; bad_recall
// (same-topic knowledge invalidated/superseded after recall — the report.go
// bad-recall derivation) and supersede (same-topic superseded_by lineage)
// are REFERENCE columns — report information only, no weight.
type staleScoredItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	Score     int    `json:"score"`
	TTLDays   int    `json:"ttl_days"`
	CorrCount int    `json:"correction_count"`
	BadRecall int    `json:"bad_recall"`
	Supersede int    `json:"supersede_count"`
}

// listStaleScored — the stale-candidate population with the 2-signal score
// (read-only). Exclusions are server-side: :vol:immutable: rows (strpos —
// NULL tags safe via the IS NULL arm) never enter the population (the
// volatility tier contract), and only active·non-invalidated rows do. The
// population cap is LIMIT $1 (the decay-audit convention — the SQL pre-order
// COALESCE(re.last_at, k.ingested_at) ASC is the monotone proxy of the
// dominant TTL signal; the final order is the Go-side score DESC stable
// sort). Disposition is NOT performed here — the report (T4) delegates to
// the existing --invalidate approval system.
func listStaleScored(ctx context.Context, ms wire.Store, limit int) ([]staleScoredItem, error) {
	if limit <= 0 {
		limit = 20
	}
	// The TTL saturation prefers the stale_ttl_days meta override (T5
	// wiring — the decay-audit threshold convention: unset·load failure·
	// non-positive all fall back to the DefaultThresholds basis).
	ttlSat := store.DefaultThresholds().StaleTTLDays
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.StaleTTLDays > 0 {
		ttlSat = th.StaleTTLDays
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, k.topic, COALESCE(re.last_at, k.ingested_at),
		       (SELECT count(*) FROM knowledge c
		         WHERE c.topic = k.topic AND c.id <> k.id
		           AND strpos(c.tags, ':correction:') > 0),
		       (SELECT count(*) FROM knowledge b
		         WHERE b.topic = k.topic AND b.id <> k.id
		           AND (b.invalid_at IS NOT NULL OR b.superseded_by IS NOT NULL)
		           AND EXISTS (SELECT 1 FROM recall_events rb WHERE rb.knowledge_id = b.id)),
		       (SELECT count(*) FROM knowledge s
		         WHERE s.topic = k.topic AND s.id <> k.id AND s.superseded_by IS NOT NULL)
		FROM knowledge k
		LEFT JOIN (`+knowledge.RecallAggregateSQL+`) re ON re.knowledge_id = k.id
		WHERE k.status = 'active' AND k.invalid_at IS NULL
		  AND (k.tags IS NULL OR strpos(k.tags, ':vol:immutable:') = 0)
		ORDER BY COALESCE(re.last_at, k.ingested_at) ASC
		LIMIT $1`, []any{limit})
	if err != nil {
		return nil, err
	}
	now := decayNow()
	items := []staleScoredItem{}
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		ref, ok := AnyToTime(r[2])
		if !ok || ref.IsZero() {
			continue
		}
		corr, _ := render.CellToInt(r[3])
		bad, _ := render.CellToInt(r[4])
		sup, _ := render.CellToInt(r[5])
		items = append(items, staleScoredItem{
			ID:        id,
			Topic:     render.Text(topic), // via render.Text (the output re-sanitization convention)
			TTLDays:   int(now.Sub(ref).Hours() / 24),
			CorrCount: corr,
			BadRecall: bad,
			Supersede: sup,
		})
	}
	for i := range items {
		items[i].Score = StaleScore(items[i].TTLDays, items[i].CorrCount, ttlSat)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	return items, nil
}

// ageAuditTopicMax — the rune cap of the report topic (the hotHeadline
// truncation convention — … suffix when over; the full topic stays
// addressable via --invalidate --id N).
const ageAuditTopicMax = 40

// ageAuditItem — one --age-audit listing item (the decay-audit formatting
// inherited): the scored signals plus the 2 non-weighted reference columns,
// with the threshold marking. Disposition is NOT performed — the summary
// delegates to the existing --invalidate approval system.
type ageAuditItem struct {
	ID            int    `json:"id"`
	Topic         string `json:"topic"`
	Score         int    `json:"score"`
	TTLDays       int    `json:"ttl_days"`
	CorrCount     int    `json:"correction_count"`
	BadRecall     int    `json:"bad_recall"`
	SupersedeCnt  int    `json:"supersede_count"`
	OverThreshold bool   `json:"over_threshold"`
}

// clampAgeTopic — truncates a report topic to the rune cap (… suffix when over).
func clampAgeTopic(s string) string {
	r := []rune(s)
	if len(r) <= ageAuditTopicMax {
		return s
	}
	return string(r[:ageAuditTopicMax]) + "…"
}

// maintainAgeAudit — the v1.13.0 stale-score report (read-only, dry-run by
// default — no execution flag exists; disposition goes through the existing
// --invalidate approval system). The listing is listStaleScored (T3: the 2
// weighted signals, the :vol:immutable: server-side exclusion, and the
// score DESC order); this report adds the threshold marking and the topic
// clamp (--limit inherited, default 20 — the decay-audit convention).
func maintainAgeAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {
	// The marking line prefers the stale_score_threshold meta override (T5
	// wiring — the decay-audit threshold convention: unset·load failure·
	// non-positive all fall back to the default 70; the at-or-above
	// convention: score >= threshold is marked).
	threshold := store.DefaultThresholds().StaleScoreThreshold
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.StaleScoreThreshold > 0 {
		threshold = th.StaleScoreThreshold
	}
	items, err := listStaleScored(ctx, ms, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out := make([]ageAuditItem, 0, len(items))
	over := 0
	for _, it := range items {
		marked := it.Score >= threshold
		if marked {
			over++
		}
		out = append(out, ageAuditItem{
			ID:            it.ID,
			Topic:         clampAgeTopic(it.Topic), // already render.Text-sanitized in listStaleScored
			Score:         it.Score,
			TTLDays:       it.TTLDays,
			CorrCount:     it.CorrCount,
			BadRecall:     it.BadRecall,
			SupersedeCnt:  it.Supersede,
			OverThreshold: marked,
		})
	}
	b, mErr := json.Marshal(struct {
		Status    string         `json:"status"`
		Threshold int            `json:"threshold"`
		Count     int            `json:"count"`
		Items     []ageAuditItem `json:"items"`
		Summary   string         `json:"summary"`
	}{Status: "age-audit", Threshold: threshold, Count: len(out), Items: out,
		Summary: fmt.Sprintf("%d stale candidates (%d over threshold %d) — dispose via maintain --invalidate (approval required)",
			len(out), over, threshold)})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// AnyToTime — a cell value as time (accepting time.Time and RFC3339 strings).
func AnyToTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if t == "" {
			return time.Time{}, false
		}
		ts, err := time.Parse(time.RFC3339, t)
		return ts, err == nil
	default:
		return time.Time{}, false
	}
}

// maintainCheck — the Counts vs Thresholds comparison report. Thresholds are read from meta
// (global constraint: no hardcoding), and the boundary starts at the excess (pending==50 is ok; 51+ alarms).
func maintainCheck(ctx context.Context, ms MaintStore, stdout io.Writer) int {
	counts, err := ms.Counts(ctx)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	th, err := wire.LoadThresholds(ctx, ms)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	line := `{"status":"ok"}`
	if counts.Pending > th.PendingMax || counts.DupSuspect > th.DupSuspMax {
		line = fmt.Sprintf(`{"status":"review-needed","pending":%d,"dup_suspect":%d}`,
			counts.Pending, counts.DupSuspect)
	}
	// stall warnings — last_hot_at and last_write_at elapsed > grace (a warnings array).
	if warns := stallWarnings(ctx, ms, th); len(warns) > 0 {
		b, mErr := json.Marshal(warns)
		if mErr == nil {
			line = line[:len(line)-1] + `,"warnings":` + string(b) + `}`
		}
	}
	// the last restore drill (restore --drill) elapsed report — the field is added only when exceeded.
	if extra := drillReport(ctx, ms); extra != "" {
		line = line[:len(line)-1] + extra + "}"
	}
	// the next key — the recommended action command per state (the pending import
	// dry-run convention). Being additive, the existing status and numeric fields are unchanged.
	line = line[:len(line)-1] + `,"next":` + strconv.Quote(maintainCheckNext(counts, th)) + `}`
	fmt.Fprintln(stdout, line)
	return 0
}

// maintainCheckNext — the --check next key value: concatenates the recommended commands
// per exceeded item (none within the threshold). The commands are only paths that actually
// exist (pending review, expire-pending expiry, dup-suspects listing).
func maintainCheckNext(counts db.Counts, th store.Thresholds) string {
	var cmds []string
	if counts.Pending > th.PendingMax {
		cmds = append(cmds, "flint pending", "flint maintain --expire-pending N")
	}
	if counts.DupSuspect > th.DupSuspMax {
		cmds = append(cmds, "flint maintain --dup-suspects")
	}
	if len(cmds) == 0 {
		return "none — counts within thresholds"
	}
	return strings.Join(cmds, " · ")
}

// stallWarnings — the last-seen heartbeat elapsed diagnostics (spec §1: the nodata pattern).
// A warning string when last_hot_at elapsed > stall_hot_h (default 48h) or last_write_at
// elapsed > stall_write_h (default 72h). warn keeps exit 0 (following brew doctor).
func stallWarnings(ctx context.Context, st wire.Store, th store.Thresholds) []string {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_hot_at','last_write_at')`, nil)
	if err != nil {
		return nil // read failures are skipped so they do not harm the --check result
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	var warns []string
	now := time.Now()
	if ts, ok := AnyToTime(kv["last_hot_at"]); ok {
		if el := now.Sub(ts); el > time.Duration(th.StallHotH)*time.Hour {
			warns = append(warns, fmt.Sprintf(
				"hot stall: %dh since last_hot_at (grace %dh)", int(el.Hours()), th.StallHotH))
		}
	}
	if ts, ok := AnyToTime(kv["last_write_at"]); ok {
		if el := now.Sub(ts); el > time.Duration(th.StallWriteH)*time.Hour {
			warns = append(warns, fmt.Sprintf(
				"write stall: %dh since last_write_at (grace %dh)", int(el.Hours()), th.StallWriteH))
		}
	}
	return warns
}

// drillThresholds — the drill elapsed threshold days (via the registry, default on failure).
func drillThresholds(ctx context.Context, st wire.Store) int {
	if th, err := wire.LoadThresholds(ctx, st); err == nil {
		return th.DrillMaxDays
	}
	return 30
}

// drillReport — the last_drill elapsed report. Over 30 days (the meta drill_max_days) it
// returns `,"drill":"stale"`; with no valid record at all, `,"drill":"never"`.
// Within it, the empty string (no report). Read failures are skipped so they do not harm --check.
func drillReport(ctx context.Context, st wire.Store) string {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_drill')`, nil)
	if err != nil {
		return ""
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	maxDays := drillThresholds(ctx, st) // the single source (last_drill below stays in kv)
	last, err := time.Parse(time.RFC3339, kv["last_drill"])
	if err != nil {
		return `,"drill":"never"`
	}
	if time.Since(last) > time.Duration(maxDays)*24*time.Hour {
		return `,"drill":"stale"`
	}
	return ""
}

// adminErrLine — the update-path error output. ErrNotFound is 1; the rest MapExit.
func adminErrLine(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrNotFound) {
		return 1
	}
	return store.MapExit(err)
}

// ── knowledge hygiene and history management ──

// metaKnownKeys — the known meta keys (namespaces and fixed keys). The classification
// basis of the hygiene audit — keys not here are reported as unknown.
var metaKnownKeys = map[string]bool{
	"schema_version": true, "views_hash": true, "views_sig": true,
	"exposure_profile": true, "last_backup": true, "last_drill": true,
	"last_hot_at": true, "last_read_at": true, "last_write_at": true,
	"read_stats": true, "read_fallback_calls": true, "hot_stats": true,
	"session_knowledge_last": true, "recall_starved_count": true,
	"last_brief_at": true, // instant (spec §1.3 — resolving the brief time-key unknown false positive)
}

// metaKnownPrefixes — the known prefixes (daily: date counters; pb_: playbook
// usage counters pb_<id>_uses/_ok — preventing false unknowns in the active
// statistics. fired_: the fired-counter cumulative keys (the 3 fired_nudge_{distill,recall,
// update} kinds, the 1 fired_done_stale kind, and fired_doctor_<check> — the check
// registry's 16 + harness 14 + skill-file 1 = 31 kind cap). Preventing doctor from warning
// about its own counters as unknown keys (a self-reference false positive). The threshold override
// keys are handled by thresholdsKeys — a dynamic lookup).
var metaKnownPrefixes = []string{"daily:", "pb_", "fired_"}

// KnownMetaKey — the single classification source (prefixes included — preventing hardcoded splits).
func KnownMetaKey(k string) bool {
	if metaKnownKeys[k] {
		return true
	}
	for _, p := range metaKnownPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// dailyKeyDate — extracts the date segment from a daily counter key.
// Supports both daily:<date> (2 segments) and daily:<kind>:<date> (3 segments) —
// the last segment is what is judged. An invalid date (not YYYY-MM-DD) is false (the
// existing convention: the key is excluded from age judgment but included in the daily
// count). The single implementation shared by maintain oldest-daily and doctor staleDaily.
func dailyKeyDate(k string) (string, bool) {
	segs := strings.Split(strings.TrimPrefix(k, "daily:"), ":")
	d := segs[len(segs)-1]
	if len(d) != 10 { // strict YYYY-MM-DD length (excluding loose forms like 2026-1-1)
		return "", false
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", false
	}
	return d, true
}

// maintainMetaAudit — the meta key hygiene report (read-only, always exit 0).
// Classification: known (fixed), threshold (threshold keys), daily (age-marked), unknown (review recommended).
func maintainMetaAudit(ctx context.Context, ms MaintStore, stdout io.Writer) int {
	rows, err := wire.QueryOnce(ctx, ms, `SELECT key, value FROM meta ORDER BY key`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	thKeys := map[string]bool{}
	for _, k := range wire.ThresholdsKeys() {
		thKeys[k] = true
	}
	tunKeys := map[string]bool{}
	for _, mk := range TuningMetaKeys() {
		tunKeys[mk.Key] = true
	}
	now := time.Now().UTC()
	known, thresholdN, tuningN, dailyN, unknown := 0, 0, 0, 0, 0
	var oldestDaily, oldestDate string
	var unknownList []string
	for _, r := range rows {
		if len(r) < 1 {
			continue
		}
		k := fmt.Sprint(r[0])
		switch {
		case strings.HasPrefix(k, "daily:"):
			dailyN++
			// flat keys (daily:<kind>:<date>) supported — compared by the date
			// segment (comparing the raw key would depend on kind sorting).
			if d, ok := dailyKeyDate(k); ok && d < now.Format("2006-01-02") {
				if oldestDaily == "" || d < oldestDate {
					oldestDaily, oldestDate = k, d
				}
			}
		case KnownMetaKey(k): // the non-daily path — including pb_ and similar prefixes
			known++
		case thKeys[k]:
			thresholdN++
		case tunKeys[k]:
			tuningN++
		default:
			unknown++
			unknownList = append(unknownList, k)
		}
	}
	// daily age — versus the retention window (daily_retention_days). Parsed directly from
	// the already-fetched rows (removing a double meta scan).
	retention := 0
	for _, r := range rows {
		if len(r) >= 2 && fmt.Sprint(r[0]) == "daily_retention_days" {
			if n, ok := render.CellToInt(r[1]); ok {
				retention = n
			}
		}
	}
	if retention <= 0 {
		retention = 14
	}
	fmt.Fprintf(stdout, `{"status":"meta-audit","total":%d,"known":%d,"thresholds":%d,"tuning":%d,"daily":%d,"retention_days":%d,"oldest_daily":%q,"unknown":%d,"unknown_keys":[`,
		len(rows), known, thresholdN, tuningN, dailyN, retention, oldestDaily, unknown)
	for i, k := range unknownList {
		if i > 0 {
			fmt.Fprint(stdout, ",")
		}
		fmt.Fprint(stdout, QuoteJSONFn(k))
	}
	fmt.Fprintln(stdout, `]}`)
	if unknown > 0 {
		fmt.Fprintln(stdout, `{"hint":"unknown keys are deprecation candidates — verify then clean up manually: DELETE FROM meta WHERE key = '...'"}`)
	}
	return 0
}

// maintainPruneInvalidated — cleans invalidated knowledge past N days, plus the recall-event
// retention (recall_event_retention_days, default 90) expiry cleanup.
// Dry-run by default (target list and counts); --commit confirms. The DELETE cleans only the
// current state — the row contents remain in dolt history (keeping the versioned-memory and
// audit principles; unrelated to ADR-0012). The event cleanup (R42-D) runs regardless of
// whether invalidated-knowledge targets exist — it is the structural growth cap of the
// append-only source (recall_events). The audit commit staging (auditStageTables) absorbs
// recall_events, and the knowledge_reads orphan portion is quietly reclaimed by the metering
// commit (TelemetryCommitter — implemented Stores only) (§13).
func maintainPruneInvalidated(ctx context.Context, ms MaintStore, stdout io.Writer, days int, commit bool) int {
	// a single threshold load — invalidated_prune_days (the knowledge expiry days; the tuning
	// override, following the --stale stale_days convention) and recall_event_retention_days
	// (the recall-event retention) share the fallback source. Also a parse-reach defense.
	th := knowledge.WriteThresholds(ctx, ms)
	if days <= 0 {
		days = th.InvalidatedPruneDays
		if days <= 0 {
			days = 90
		}
	}
	// the recall-event retention — registry-key fallback (unset 0; corrupt falls back to the default 90).
	retention := th.RecallEventRetentionD
	if retention <= 0 {
		retention = 90
	}
	eventCutoff := time.Now().UTC().AddDate(0, 0, -retention)
	cutoff := time.Now().UTC().AddDate(0, 0, -days) // a timestamptz parameter
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE status = 'invalidated' AND invalid_at < $1 ORDER BY invalid_at`,
		[]any{cutoff})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if n, ok := render.CellToInt(r[0]); ok {
			ids = append(ids, n)
		}
	}
	// the expired recall-event count — the common source for the dry-run report and the
	// commit execution (a fake's 0-row response counts as 0).
	expired := 0
	erows, err := wire.QueryOnce(ctx, ms,
		`SELECT count(*)::int FROM recall_events WHERE recalled_at < $1`,
		[]any{eventCutoff})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(erows) > 0 && len(erows[0]) > 0 {
		if n, ok := render.CellToInt(erows[0][0]); ok {
			expired = n
		}
	}
	if len(ids) == 0 && expired == 0 {
		fmt.Fprintln(stdout, `{"status":"prune-invalidated","deleted":0,"detail":"no targets"}`)
		return 0
	}
	if !commit {
		idStrs := make([]string, 0, len(ids))
		for _, id := range ids {
			idStrs = append(idStrs, strconv.Itoa(id))
		}
		list := strings.Join(idStrs, ",")
		if len(list) > 200 {
			list = list[:200] + "…"
		}
		fmt.Fprintf(stdout, `{"status":"dry-run","would_delete":%d,"older_than_days":%d,"ids":"%s","would_prune_recall_events":%d,"recall_retention_days":%d,"next":"run with --commit to apply"}`+"\n",
			len(ids), days, list, expired, retention)
		return 0
	}
	deleted := 0
	if len(ids) > 0 {
		if err := retryWrite(ctx, func() error {
			// the DELETE stays on the single db.Repo.PruneInvalidated path
			// (the return = the actual affected row count — not a snapshot).
			n, err := ms.PruneInvalidated(ctx, cutoff)
			if err != nil {
				return err
			}
			deleted = int(n)
			_, err = ms.Commit(ctx, fmt.Sprintf(
				"prune-invalidated: removed %d rows (%dd+ elapsed) (knowledge hygiene path) [session:0]", deleted, days))
			return err
		}); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		// curation counter — the actually deleted rows (the Repo return value). Not counted in dry-run.
		knowledge.BumpCurationCounter(ctx, ms, "maintain_prune_rows", deleted)
	}
	// the recall-event retention cleanup — the DELETE is the single db.Repo.PruneRecallEvents
	// path. The audit commit staging absorbs recall_events.
	prunedEvents := 0
	if err := retryWrite(ctx, func() error {
		n, err := ms.PruneRecallEvents(ctx, eventCutoff)
		if err != nil {
			return err
		}
		prunedEvents = int(n)
		if prunedEvents == 0 {
			return nil // 0 staged rows — preventing an empty audit commit
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"prune-recall-events: removed %d rows (%dd retention) (knowledge hygiene path) [session:0]", prunedEvents, retention))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// reclaiming the orphan knowledge_reads portion — the metering commit (unimplemented Stores
	// silently skip, §13; thanks to dolt_add pre-staging, any failure is reclaimed by the next metering commit).
	if tc, ok := ms.(knowledge.TelemetryCommitter); ok {
		_ = tc.CommitTelemetry(ctx, "prune-invalidated")
	}
	fmt.Fprintf(stdout, `{"status":"prune-invalidated","deleted":%d,"older_than_days":%d,"pruned_recall_events":%d,"recall_retention_days":%d}`+"\n",
		deleted, days, prunedEvents, retention)
	return 0
}
