// maintain.go — entry branching for `flint maintain --tui`. Opt-in only
// (no adaptive switching — the maintain subcommand takes many arguments, which prevents ghost-argument
// regressions): --tui standalone only goes to the TUI (tui.RunMaintainTUI); combinations are invalid-arg
// exit 3. FLINT_NO_TUI is layer 1 of the 3-layer guard — even an explicit --tui is blocked, then the existing path is
// announced. Everything else delegates unchanged to ops.RunMaintain (pipes and CI unaffected).
// The TUI launcher and ops delegation are seam vars (test fake injection).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// compile-time guarantee — realStore carries the reaffirm write extension
// (ops.ReaffirmStore — v1.22.0 T3), the recheck conditions write
// extension (ops.RecheckConditionsStore — v1.22.1 r96), and the vault
// source-change clear extension (ops.RecheckSourceClearStore — v1.24.0
// T2).
var (
	_ ops.ReaffirmStore           = (*realStore)(nil)
	_ ops.RecheckConditionsStore  = (*realStore)(nil)
	_ ops.RecheckSourceClearStore = (*realStore)(nil)
)

// maintainTuiFn — TUI entry seam. Real implementation by default; tests swap in a fake.
var maintainTuiFn = tui.RunMaintainTUI

// maintainOpsRun — delegation seam for the existing maintain path (ops.RunMaintain — no duplicate
// implementation). Real implementation by default; entry-branching tests swap in a fake.
var maintainOpsRun = ops.RunMaintain

func init() {
	// overwrites the ops.init registration (RunMaintain) — inherits the latest wire.Register value
	// (same-name re-registration convention, the pending precedent). The default path is unchanged via
	// the delegation below (this file owns only the --tui branch).
	Register("maintain", runMaintainEntry)
}

// runMaintainEntry — maintain handler. Entry rules: (1) --tui standalone -> TUI
// (combinations are invalid-arg exit 3; blocked with exit 3 when FLINT_NO_TUI is set) (2) everything
// else (no args, any flags) -> the current ops.RunMaintain path — opt-in only, so there is no
// no-arg + terminal adaptive switching (unlike pending and export, to prevent ghost
// arguments).
func runMaintainEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					"maintain: --tui cannot be combined with %s — run it alone", b)))
				return 3
			}
		}
		// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY
		// (the explicit agent/CI escape hatch takes precedence).
		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"maintain: --tui is blocked by FLINT_NO_TUI — use maintain flag-mode subcommands")))
			return 3
		}
		return maintainTuiFn(ctx, st, stdout)
	}
	return maintainOpsRun(ctx, st, args, stdin, stdout)
}

// realStore.Reaffirm — real implementation of the ops.ReaffirmStore
// extension (v1.22.0 T3): the single valid_at=now() UPDATE of the target
// active knowledge rows, plus the maintenance_runs record (kind=reaffirm —
// the drill record convention). The write SQL literal lives in this store
// layer (the lib/{ops,knowledge} write-guard scope owns write verbs here —
// the InsertKnowledgeLink convention). The dolt commit is the caller's
// (ops) responsibility — the Invalidate-class convention.
func (s *realStore) Reaffirm(ctx context.Context, ids []int) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET valid_at = now()
		 WHERE id = ANY($1) AND status = 'active' AND invalid_at IS NULL`, ids)
	if err != nil {
		return 0, fmt.Errorf("reaffirm: %w", err)
	}
	n := ct.RowsAffected()
	if n > 0 {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO maintenance_runs (kind, details) VALUES ('reaffirm', $1)`,
			fmt.Sprintf("reaffirmed ids=%v", ids)); err != nil {
			return n, fmt.Errorf("reaffirm: maintenance_runs: %w", err)
		}
	}
	return n, nil
}

// realStore.SetRecheckConditions — real implementation of the
// ops.RecheckConditionsStore extension (v1.22.1 r96): the single
// knowledge.conditions UPDATE of one target active row plus the
// maintenance_runs record (kind=recheck — the drill record convention).
// conditions is hash-excluded (hashVolatile — the T5 registration), so the
// row's content_hash never moves. The dolt commit is the caller's (ops)
// responsibility — the Invalidate-class convention.
func (s *realStore) SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET conditions = $2
		 WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`, id, conditions)
	if err != nil {
		return 0, fmt.Errorf("recheck conditions: %w", err)
	}
	n := ct.RowsAffected()
	if n > 0 {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO maintenance_runs (kind, details) VALUES ('recheck', $1)`,
			fmt.Sprintf("conditions set id=%d (%d runes)", id, len([]rune(conditions)))); err != nil {
			return n, fmt.Errorf("recheck conditions: maintenance_runs: %w", err)
		}
	}
	return n, nil
}

// realStore.ClearVaultSourceChanged — real implementation of the
// ops.RecheckSourceClearStore extension (v1.24.0 T2): clears the
// content_changed_at stamp of the vault_files rows whose path equals the
// rechecked knowledge row's source_path (the recheck queue consumption —
// every --recheck invocation clears, the --conditions argument is
// optional). The stamp-clearing guard keeps the UPDATE idempotent (only
// NOT NULL rows match, so the rows-affected count names the actually
// cleared files). The dolt commit is the caller's (ops) responsibility —
// the Invalidate-class convention.
func (s *realStore) ClearVaultSourceChanged(ctx context.Context, path string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE vault_files SET content_changed_at = NULL
		 WHERE path = $1 AND content_changed_at IS NOT NULL`, path)
	if err != nil {
		return 0, fmt.Errorf("recheck source clear: %w", err)
	}
	return ct.RowsAffected(), nil
}
