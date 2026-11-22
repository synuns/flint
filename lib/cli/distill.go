// distill.go — `flint distill --out DIR [--min-score N]` (v1.22.0 T6,
// ADR-0018). Emits the selected insight rows (recall_score >=
// distill_min_score, active, never emitted) as ONE aggregate markdown
// file per run — insights-<YYYYMMDD-HHMM>.md in the --out dir, row
// sections in stable id order. Output contract: the body goes only to
// the specified folder (never vault notes, never remote — ADR-0015);
// the summary and collision notice are one stderr line each. v1.22.1
// r98: a same-minute rerun NEVER overwrites — it writes
// insights-<ts>-2.md instead (stamped rows cannot be re-emitted, so an
// overwrite would lose the earlier file's content permanently).
// Atomicity: rows are stamped distilled_at=now() only AFTER a
// successful file write (a failed write leaves everything unstamped, so
// the re-run re-emits them). v1.22.1 r97: the file is capped at
// distill_max_runes (default 20000) — rows past the cap stay unstamped
// and defer to the next run (a "N rows deferred" stderr notice). The
// default clock/seam vars follow the export convention (test fake
// injection; SUPERLITE_REAL=1 on the seam defaults is the
// live-connection verification).
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
// within the same minute never overwrites — see distillResolvePath).
func distillFileNameFor(now time.Time) string {
	return "insights-" + now.UTC().Format("20060102-1504") + ".md"
}

// distillResolvePath — the run's file path with collision resolution
// (v1.22.1 r98): the plain minute name when free, else
// insights-<ts>-2.md, -3, ... — NEVER an overwrite. A same-minute rerun
// emits different rows (the first run's are stamped), so overwriting
// would destroy the earlier file's content permanently — stamped rows
// cannot be re-emitted. A notice line names both files.
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
			// the 1e9 ceiling (v1.22.1 r98): the floor rides into
			// `COALESCE(re.cnt, 0) >= $1` against an integer count — a
			// MaxFloat-scale value breaks the parameter encoding as an
			// internal error, so refuse up front (usage exit 3).
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
	// does not complete float keys). 0 on the meta key is the UNSET
	// sentinel — only the --min-score 0 flag means "no floor".
	minScore := store.DefaultThresholds().DistillMinScore
	// effective file cap (v1.22.1 r97): meta distill_max_runes >
	// DefaultThresholds (unset-keeps-0 consumer fallback — 0 cannot be a
	// meaningful cap, so there is no 0-sentinel question here).
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
	path, rerr := distillResolvePath(outDir, distillFileNameFor(now))
	if rerr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(rerr))
		return 1
	}
	if suffix := filepath.Base(path); suffix != distillFileNameFor(now) {
		// the same-minute collision notice (never an overwrite — the
		// stamped rows of the earlier run cannot be re-emitted).
		fmt.Fprintf(distillStderr, "distill: file exists: %s — writing %s instead (no overwrite)\n",
			distillFileNameFor(now), suffix)
	}
	data, emitted := knowledge.FormatInsights(rows, now, maxRunes)
	// atomicity: stamp only after a successful file write (ADR-0018) — a
	// failed write leaves every row unstamped, so the re-run re-emits them.
	if werr := distillWriteFileFn(path, data, 0o644); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}
	// only the EMITTED rows stamp (v1.22.1 r97 file cap): the rows past
	// the cap stay distilled_at IS NULL and defer to the next run.
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
			// persist the stamp (the dolt commit convention of the maintain
			// write paths); a commit failure reports exit 1 — the stamp is
			// already applied, so a re-run will not re-emit these rows.
			if _, cerr := st.Commit(ctx, fmt.Sprintf("distill: %d insights stamped", n)); cerr != nil {
				fmt.Fprintln(stdout, wire.ErrLine(cerr))
				return 1
			}
		}
	}
	fmt.Fprintf(distillStderr, "distilled %d insights (%s, %d bytes)\n", emitted, filepath.Base(path), len(data))
	if deferred := len(rows) - emitted; deferred > 0 {
		// the file-cap carry-over (v1.22.1 r97): unstamped rows wait for
		// the next run — one notice line, never a silent drop.
		fmt.Fprintf(distillStderr, "distill: %d rows deferred (file cap %d runes) — the next run emits them\n", deferred, maxRunes)
	}
	return 0
}
