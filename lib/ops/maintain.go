package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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

type MaintStore interface {
	wire.Store
	Counts(ctx context.Context) (db.Counts, error)
	DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error)
	Invalidate(ctx context.Context, ids []int, reason string) (int64, error)
	Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)

	PruneInvalidated(ctx context.Context, before time.Time) (int64, error)

	PruneRecallEvents(ctx context.Context, before time.Time) (int64, error)
	Merge(ctx context.Context, fromID, toID int) error
}

type ConflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

type ReaffirmStore interface {
	Reaffirm(ctx context.Context, ids []int) (int64, error)
}

type RecheckConditionsStore interface {
	SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error)
}

var maintainValueFlags = []string{"--days", "--limit", "--id", "--from", "--to", "--reason",
	"--ids", "--supersede", "--by", "--conditions"}

var maintainModeFlags = map[string]map[string]bool{
	"--check":      {},
	"--meta-audit": {},

	"--prune-invalidated": {},
	"--dup-suspects":      {"--limit": true},
	"--conflict-suspects": {"--limit": true},
	"--stale":             {"--days": true, "--limit": true},
	"--invalidate-stale":  {"--days": true, "--limit": true},
	"--invalidate":        {"--id": true, "--reason": true, "--ids": true},
	"--merge":             {"--from": true, "--to": true},
	"--supersede":         {"--supersede": true, "--by": true},

	"--expire-pending": {},

	"--decay-audit": {"--limit": true},

	"--age-audit": {"--limit": true},

	"--merge-audit": {"--limit": true},

	"--recall-audit": {"--limit": true},

	"--reaffirm": {"--id": true, "--ids": true},

	"--recheck": {"--conditions": true},

	"--gc": {},
}

var maintainYesModes = map[string]bool{"--invalidate": true, "--invalidate-stale": true, "--reaffirm": true, "--gc": true}

var maintainCommitModes = map[string]bool{"--expire-pending": true, "--prune-invalidated": true}

var MaintainRetryDelays = db.DefaultDelays

var MaintainRetryDelaysFn = func() []time.Duration { return MaintainRetryDelays }

func retryWrite(ctx context.Context, fn func() error) error {
	return db.Retry(ctx, fn, MaintainRetryDelaysFn())
}

const maintainUsageLine = "usage: flint maintain --check | --dup-suspects [--limit M] | --conflict-suspects [--limit M] | --stale [--days N] [--limit M] | --invalidate-stale [--yes] [--days N] [--limit M] | --invalidate (--id N --reason S | --ids 1,2,3 --yes) | --merge --from N --to M | --supersede OLD --by NEW | --expire-pending N [--commit] | --meta-audit | --prune-invalidated [N] [--commit] | --decay-audit [--limit M] | --age-audit [--limit M] | --merge-audit [--limit M] | --recall-audit [--limit M] | --reaffirm (--id N | --ids 1,2) --yes | --recheck N [--conditions S --yes] | --gc [--shallow] --yes | --tui (opt-in, run alone)"

func maintainUsageErrf(format string, a ...any) *wire.UsageError {
	return wire.UsageErrorf(format+" — "+maintainUsageLine, a...)
}

func RunMaintain(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	ms, ok := st.(MaintStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--check"}, {Name: "--dup-suspects"}, {Name: "--conflict-suspects"},
			{Name: "--merge"}, {Name: "--stale"}, {Name: "--invalidate"},
			{Name: "--invalidate-stale"}, {Name: "--meta-audit"}, {Name: "--decay-audit"},
			{Name: "--age-audit"}, {Name: "--reaffirm"}, {Name: "--merge-audit"},
			{Name: "--recall-audit"},

			{Name: "--expire-pending", Value: true}, {Name: "--prune-invalidated", Value: true},
			{Name: "--recheck", Value: true},
			{Name: "--days", Value: true}, {Name: "--limit", Value: true},
			{Name: "--id", Value: true}, {Name: "--from", Value: true}, {Name: "--to", Value: true},
			{Name: "--reason", Value: true}, {Name: "--supersede", Value: true}, {Name: "--by", Value: true},
			{Name: "--ids", Value: true}, {Name: "--conditions", Value: true},
			{Name: "--gc"}, {Name: "--shallow"},
			{Name: "--commit"}, {Name: "--yes"},
		},
		Usage: maintainUsageLine,
	})
	if perr != nil {

		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	mode := ""
	id, from, to := 0, 0, 0
	old, by := 0, 0
	days, limit := 0, 20
	dupLimit := SuspectsDefaultLimit
	expireDays := 0
	recheckID := 0
	conditionsVal := ""
	conditionsGiven := false
	reason := ""
	yes, commit := res.Given("--yes"), res.Given("--commit")
	var idList []int

	for _, name := range res.FlagOrder() {
		switch name {
		case "--check", "--dup-suspects", "--conflict-suspects", "--merge", "--stale",
			"--invalidate", "--invalidate-stale", "--meta-audit", "--expire-pending",
			"--prune-invalidated", "--supersede", "--decay-audit", "--age-audit",
			"--merge-audit", "--recall-audit", "--reaffirm", "--recheck", "--gc":
			mode = name
		}
	}

	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--days", "--limit":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number", o.Name)))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {

				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 3
			}
			if o.Name == "--days" && n > db.MaxRetentionDays {

				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
					"maintain: --days %s over the %d-day ceiling", o.Value, db.MaxRetentionDays)))
				return 3
			}
			if o.Name == "--limit" && n > math.MaxInt32 {

				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
					"maintain: --limit %d over the int4 ceiling %d", n, math.MaxInt32)))
				return 3
			}
			if o.Name == "--days" {
				days = n
			} else {
				limit, dupLimit = n, n
			}
		case "--expire-pending", "--prune-invalidated":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a number of days", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			if n > db.MaxRetentionDays {

				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
					"maintain: %s %s over the %d-day ceiling", o.Name, o.Value, db.MaxRetentionDays)))
				return 1
			}
			expireDays = n
		case "--recheck":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s requires a knowledge id", o.Name)))
				return 1
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: invalid number %q", o.Value)))
				return 1
			}
			if n > maxKnowledgeID {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
					"maintain: --recheck %d over the int4 ceiling %d", n, maxKnowledgeID)))
				return 1
			}
			recheckID = n
		case "--conditions":

			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --conditions requires a value")))
				return 1
			}

			if err := knowledge.ValidateFlagValue("conditions", o.Value); err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return 3
			}

			if serr := knowledge.ValidateConditionsString(o.Value); serr != nil {
				fmt.Fprintln(stdout, wire.ErrLine(serr))
				return 3
			}
			conditionsVal, conditionsGiven = o.Value, true
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
			if n > maxKnowledgeID {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
					"maintain: %s %d over the int4 ceiling %d", o.Name, n, maxKnowledgeID)))
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

	if allowed, ok := maintainModeFlags[mode]; ok {
		for _, f := range maintainValueFlags {
			if seen[f] && !allowed[f] {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: %s is not valid with %s", f, mode)))
				return 1
			}
		}
	}

	if yes && !maintainYesModes[mode] && !(mode == "--recheck" && conditionsGiven) {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --yes is not valid with %s", mode)))
		return 1
	}

	if commit && !maintainCommitModes[mode] {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --commit is not valid with %s", mode)))
		return 1
	}

	if res.Given("--shallow") && mode != "--gc" {
		if mode == "" {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
				"maintain: --shallow is not valid without a mode (it is --gc-scoped)")))
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --shallow is not valid with %s", mode)))
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
	case "--merge-audit":
		return maintainMergeAudit(ctx, ms, stdout, limit)
	case "--recall-audit":
		return maintainRecallAudit(ctx, ms, stdout, limit)
	case "--reaffirm":
		return maintainReaffirm(ctx, ms, stdout, id, idList, yes)
	case "--recheck":
		if recheckID == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --recheck requires a knowledge id"))))
			return 1
		}
		if conditionsGiven {
			return maintainRecheckConditions(ctx, ms, stdout, recheckID, conditionsVal, yes)
		}
		return maintainRecheck(ctx, ms, stdout, recheckID)
	case "--gc":
		return maintainGC(ctx, ms, stdout, res.Given("--shallow"), yes)
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
			if !yes {
				fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate --ids requires --yes"))))
				return 1
			}
			return maintainInvalidateIDs(ctx, ms, stdout, idList)
		}
		if id == 0 || reason == "" {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate requires --id and --reason"))))
			return 1
		}

		chk, err := wire.QueryOnce(ctx, ms,
			`SELECT invalid_at FROM knowledge WHERE id = $1`, []any{id})
		if err != nil {
			return adminErrLine(stdout, err)
		}
		if len(chk) == 0 {
			return adminErrLine(stdout, fmt.Errorf("%w: invalidate: id=%d not found", db.ErrNotFound, id))
		}
		if chk[0][0] == nil {

			recallAuditDisposed(ctx, ms, []int{id})
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

		return maintainExpirePending(ctx, ms, stdout, expireDays, commit)
	case "--merge":
		if from == 0 || to == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --merge requires --from and --to"))))
			return 1
		}

		recallAuditDisposed(ctx, ms, []int{from})
		if err := ms.Merge(ctx, from, to); err != nil {
			return adminErrLine(stdout, err)
		}
		fmt.Fprintf(stdout, `{"status":"merged","from":%d,"to":%d}`+"\n", from, to)
		return 0
	default:

		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", maintainUsageLine)))
		return 3
	}
}

const maxKnowledgeID = math.MaxInt32

func parseIDList(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("maintain: --ids invalid id %q", p)
		}
		if n > maxKnowledgeID {
			return nil, fmt.Errorf("maintain: --ids id %d over the int4 ceiling %d", n, maxKnowledgeID)
		}
		if seen[n] {
			return nil, fmt.Errorf("maintain: --ids duplicate id %d", n)
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

type staleItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	NeverRead bool   `json:"never_read"`
	Reason    string `json:"reason"`
	StaleDays int    `json:"stale_days"`
}

func maintainDupSuspects(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	groups, err := ms.DupGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, DupSuspectsJSON(groups))
	return 0
}

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
		out.Groups = append(out.Groups, dupGroupJSON{Topic: render.Text(g.Topic), IDs: ids})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"dup-suspects","groups":[]}`
	}
	return string(b)
}

var ConflictDupThreshold = store.DefaultThresholds().ConflictDupThreshold

const SuspectsDefaultLimit = 10

const MaintainTUILoadLimit = 500

func ConflictDice(a, b string) float64 {
	return query.BigramDiceMultiset(a, b)
}

func ClassifyConflict(summA, summB string) bool {
	return classifyConflictAt(summA, summB, ConflictDupThreshold)
}

func classifyConflictAt(summA, summB string, dupThreshold float64) bool {
	return ConflictDice(summA, summB) < dupThreshold
}

func ConflictSummaryPrefix(s string) string {
	return render.TruncateRunes(s, 40)
}

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

func maintainConflictSuspects(ctx context.Context, cs ConflictStore, stdout io.Writer, limit int) int {
	groups, err := cs.ConflictGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

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
			sums = append(sums, ConflictSummaryPrefix(render.Text(s)))
		}
		out.Groups = append(out.Groups, conflictGroupJSON{Topic: render.Text(g.Topic), IDs: ids, Summaries: sums})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"conflict-suspects","groups":[]}`
	}
	return string(b)
}

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

func listStale(ctx context.Context, ms MaintStore, daysOverride, limit int) ([]staleItem, int, error) {
	th, err := wire.LoadThresholds(ctx, ms)
	if err != nil {
		return nil, 0, err
	}
	days := th.StaleDays
	if daysOverride > 0 {
		days = daysOverride
	}

	cutoff := db.DaysCutoff(days, time.Now())
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
		ref, ok := AnyToTime(r[2])
		if !ok || ref.IsZero() {
			ref, ok = AnyToTime(r[3])
		}
		if !ok || !ref.Before(cutoff) {
			continue
		}
		reason := "stale"
		if readCount == 0 {
			reason = "never_read"
		}
		items = append(items, staleItem{
			ID:        id,
			Topic:     render.Text(topic),
			NeverRead: readCount == 0,
			Reason:    reason,
			StaleDays: int(time.Since(ref).Hours() / 24),
		})
	}
	return items, days, nil
}

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

func maintainInvalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int) int {
	return invalidateIDs(ctx, ms, stdout, ids, "invalidate --ids")
}

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

	recallAuditDisposed(ctx, ms, ids)
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

type expireItem struct {
	ID      int    `json:"id"`
	Topic   string `json:"topic"`
	Summary string `json:"summary"`
	AgeDays int    `json:"age_days"`
}

const expireListLimit = 20

func listExpiredPending(ctx context.Context, ms MaintStore, days int) ([]expireItem, time.Time, error) {

	cutoff := db.DaysCutoff(days, time.Now())

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
		if !ok || !va.Before(cutoff) {
			continue
		}
		items = append(items, expireItem{
			ID:      id,
			Topic:   render.Text(topic),
			Summary: ConflictSummaryPrefix(render.Text(summary)),
			AgeDays: int(time.Since(va).Hours() / 24),
		})
	}
	return items, cutoff, nil
}

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
	if oldStatus != "active" {
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

	cycRows, err := wire.QueryOnce(ctx, ms, db.SupersedeCycleSQL,
		[]any{oldID, newID, db.SupersedeCycleDepthLimit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(cycRows) > 0 {
		if cyc, _ := cycRows[0][0].(bool); cyc {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
				"maintain: --supersede: id=%d -> id=%d would create a supersede cycle", oldID, newID)))
			return 1
		}
		if truncated, _ := cycRows[0][1].(bool); truncated {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
				"maintain: --supersede: id=%d -> id=%d lineage walk hit the depth cap (%d) — refusing conservatively",
				oldID, newID, db.SupersedeCycleDepthLimit)))
			return 1
		}
	}

	recallAuditDisposed(ctx, ms, []int{oldID})
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
	knowledge.BumpCurationCounter(ctx, ms, "maintain_supersede", 1)
	fmt.Fprintf(stdout, `{"status":"superseded","old":%d,"superseded_by":%d}`+"\n",
		oldID, newID)
	return 0
}

var decayNow = time.Now

type decayAuditItem struct {
	ID           int    `json:"id"`
	Score        int    `json:"score"`
	LastRecalled string `json:"last_recalled"`
	Effective    int    `json:"effective"`
	Recommend    string `json:"recommend"`
}

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

	var survived map[int]int
	if th.SurvivedDecayRelief > 0 {
		ids := make([]int, 0, len(rows))
		for _, r := range rows {
			if len(r) < 4 {
				continue
			}
			if id, ok := render.CellToInt(r[0]); ok {
				ids = append(ids, id)
			}
		}
		survived, _ = knowledge.SurvivedCounts(ctx, ms, ids)
	}

	knowledge.SnapshotSurvivedSessions(ctx, ms, survived)
	items := []decayAuditItem{}
	for _, r := range rows {

		if len(r) < 4 {
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
		tags, _ := r[3].(string)
		eff := knowledge.EffectiveScoreFor(score, last, now, th.DecayRate, tags,
			survived[id], th.SurvivedDecayRelief)
		if eff >= score {
			continue
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

func maintainMergeAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {
	th := store.DefaultThresholds()
	if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
		th = loaded
	}
	if th.SuggestScanCap <= 0 {
		th.SuggestScanCap = store.DefaultThresholds().SuggestScanCap
	}
	if th.ConflictDupThreshold <= 0 {
		th.ConflictDupThreshold = store.DefaultThresholds().ConflictDupThreshold
	}
	if limit <= 0 {
		limit = 20
	}
	rows, truncated, err := knowledge.FetchMergeAuditRows(ctx, ms, th.SuggestScanCap)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	pairs, cost := knowledge.ClassifyMergeAuditPairs(rows, th.ConflictDupThreshold)
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}

	knowledge.BumpCurationCounter(ctx, ms, "merge_audit_pairs", len(pairs))

	getKV, _ := MetaKV(ctx, ms)
	echoStats, _ := ScanStatsFromMetaFn(getKV)
	out, mErr := json.Marshal(struct {
		Status        string                     `json:"status"`
		Threshold     float64                    `json:"threshold"`
		ScanCap       int                        `json:"scan_cap"`
		Scanned       int                        `json:"scanned"`
		ScanTruncated bool                       `json:"scan_truncated"`
		Cost          knowledge.MergeAuditCost   `json:"cost"`
		EchoP95US     int                        `json:"echo_scan_p95_us"`
		Count         int                        `json:"count"`
		Items         []knowledge.MergeAuditPair `json:"items"`
		Summary       string                     `json:"summary"`
	}{Status: "merge-audit", Threshold: th.ConflictDupThreshold, ScanCap: th.SuggestScanCap,
		Scanned: len(rows), ScanTruncated: truncated, Cost: cost, EchoP95US: echoStats.P95,
		Count: len(pairs), Items: pairs,
		Summary: fmt.Sprintf("%d duplicate pair(s) — dispose via maintain --merge/--supersede (approval required)",
			len(pairs))})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

const recallAuditBadSQL = `(SELECT count(*) FROM knowledge b
			 WHERE b.topic = k.topic AND b.id <> k.id
			   AND (b.invalid_at IS NOT NULL OR b.superseded_by IS NOT NULL)
			   AND EXISTS (SELECT 1 FROM recall_events rb WHERE rb.knowledge_id = b.id))`

type recallAuditItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	BadRecall int    `json:"bad_recall"`
}

func maintainRecallAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {
	minBad := store.DefaultThresholds().RecallAuditMinBad
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.RecallAuditMinBad > 0 {
		minBad = th.RecallAuditMinBad
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, k.topic, `+recallAuditBadSQL+` AS bad_recall
		FROM knowledge k
		WHERE k.status = 'active' AND k.invalid_at IS NULL
		  AND `+recallAuditBadSQL+` >= $1
		ORDER BY bad_recall DESC, k.id ASC
		LIMIT $2`, []any{minBad, limit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	items := []recallAuditItem{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		bad, _ := render.CellToInt(r[2])
		items = append(items, recallAuditItem{
			ID:        id,
			Topic:     clampAgeTopic(render.Text(topic)),
			BadRecall: bad,
		})
	}
	if len(items) > limit {
		items = items[:limit]
	}

	knowledge.BumpCurationCounter(ctx, ms, "recall_audit_listed", len(items))
	out, mErr := json.Marshal(struct {
		Status  string            `json:"status"`
		MinBad  int               `json:"min_bad"`
		Count   int               `json:"count"`
		Items   []recallAuditItem `json:"items"`
		Summary string            `json:"summary"`
	}{Status: "recall-audit", MinBad: minBad, Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d bad-recall row(s) (min %d) — dispose via maintain --invalidate/--supersede/--reaffirm (approval required)",
			len(items), minBad)})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func recallAuditDisposed(ctx context.Context, ms wire.Store, ids []int) {
	if len(ids) == 0 {
		return
	}
	minBad := store.DefaultThresholds().RecallAuditMinBad
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.RecallAuditMinBad > 0 {
		minBad = th.RecallAuditMinBad
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, `+recallAuditBadSQL+` AS bad_recall
		FROM knowledge k
		WHERE k.id = ANY($1) AND k.status = 'active' AND k.invalid_at IS NULL`,
		[]any{ids})
	if err != nil {
		return
	}
	n := 0
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		if bad, ok := render.CellToInt(r[1]); ok && bad >= minBad {
			n++
		}
	}
	knowledge.BumpCurationCounter(ctx, ms, "recall_audit_disposed", n)
}

const (
	staleScoreWeightTTL  = 0.7
	staleScoreWeightCorr = 0.3
	staleCorrSat         = 3.0
)

func StaleScore(ttlDays, corrCount, ttlSatDays int) int {
	if ttlSatDays <= 0 {
		ttlSatDays = store.DefaultThresholds().StaleTTLDays
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

type staleScoredItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	Score     int    `json:"score"`
	TTLDays   int    `json:"ttl_days"`
	CorrCount int    `json:"correction_count"`
	BadRecall int    `json:"bad_recall"`
	Supersede int    `json:"supersede_count"`

	Tags string `json:"-"`
}

func listStaleScored(ctx context.Context, ms wire.Store, limit int) ([]staleScoredItem, error) {
	if limit <= 0 {
		limit = 20
	}

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
		         WHERE s.topic = k.topic AND s.id <> k.id AND s.superseded_by IS NOT NULL),
		       COALESCE(k.tags, '')
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
		if len(r) < 7 {
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
		tags, _ := r[6].(string)
		items = append(items, staleScoredItem{
			ID:        id,
			Topic:     render.Text(topic),
			TTLDays:   int(now.Sub(ref).Hours() / 24),
			CorrCount: corr,
			BadRecall: bad,
			Supersede: sup,
			Tags:      tags,
		})
	}
	for i := range items {
		items[i].Score = StaleScore(items[i].TTLDays, items[i].CorrCount, ttlSat)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	return items, nil
}

const ageAuditTopicMax = 40

type ageAuditItem struct {
	ID            int    `json:"id"`
	Topic         string `json:"topic"`
	Score         int    `json:"score"`
	TTLDays       int    `json:"ttl_days"`
	CorrCount     int    `json:"correction_count"`
	BadRecall     int    `json:"bad_recall"`
	SupersedeCnt  int    `json:"supersede_count"`
	OverThreshold bool   `json:"over_threshold"`

	SuggestedVol string `json:"suggested_vol,omitempty"`
	VolTag       string `json:"vol_tag,omitempty"`
}

func clampAgeTopic(s string) string {
	r := []rune(s)
	if len(r) <= ageAuditTopicMax {
		return s
	}
	return string(r[:ageAuditTopicMax]) + "…"
}

func maintainAgeAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {

	threshold := store.DefaultThresholds().StaleScoreThreshold
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.StaleScoreThreshold > 0 {
		threshold = th.StaleScoreThreshold
	}
	items, err := listStaleScored(ctx, ms, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	srcChanged := vaultSourceChanged(ctx, ms)
	out := make([]ageAuditItem, 0, len(items))
	over := 0
	for _, it := range items {
		marked := it.Score >= threshold
		if marked {
			over++
		}

		sug, existing := "", ExistingVolTag(it.Tags)
		if existing == "" {
			sug = SuggestVolClass(it.Topic, it.Tags)
		}
		out = append(out, ageAuditItem{
			ID:            it.ID,
			Topic:         clampAgeTopic(it.Topic),
			Score:         it.Score,
			TTLDays:       it.TTLDays,
			CorrCount:     it.CorrCount,
			BadRecall:     it.BadRecall,
			SupersedeCnt:  it.Supersede,
			OverThreshold: marked,
			SuggestedVol:  sug,
			VolTag:        existing,
		})
	}
	summary := fmt.Sprintf("%d stale candidates (%d over threshold %d) — dispose via maintain --invalidate (approval required)",
		len(out), over, threshold)
	if srcChanged > 0 {
		summary += fmt.Sprintf(" · %d vault source changed (recheck advised)", srcChanged)
	}
	b, mErr := json.Marshal(struct {
		Status       string         `json:"status"`
		Threshold    int            `json:"threshold"`
		Count        int            `json:"count"`
		SourceChange int            `json:"source_changed"`
		Items        []ageAuditItem `json:"items"`
		Summary      string         `json:"summary"`
	}{Status: "age-audit", Threshold: threshold, Count: len(out), SourceChange: srcChanged,
		Items: out, Summary: summary})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func vaultSourceChanged(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM vault_files WHERE content_changed_at IS NOT NULL`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n, _ := render.CellToInt(rows[0][0])
	return n
}

func maintainReaffirm(ctx context.Context, ms MaintStore, stdout io.Writer, id int, idList []int, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm requires --yes (confirmation)"))))
		return 1
	}
	if len(idList) > 0 && id != 0 {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm takes --id or --ids, not both"))))
		return 1
	}
	ids := idList
	if len(ids) == 0 {
		if id == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm requires --id or --ids"))))
			return 1
		}
		ids = []int{id}
	}
	rs, ok := ms.(ReaffirmStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id, status FROM knowledge WHERE id = ANY($1) AND invalid_at IS NULL`, []any{ids})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	active := map[int]bool{}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		if rid, ok := render.CellToInt(r[0]); ok {
			if status, _ := r[1].(string); status == "active" {
				active[rid] = true
			}
		}
	}
	var missing []int
	for _, i := range ids {
		if !active[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --reaffirm: nonexistent or not active ids %v", missing)))
		return 1
	}

	recallAuditDisposed(ctx, ms, ids)
	if err := retryWrite(ctx, func() error {
		n, err := rs.Reaffirm(ctx, ids)
		if err != nil {
			return err
		}
		if int(n) != len(ids) {
			return fmt.Errorf("reaffirm: updated %d of %d rows (race — retry)", n, len(ids))
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"reaffirm: %d rows valid_at reset (reaffirm path) [session:0]", len(ids)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "reaffirmed", Count: len(ids), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

var recheckVolDict = []string{"vol:immutable", "vol:volatile", "vol:conditional"}

func recheckVolTags(tags string) []string {
	var out []string
	for _, v := range recheckVolDict {
		if strings.Contains(tags, ":"+v+":") {
			out = append(out, v)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

type RecheckSourceClearStore interface {
	ClearVaultSourceChanged(ctx context.Context, path string) (int64, error)
}

func clearRecheckVaultSource(ctx context.Context, ms wire.Store, stdout io.Writer, id int) (int64, int) {
	cs, ok := ms.(RecheckSourceClearStore)
	if !ok {
		return 0, 0
	}
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT source_path FROM knowledge WHERE id = $1`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0, store.MapExit(err)
	}
	sourcePath := ""
	if len(rows) > 0 && len(rows[0]) > 0 {
		sourcePath, _ = rows[0][0].(string)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return 0, 0
	}
	n, err := cs.ClearVaultSourceChanged(ctx, sourcePath)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0, store.MapExit(err)
	}
	if n > 0 {
		if _, cerr := ms.Commit(ctx, fmt.Sprintf(
			"recheck: vault source changed cleared path=%s (%d file(s)) [session:0]",
			render.Text(sourcePath), n)); cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return 0, store.MapExit(cerr)
		}
	}
	return n, 0
}

func maintainRecheck(ctx context.Context, ms wire.Store, stdout io.Writer, id int) int {
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT topic, tags, conditions, valid_at, source_path FROM knowledge WHERE id = $1`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(rows) == 0 {
		return adminErrLine(stdout, fmt.Errorf("%w: recheck: id=%d not found", db.ErrNotFound, id))
	}
	cell := func(i int) any {
		if i < len(rows[0]) {
			return rows[0][i]
		}
		return nil
	}
	topic, _ := cell(0).(string)
	tags, _ := cell(1).(string)
	conditions, _ := cell(2).(string)
	empty := strings.TrimSpace(conditions) == ""
	volTags := recheckVolTags(tags)
	ageDays := 0
	validAt := ""
	if va, ok := AnyToTime(cell(3)); ok && !va.IsZero() {
		validAt = va.UTC().Format(time.RFC3339)
		ageDays = int(time.Since(va).Hours() / 24)
	}
	sourcePath, _ := cell(4).(string)

	cleared, code := clearRecheckVaultSource(ctx, ms, stdout, id)
	if code != 0 {
		return code
	}
	summary := fmt.Sprintf("recheck target id=%d — valid %dd ago", id, ageDays)
	if empty {
		summary += " · no conditions recorded (set via maintain --recheck --conditions)"
	} else {
		summary += " · conditions recorded — re-verify them against the source"
	}
	if cleared > 0 {
		summary += fmt.Sprintf(" · %d vault source change(s) cleared", cleared)
	}
	out, mErr := json.Marshal(struct {
		Status          string   `json:"status"`
		ID              int      `json:"id"`
		Topic           string   `json:"topic"`
		Conditions      string   `json:"conditions"`
		ConditionsEmpty bool     `json:"conditions_empty"`
		VolTags         []string `json:"vol_tags"`
		ValidAt         string   `json:"valid_at"`
		AgeDays         int      `json:"age_days"`
		SourcePath      string   `json:"source_path"`
		SourceCleared   int      `json:"source_cleared"`
		Summary         string   `json:"summary"`
	}{Status: "recheck", ID: id, Topic: clampAgeTopic(render.Text(topic)),
		Conditions: render.Text(conditions), ConditionsEmpty: empty, VolTags: volTags,
		ValidAt: validAt, AgeDays: ageDays, SourcePath: render.Text(sourcePath),
		SourceCleared: int(cleared), Summary: summary})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func maintainRecheckConditions(ctx context.Context, ms MaintStore, stdout io.Writer, id int, conditions string, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --recheck --conditions requires --yes (confirmation)"))))
		return 1
	}
	rs, ok := ms.(RecheckConditionsStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	chk, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(chk) == 0 {
		return adminErrLine(stdout, fmt.Errorf("%w: recheck: id=%d not found or not active", db.ErrNotFound, id))
	}
	if err := retryWrite(ctx, func() error {
		n, err := rs.SetRecheckConditions(ctx, id, conditions)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("recheck conditions: updated %d rows for id=%d (race — retry)", n, id)
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"recheck: conditions set id=%d (recheck conditions path) [session:0]", id))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	cleared, code := clearRecheckVaultSource(ctx, ms, stdout, id)
	if code != 0 {
		return code
	}
	shown := render.Text(conditions)
	out, mErr := json.Marshal(struct {
		Status        string `json:"status"`
		ID            int    `json:"id"`
		Conditions    string `json:"conditions"`
		SourceCleared int    `json:"source_cleared"`
	}{Status: "recheck_conditions", ID: id, Conditions: shown, SourceCleared: int(cleared)})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

const gcPingAttempts = 5

var maintainGCFn = func(ctx context.Context, st wire.Store, shallow bool) error {
	q := "SELECT dolt_gc()"
	if shallow {
		q = "SELECT dolt_gc('--shallow')"
	}
	_, err := st.Query(ctx, q)
	return err
}

var maintainGCPingDelay = time.Second

func maintainGC(ctx context.Context, ms wire.Store, stdout io.Writer, shallow, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --gc requires --yes (confirmation)"))))
		return 1
	}
	mode := "full"
	if shallow {
		mode = "shallow"
	}

	fmt.Fprintln(stdout, `{"status":"gc-pending","notice":"dolt_gc() disconnects all active sessions — flint will reconnect (up to `+
		strconv.Itoa(gcPingAttempts)+` pings)"}`)
	if err := maintainGCFn(ctx, ms, shallow); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: %w", err)))
		return store.MapExit(err)
	}

	reconnected := false
	for i := 0; i < gcPingAttempts; i++ {
		if i > 0 {
			time.Sleep(maintainGCPingDelay)
		}
		if err := ms.Ping(ctx); err == nil {
			reconnected = true
			break
		}
	}
	if !reconnected {
		fmt.Fprintln(stdout, `{"status":"gc-incomplete","notice":"the GC ran but the connection did not return — verify with flint doctor, then rerun maintain --gc --yes"}`)
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --gc: reconnect failed after %d pings", gcPingAttempts)))
		return 1
	}

	rows, err := wire.QueryOnce(ctx, ms, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: schema recheck failed: %w", err)))
		return 1
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if _, convErr := strconv.Atoi(version); convErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --gc: schema recheck failed: schema_version=%q", version)))
		return 1
	}

	stamped := time.Now().UTC().Format(time.RFC3339)
	if err := wire.SetMetaKV(ctx, ms, "last_gc_at", stamped); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: last_gc_at: %w", err)))
		return store.MapExit(err)
	}
	if err := wire.SetMetaKV(ctx, ms, "last_gc_mode", mode); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: last_gc_mode: %w", err)))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Mode   string `json:"mode"`
		LastAt string `json:"last_gc_at"`
	}{Status: "gc", Mode: mode, LastAt: stamped})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

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

	if warns := stallWarnings(ctx, ms, th); len(warns) > 0 {
		b, mErr := json.Marshal(warns)
		if mErr == nil {
			line = line[:len(line)-1] + `,"warnings":` + string(b) + `}`
		}
	}

	if extra := drillReport(ctx, ms); extra != "" {
		line = line[:len(line)-1] + extra + "}"
	}

	line = line[:len(line)-1] + `,"next":` + strconv.Quote(maintainCheckNext(counts, th)) + `}`
	fmt.Fprintln(stdout, line)
	return 0
}

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

func stallWarnings(ctx context.Context, st wire.Store, th store.Thresholds) []string {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_hot_at','last_write_at')`, nil)
	if err != nil {
		return nil
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	var warns []string
	now := time.Now()

	graceHot := db.HoursSpan(th.StallHotH, store.DefaultThresholds().StallHotH)
	graceWrite := db.HoursSpan(th.StallWriteH, store.DefaultThresholds().StallWriteH)
	if ts, ok := AnyToTime(kv["last_hot_at"]); ok {
		if el := now.Sub(ts); el > graceHot {
			warns = append(warns, fmt.Sprintf(
				"hot stall: %dh since last_hot_at (grace %dh)", int(el.Hours()), int(graceHot.Hours())))
		}
	}
	if ts, ok := AnyToTime(kv["last_write_at"]); ok {
		if el := now.Sub(ts); el > graceWrite {
			warns = append(warns, fmt.Sprintf(
				"write stall: %dh since last_write_at (grace %dh)", int(el.Hours()), int(graceWrite.Hours())))
		}
	}
	return warns
}

func drillThresholds(ctx context.Context, st wire.Store) int {
	if th, err := wire.LoadThresholds(ctx, st); err == nil {
		return th.DrillMaxDays
	}
	return 30
}

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
	maxDays := drillThresholds(ctx, st)
	last, err := time.Parse(time.RFC3339, kv["last_drill"])
	if err != nil {
		return `,"drill":"never"`
	}
	if time.Since(last) > db.DaysSpan(maxDays, time.Now()) {
		return `,"drill":"stale"`
	}
	return ""
}

func adminErrLine(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrNotFound) {
		return 1
	}
	return store.MapExit(err)
}

var metaKnownKeys = buildMetaKnownKeys()

func buildMetaKnownKeys() map[string]bool {
	m := map[string]bool{
		"schema_version": true, "views_hash": true, "views_sig": true,
		"exposure_profile": true, "last_backup": true, "last_drill": true,
		"last_hot_at": true, "last_read_at": true, "last_write_at": true,
		"read_stats": true, "read_fallback_calls": true, "hot_stats": true,
		"session_knowledge_last": true, "recall_starved_count": true,
		"last_brief_at":        true,
		"last_events_prune_at": true,
		"vault_path":           true,
		"vault_last_scan":      true,
		"scan_stats":           true,
		"auto_mode":            true,
		"last_shadow_at":       true,
		"last_gc_at":           true,
		"last_gc_mode":         true,
	}
	for _, k := range meteringFreshnessGateKeys {
		m[k] = true
	}
	return m
}

var metaKnownPrefixes = []string{"daily:", "pb_", "fired_", "auto_streak_", "auto_rounds_", "self_version_last:"}

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

func dailyKeyDate(k string) (string, bool) {
	segs := strings.Split(strings.TrimPrefix(k, "daily:"), ":")
	d := segs[len(segs)-1]
	if len(d) != 10 {
		return "", false
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", false
	}
	return d, true
}

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

			if d, ok := dailyKeyDate(k); ok && d < now.Format("2006-01-02") {
				if oldestDaily == "" || d < oldestDate {
					oldestDaily, oldestDate = k, d
				}
			}
		case KnownMetaKey(k):
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

func maintainPruneInvalidated(ctx context.Context, ms MaintStore, stdout io.Writer, days int, commit bool) int {

	th := knowledge.WriteThresholds(ctx, ms)
	if days <= 0 {
		days = th.InvalidatedPruneDays
		if days <= 0 {
			days = 90
		}
	}

	retention := th.RecallEventRetentionD
	if retention <= 0 {
		retention = 90
	}
	eventCutoff := time.Now().UTC().AddDate(0, 0, -retention)
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
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

		knowledge.BumpCurationCounter(ctx, ms, "maintain_prune_rows", deleted)
	}

	prunedEvents := 0
	if err := retryWrite(ctx, func() error {
		n, err := ms.PruneRecallEvents(ctx, eventCutoff)
		if err != nil {
			return err
		}
		prunedEvents = int(n)
		if prunedEvents == 0 {
			return nil
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"prune-recall-events: removed %d rows (%dd retention) (knowledge hygiene path) [session:0]", prunedEvents, retention))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	if tc, ok := ms.(knowledge.TelemetryCommitter); ok {
		_ = tc.CommitTelemetry(ctx, "prune-invalidated")
	}
	fmt.Fprintf(stdout, `{"status":"prune-invalidated","deleted":%d,"older_than_days":%d,"pruned_recall_events":%d,"recall_retention_days":%d}`+"\n",
		deleted, days, prunedEvents, retention)
	return 0
}
