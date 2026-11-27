package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
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

type maintainRequest struct {
	ms              MaintStore
	shallow         bool
	id              int
	from            int
	to              int
	old             int
	by              int
	days            int
	limit           int
	dupLimit        int
	expireDays      int
	recheckID       int
	conditionsVal   string
	conditionsGiven bool
	reason          string
	yes             bool
	commit          bool
	idList          []int
	applyVol        bool
}

type maintainMode struct {
	name   string
	flags  []string
	value  bool
	yes    bool
	commit bool
	run    func(ctx context.Context, r *maintainRequest, stdout io.Writer) int
}

func maintainModes() []maintainMode {
	return []maintainMode{
		{name: "--check", run: maintainRunCheck},
		{name: "--meta-audit", run: maintainRunMetaAudit},

		{name: "--prune-invalidated", value: true, commit: true, run: maintainRunPruneInvalidated},
		{name: "--dup-suspects", flags: []string{"--limit"}, run: maintainRunDupSuspects},
		{name: "--conflict-suspects", flags: []string{"--limit"}, run: maintainRunConflictSuspects},
		{name: "--stale", flags: []string{"--days", "--limit"}, run: maintainRunStale},
		{name: "--invalidate-stale", flags: []string{"--days", "--limit"}, yes: true, run: maintainRunInvalidateStale},
		{name: "--invalidate", flags: []string{"--id", "--reason", "--ids"}, yes: true, run: maintainRunInvalidate},
		{name: "--merge", flags: []string{"--from", "--to"}, run: maintainRunMerge},
		{name: "--supersede", flags: []string{"--supersede", "--by"}, run: maintainRunSupersede},

		{name: "--expire-pending", value: true, commit: true, run: maintainRunExpirePending},

		{name: "--decay-audit", flags: []string{"--limit"}, run: maintainRunDecayAudit},

		{name: "--stale-state", commit: true, run: maintainRunStaleState},

		{name: "--age-audit", flags: []string{"--limit", "--apply-vol"}, run: maintainRunAgeAudit},

		{name: "--merge-audit", flags: []string{"--limit"}, run: maintainRunMergeAudit},

		{name: "--recall-audit", flags: []string{"--limit"}, run: maintainRunRecallAudit},

		{name: "--reaffirm", flags: []string{"--id", "--ids"}, yes: true, run: maintainRunReaffirm},

		{name: "--recheck", flags: []string{"--conditions"}, value: true, run: maintainRunRecheck},

		{name: "--gc", yes: true, run: maintainRunGC},
		{name: "--purge-dropped", yes: true, run: maintainRunPurgeDropped},
	}
}

func maintainModeSpec(name string) (maintainMode, bool) {
	for _, m := range maintainModes() {
		if m.name == name {
			return m, true
		}
	}
	return maintainMode{}, false
}

var maintainModeFlags = maintainModeFlagSets()

func maintainModeFlagSets() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(maintainModes()))
	for _, m := range maintainModes() {
		allowed := make(map[string]bool, len(m.flags))
		for _, f := range m.flags {
			allowed[f] = true
		}
		out[m.name] = allowed
	}
	return out
}

var maintainYesModes = maintainYesModeSet()

func maintainYesModeSet() map[string]bool {
	out := map[string]bool{}
	for _, m := range maintainModes() {
		if m.yes {
			out[m.name] = true
		}
	}
	return out
}

var maintainCommitModes = maintainCommitModeSet()

func maintainCommitModeSet() map[string]bool {
	out := map[string]bool{}
	for _, m := range maintainModes() {
		if m.commit {
			out[m.name] = true
		}
	}
	return out
}

var maintainValueFlags = []string{"--days", "--limit", "--id", "--from", "--to", "--reason",
	"--ids", "--supersede", "--by", "--conditions"}

var MaintainRetryDelays = db.DefaultDelays

var MaintainRetryDelaysFn = func() []time.Duration { return MaintainRetryDelays }

func retryWrite(ctx context.Context, fn func() error) error {
	return db.Retry(ctx, fn, MaintainRetryDelaysFn())
}

var maintainUsageLine = i18n.T("usage.maintain")

func maintainUsageErrf(format string, a ...any) *wire.UsageError {
	return wire.UsageErrorf(format+" — "+maintainUsageLine, a...)
}

func RunMaintain(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	ms, ok := st.(MaintStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	specFlags := make([]wire.FlagSpec, 0, len(maintainModes())+len(maintainValueFlags)+3)
	for _, m := range maintainModes() {
		specFlags = append(specFlags, wire.FlagSpec{Name: m.name, Value: m.value})
	}
	for _, f := range maintainValueFlags {
		specFlags = append(specFlags, wire.FlagSpec{Name: f, Value: true})
	}
	specFlags = append(specFlags,
		wire.FlagSpec{Name: "--shallow"}, wire.FlagSpec{Name: "--commit"}, wire.FlagSpec{Name: "--yes"},
		wire.FlagSpec{Name: "--apply-vol"})
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: specFlags,
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
	yes, commit, applyVol := res.Given("--yes"), res.Given("--commit"), res.Given("--apply-vol")
	var idList []int

	for _, name := range res.FlagOrder() {
		if _, ok := maintainModeSpec(name); ok {
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

	if yes && !maintainYesModes[mode] && !(mode == "--recheck" && conditionsGiven) && !(mode == "--age-audit" && applyVol) {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --yes is not valid with %s", mode)))
		return 1
	}

	if applyVol && mode != "--age-audit" {
		if mode == "" {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf(
				"maintain: --apply-vol is not valid without a mode (it is --age-audit-scoped)")))
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("maintain: --apply-vol is not valid with %s", mode)))
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

	if m, ok := maintainModeSpec(mode); ok {
		return m.run(ctx, &maintainRequest{
			ms: ms, shallow: res.Given("--shallow"),
			id: id, from: from, to: to, old: old, by: by,
			days: days, limit: limit, dupLimit: dupLimit, expireDays: expireDays,
			recheckID: recheckID, conditionsVal: conditionsVal, conditionsGiven: conditionsGiven,
			reason: reason, yes: yes, commit: commit, idList: idList, applyVol: applyVol,
		}, stdout)
	}

	fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", maintainUsageLine)))
	return 3
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

func adminErrLine(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrNotFound) {
		return 1
	}
	return store.MapExit(err)
}
