// approve.go — the approve command — passing the review gate (§8: approval
// = via conversation → the agent runs it). `flint approve --id N` —
// pending→active promotion. Success: {"status":"approved","id":N} exit 0;
// a missing id gets a one-line not_found JSON, exit 1.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// approveStore — the promotion access for Approve. The wire.Store interface
// is v0.1-owned, so it is consumed via type assertion instead of extension
// (root.go modifications forbidden — the done.go staleSessionStore
// convention). The realStore passed on real paths (root·mcp) always
// implements it.
type approveStore interface {
	Approve(ctx context.Context, id int) error
}

// ApproveFn — the legacy fallback seam. Used only when st does not
// implement Approve (a test harness without injection) — the default is an
// error (delegation to the real-path st is the premise). Tests swap the
// fake (approve_test.go).
var ApproveFn = func(ctx context.Context, id int) error {
	return errors.New("approve: injected store does not implement Approve")
}

// quoteJSON — wraps strings such as paths into a JSON string literal.
var quoteJSON = wire.QuoteJSON

// curationCounterKinds — the daily counter kind whitelist for review
// (curation) (spec §2.4 gap 1). Key kinds are capped at these eight
// (preventing infinite key proliferation from typo'd kinds — the
// diagnostics·report curation objects consume the same namespace). A
// dedicated key space, distinct from the existing intake metering
// (RecordUsageCount kind) — promotion·invalidation outcomes are review
// results, not intake.
var curationCounterKinds = map[string]bool{
	"approve_done": true, "approve_not_found": true, "approve_supersede_err": true,
	"maintain_supersede": true, "maintain_expire": true, "maintain_prune_rows": true,
	"import_added": true, "import_skipped": true,
}

// BumpCurationCounter — increments one daily flat-key review counter (v1.2.0
// T4). Raises the `daily:<kind>:<date>` (UTC) integer value with a single
// UPSERT — the query body is singly owned by db.BumpDailyFlatKey (the
// db-package ownership principle for write SQL literals — writes_through_repo
// guard scope) and runs through db.Querier, which st structurally satisfies
// (covers wire.Store·fakes alike). The increment is non-idempotent, so it
// fires exactly once without retry (inheriting the BumpMetaKV contract —
// preventing double-counting on ambiguous failure); n<=0·invalid kind·
// failure are quiet (§13 metering principle — a lost counter beats a failed
// call). Cleanup comes from PruneDaily's daily:* regex, which clears both
// formats (legacy JSON·flat) together. Calls with kinds outside the
// whitelist are not metered (the cap principle — preventing meta key
// proliferation from typo'd kinds).
func BumpCurationCounter(ctx context.Context, st wire.Store, kind string, n int) {
	if st == nil || n <= 0 || !curationCounterKinds[kind] {
		return
	}
	_ = db.BumpDailyFlatKey(ctx, st, kind, time.Now().UTC().Format("2006-01-02"), n)
}

// approveUsageErr — assembles the approve argument validation error:
// wire.UsageError classification (error:"usage") + a usage hint
// `--id N [--supersede OLD]` in the message — turning a mistyped flag into
// the correct guidance.
func approveUsageErr(format string, a ...any) error {
	return wire.UsageErrorf(format+" — usage: flint approve --id N [--supersede OLD]", a...)
}

// RunApprove — runs the approval. The promotion goes through the given st
// (R14 rewiring — previously it ignored st and opened its own Repo).
// v1.0.1 (F3): argument-validation paths (unknown flag·missing value·
// invalid id·missing --id·supersede==id) are unified to the invalid-arg
// exit 3 — matching the mcp dispatchApprove validation-failure contract.
// v1.5.3 (P2): the ErrLine error value on these paths is "usage"
// (previously internal).
func RunApprove(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	id := -1
	supersede := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--supersede":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(approveUsageErr("%s requires a number", flag)))
				return 3
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(approveUsageErr("%s: invalid id %q", flag, args[i])))
				return 3
			}
			if flag == "--id" {
				id = n
			} else {
				supersede = n
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(approveUsageErr("unknown flag: %s", args[i])))
			return 3
		}
	}
	if id < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(approveUsageErr("approve requires --id")))
		return 3
	}
	if supersede == id {
		fmt.Fprintln(stdout, wire.ErrLine(approveUsageErr("--supersede must differ from --id")))
		return 3
	}

	// Injected st first — realStore delegates Approve. Only a
	// non-implementing st (incl. nil) falls back to the legacy ApproveFn
	// seam.
	var err error
	if as, ok := st.(approveStore); ok {
		err = as.Approve(ctx, id)
	} else {
		err = ApproveFn(ctx, id)
	}
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			BumpCurationCounter(ctx, st, "approve_not_found", 1) // review counter
			fmt.Fprintln(stdout, `{"error":"not_found","id":`+strconv.Itoa(id)+`}`)
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	BumpCurationCounter(ctx, st, "approve_done", 1) // review counter — one successful promotion
	// Supersede at promotion time — approve --id N --supersede OLD
	// invalidates OLD (the old active knowledge) in favor of N and records
	// the precedence, all at once (the same UPDATE as the maintain
	// --supersede renewal path).
	if supersede > 0 {
		if st == nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("--supersede requires store")))
			return 1
		}
		if serr := supersedeAfterApprove(ctx, st, supersede, id); serr != nil {
			BumpCurationCounter(ctx, st, "approve_supersede_err", 1) // review counter
			fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
				`,"supersede_error":`+quoteJSON(serr.Error())+`}`)
			return 1
		}
		fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
			`,"superseded":`+strconv.Itoa(supersede)+`}`)
		return 0
	}
	// Similar-active notice — if an existing active shares the topic (or a
	// prefix match), surface it as a merge candidate (the promotion is
	// already complete — this is only a notice). nil st (test harness)
	// skips the notice — the promotion contract is unchanged.
	var sim []int
	if st != nil {
		sim = similarActiveIDs(ctx, st, id)
	}
	if len(sim) > 0 {
		ids := ""
		for i, s := range sim {
			if i > 0 {
				ids += ","
			}
			ids += strconv.Itoa(s)
		}
		fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
			`,"similar_active":[`+ids+`],"hint":"merge candidate — flint approve --id `+
			strconv.Itoa(id)+` --supersede OLD or flint maintain --supersede OLD --by NEW"}`)
		return 0
	}
	fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+`}`)
	return 0
}

func init() { wire.Register("approve", RunApprove) }

// supersedeStore — the supersede-transition capability. realStore implements
// it; the renewal UPDATE has the single path db.Repo.Supersede (rows-affected
// stewardship built in — the approveStore convention: wire.Store is v0.1-owned,
// so it is consumed via type assertion).
type supersedeStore interface {
	Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
}

// supersedeAfterApprove — supersedes OLD with NEW right after promotion.
// The commit goes through Store.Commit (dolt_add -A +
// dolt_commit — dolt_commit is a built-in function, not a table). The
// pre-check that OLD is active·the zero-row race stewardship are inherited
// from Repo.Supersede's rows-affected count.
func supersedeAfterApprove(ctx context.Context, st wire.Store, oldID, newID int) error {
	ss, ok := st.(supersedeStore)
	if !ok {
		return fmt.Errorf("supersede: injected store does not implement Supersede")
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT status FROM knowledge WHERE id = $1`, []any{oldID})
	if err != nil {
		return err
	}
	if len(rows) == 0 || rows[0][0] == nil || fmt.Sprint(rows[0][0]) != "active" {
		return fmt.Errorf("supersede: id=%d is not active", oldID)
	}
	// invalid_at = NEW's valid_at, carried over (per Zep t_invalid=t_valid).
	vrows, verr := wire.QueryOnce(ctx, st,
		`SELECT valid_at FROM knowledge WHERE id = $1`, []any{newID})
	if verr != nil {
		return verr
	}
	if len(vrows) == 0 || vrows[0][0] == nil {
		return fmt.Errorf("supersede: NEW missing id=%d", newID)
	}
	at, atOK := cellToTime(vrows[0][0])
	if !atOK {
		return fmt.Errorf("supersede: id=%d valid_at unreadable", newID)
	}
	if _, err := ss.Supersede(ctx, oldID, newID, at); err != nil {
		return err
	}
	_, err = st.Commit(ctx, fmt.Sprintf(
		"approve+supersede: id=%d superseded=%d (promotion merge path) [session:0]", newID, oldID))
	return err
}

// similarActiveIDs — the active ids similar to the promoted knowledge. The
// similarity approximation is topicSimilarityKey (12-rune prefix after
// stripping the common session prefix), a single Go implementation shared
// with the pending decision — preventing SQL dual-implementation drift.
func similarActiveIDs(ctx context.Context, st wire.Store, id int) []int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT topic FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 || rows[0][0] == nil {
		return nil
	}
	key := topicSimilarityKey(fmt.Sprint(rows[0][0]))
	actives, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL AND id != $1`,
		[]any{id})
	if err != nil {
		return nil
	}
	return matchTopicPrefix(key, actives)
}
