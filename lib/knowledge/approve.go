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

type approveStore interface {
	Approve(ctx context.Context, id int) error
}

var ApproveFn = func(ctx context.Context, id int) error {
	return errors.New("approve: injected store does not implement Approve")
}

var quoteJSON = wire.QuoteJSON

var curationCounterKinds = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range db.MeteringKinds("curation") {
		m[k] = true
	}
	return m
}()

func BumpCurationCounter(ctx context.Context, st wire.Store, kind string, n int) {
	if st == nil || n <= 0 || !curationCounterKinds[kind] {
		return
	}
	_ = db.BumpDailyFlatKey(ctx, st, kind, time.Now().UTC().Format("2006-01-02"), n)
}

func approveUsageErr(format string, a ...any) error {
	return wire.UsageErrorf(format+" — usage: flint approve --id N [--supersede OLD]", a...)
}

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

	var err error
	if as, ok := st.(approveStore); ok {
		err = as.Approve(ctx, id)
	} else {
		err = ApproveFn(ctx, id)
	}
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			BumpCurationCounter(ctx, st, "approve_not_found", 1)
			fmt.Fprintln(stdout, `{"error":"not_found","id":`+strconv.Itoa(id)+`}`)
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	BumpCurationCounter(ctx, st, "approve_done", 1)

	bumpApproveOriginCounter(ctx, st, id)

	if supersede > 0 {
		if st == nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("--supersede requires store")))
			return 1
		}
		if serr := supersedeAfterApprove(ctx, st, supersede, id); serr != nil {
			BumpCurationCounter(ctx, st, "approve_supersede_err", 1)
			fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
				`,"supersede_error":`+quoteJSON(serr.Error())+`}`)
			return 1
		}
		fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
			`,"superseded":`+strconv.Itoa(supersede)+`}`)
		return 0
	}

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

func approveOriginCounterKind(origin string) (string, bool) {
	switch origin {
	case "correction":
		return "correction_approved", true
	case "gap":
		return "gap_approved", true
	}
	return "", false
}

func bumpApproveOriginCounter(ctx context.Context, st wire.Store, id int) {
	if st == nil {
		return
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT origin FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 || rows[0][0] == nil {
		return
	}
	kind, ok := approveOriginCounterKind(fmt.Sprint(rows[0][0]))
	if !ok {
		return
	}
	BumpCurationCounter(ctx, st, kind, 1)
}

type supersedeStore interface {
	Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error)
}

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
