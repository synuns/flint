package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var (
	distillRowsFn                = knowledge.DistillRows
	distillNowFn                 = func() time.Time { return time.Now().UTC() }
	distillStderr      io.Writer = os.Stderr
	distillWriteFileFn           = os.WriteFile
)

type distillStamper interface {
	MarkDistilled(ctx context.Context, ids []int) (int64, error)
}

var _ distillStamper = (*realStore)(nil)

func (s *realStore) MarkDistilled(ctx context.Context, ids []int) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET distilled_at = now()
		 WHERE id = ANY($1) AND distilled_at IS NULL AND status = 'active' AND invalid_at IS NULL`, ids)
	if err != nil {
		return 0, fmt.Errorf("distill stamp: %w", err)
	}
	return ct.RowsAffected(), nil
}

func distillFileNameFor(now time.Time) string {
	return "insights-" + now.UTC().Format("20060102-1504") + ".md"
}

func distillResolvePath(dir, baseName string) (string, error) {
	first := filepath.Join(dir, baseName)
	if _, err := os.Stat(first); os.IsNotExist(err) {
		return first, nil
	}
	ext := filepath.Ext(baseName)
	stem := baseName[:len(baseName)-len(ext)]
	for i := 2; i < 1000; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p, nil
		}
	}
	return "", fmt.Errorf("distill: file collision limit reached for %s", baseName)
}

func init() { Register("distill", runDistill) }

func runDistill(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--out", Value: true},
			{Name: "--min-score", Value: true},
		},
		Usage: "usage: flint distill --out DIR [--min-score N]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	outDir, minFlag, minGiven := "", 0.0, false
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--out":
			if o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "non-empty"}))
				return 3
			}
			outDir = o.Value
		case "--min-score":
			v, err := strconv.ParseFloat(o.Value, 64)

			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "min-score", Expect: "0..1e9 float"}))
				return 3
			}
			minFlag, minGiven = v, true
		}
	}
	if outDir == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "required --out DIR"}))
		return 3
	}

	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return store.MapExit(store.ErrServerDown)
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	minScore := store.DefaultThresholds().DistillMinScore

	maxRunes := store.DefaultThresholds().DistillMaxRunes
	if th, err := wire.LoadThresholdsFn(ctx, st); err == nil {
		if th.DistillMinScore > 0 {
			minScore = th.DistillMinScore
		}
		if th.DistillMaxRunes > 0 {
			maxRunes = th.DistillMaxRunes
		}
	}
	if minGiven {
		minScore = minFlag
	}
	if distillRowsFn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("distill: source not linked")))
		return 1
	}
	rows, err := distillRowsFn(ctx, st, minScore)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) == 0 {

		fmt.Fprintf(distillStderr, "distilled 0 insights — no unstamped rows above min score (no file written)\n")
		return 0
	}
	now := distillNowFn()
	path, rerr := distillResolvePath(outDir, distillFileNameFor(now))
	if rerr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(rerr))
		return 1
	}
	if suffix := filepath.Base(path); suffix != distillFileNameFor(now) {

		fmt.Fprintf(distillStderr, "distill: file exists: %s — writing %s instead (no overwrite)\n",
			distillFileNameFor(now), suffix)
	}
	data, emitted := knowledge.FormatInsights(rows, now, maxRunes)

	if werr := distillWriteFileFn(path, data, 0o644); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}

	ids := make([]int, emitted)
	for i, r := range rows[:emitted] {
		ids[i] = r.ID
	}
	if ms, ok := st.(distillStamper); ok {
		n, serr := ms.MarkDistilled(ctx, ids)
		if serr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(serr))
			return 1
		}
		if n > 0 {

			if _, cerr := st.Commit(ctx, fmt.Sprintf("distill: %d insights stamped", n)); cerr != nil {
				fmt.Fprintln(stdout, wire.ErrLine(cerr))
				return 1
			}
		}
	}
	fmt.Fprintf(distillStderr, "distilled %d insights (%s, %d bytes)\n", emitted, filepath.Base(path), len(data))
	if deferred := len(rows) - emitted; deferred > 0 {

		fmt.Fprintf(distillStderr, "distill: %d rows deferred (file cap %d runes) — the next run emits them\n", deferred, maxRunes)
	}
	return 0
}
