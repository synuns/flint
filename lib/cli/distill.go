// distill.go — `flint distill --out DIR [--min-score N]` (v1.22.0 T6,
// ADR-0018). Emits the selected insight rows (recall_score >=
// distill_min_score, active, never emitted) as ONE aggregate markdown
// file per run — insights-<YYYYMMDD-HHMM>.md in the --out dir, row
// sections in stable id order. Output contract: the body goes only to
// the specified folder (never vault notes, never remote — ADR-0015);
// the summary and overwrite warning are one stderr line each.
// Atomicity: rows are stamped distilled_at=now() only AFTER a
// successful file write (a failed write leaves everything unstamped, so
// the re-run re-emits them). The default clock/seam vars follow the
// export convention (test fake injection; SUPERLITE_REAL=1 on the seam
// defaults is the live-connection verification).
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

// distill seams — the source query (knowledge.DistillRows), the run
// clock (aggregate file timestamp), the stderr channel, and the file
// write (failure injection).
var (
	distillRowsFn                = knowledge.DistillRows
	distillNowFn                 = func() time.Time { return time.Now().UTC() }
	distillStderr      io.Writer = os.Stderr
	distillWriteFileFn           = os.WriteFile
)

// distillStamper — the store extension carrying the distilled_at stamp
// write (the production realStore implements it; the write SQL literal
// lives in this store layer — the Reaffirm convention).
type distillStamper interface {
	MarkDistilled(ctx context.Context, ids []int) (int64, error)
}

// compile-time guarantee — realStore carries the distill stamp write.
var _ distillStamper = (*realStore)(nil)

// MarkDistilled — real implementation of the distillStamper extension:
// stamps the emitted rows distilled_at=now(). The distilled_at IS NULL
// guard keeps the stamp idempotent (a concurrent run's rows are never
// overwritten). The dolt commit is the caller's (runDistill)
// responsibility — the Reaffirm convention.
func (s *realStore) MarkDistilled(ctx context.Context, ids []int) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET distilled_at = now()
		 WHERE id = ANY($1) AND distilled_at IS NULL AND status = 'active' AND invalid_at IS NULL`, ids)
	if err != nil {
		return 0, fmt.Errorf("distill stamp: %w", err)
	}
	return ct.RowsAffected(), nil
}

// distillFileNameFor — the per-run aggregate file name
// insights-<YYYYMMDD-HHMM>.md (UTC — one file per run minute; a re-run
// within the same minute overwrites with a warning).
func distillFileNameFor(now time.Time) string {
	return "insights-" + now.UTC().Format("20060102-1504") + ".md"
}

func init() { Register("distill", runDistill) }

// runDistill — `flint distill --out DIR [--min-score N]`. --out DIR is
// required (missing/empty exits 3 — the local-folder-only contract of
// ADR-0018). --min-score (>= 0, float) takes precedence over the
// distill_min_score threshold override, which in turn replaces the
// DefaultThresholds value. nil store or server-down exits 2; a query,
// write, stamp, or commit failure exits 1 (a write failure leaves the
// rows unstamped). On success: one aggregate file in DIR and one
// summary line on stderr.
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
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "min-score", Expect: ">=0 float"}))
				return 3
			}
			minFlag, minGiven = v, true
		}
	}
	if outDir == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "required --out DIR"}))
		return 3
	}
	// contract: nil store or server-down exits 2 (the export convention).
	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return store.MapExit(store.ErrServerDown)
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// effective floor: --min-score > meta override > DefaultThresholds
	// (the unset-0 consumer-fallback convention — ApplyThresholdsFallback
	// does not complete float keys).
	minScore := store.DefaultThresholds().DistillMinScore
	if th, err := wire.LoadThresholdsFn(ctx, st); err == nil && th.DistillMinScore > 0 {
		minScore = th.DistillMinScore
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
	// stable id order for the aggregate file and the stamp (the SQL
	// already orders by id — the sort is the in-memory guarantee for the
	// emitted body, defense against seam/concurrent reordering).
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) == 0 {
		// nothing unstamped above the floor — no file, no stamp (idempotence).
		fmt.Fprintf(distillStderr, "distilled 0 insights — no unstamped rows above min score (no file written)\n")
		return 0
	}
	now := distillNowFn()
	path := filepath.Join(outDir, distillFileNameFor(now))
	if _, serr := os.Stat(path); serr == nil {
		fmt.Fprintf(distillStderr, "distill: overwriting existing file: %s\n", path)
	}
	data := knowledge.FormatInsights(rows, now)
	// atomicity: stamp only after a successful file write (ADR-0018) — a
	// failed write leaves every row unstamped, so the re-run re-emits them.
	if werr := distillWriteFileFn(path, data, 0o644); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}
	ids := make([]int, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	if ms, ok := st.(distillStamper); ok {
		n, serr := ms.MarkDistilled(ctx, ids)
		if serr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(serr))
			return 1
		}
		if n > 0 {
			// persist the stamp (the dolt commit convention of the maintain
			// write paths); a commit failure reports exit 1 — the stamp is
			// already applied, so a re-run will not re-emit these rows.
			if _, cerr := st.Commit(ctx, fmt.Sprintf("distill: %d insights stamped", n)); cerr != nil {
				fmt.Fprintln(stdout, wire.ErrLine(cerr))
				return 1
			}
		}
	}
	fmt.Fprintf(distillStderr, "distilled %d insights (%s, %d bytes)\n", len(rows), filepath.Base(path), len(data))
	return 0
}
