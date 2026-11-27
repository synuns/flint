package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

var (
	_ ops.ReaffirmStore           = (*realStore)(nil)
	_ ops.RecheckConditionsStore  = (*realStore)(nil)
	_ ops.RecheckSourceClearStore = (*realStore)(nil)
	_ ops.ApplyVolStore           = (*realStore)(nil)
)

var maintainTuiFn = tui.RunMaintainTUI

var maintainOpsRun = ops.RunMaintain

func init() {

	Register("maintain", runMaintainEntry)
}

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

		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"maintain: --tui is blocked by FLINT_NO_TUI — use maintain flag-mode subcommands")))
			return 3
		}
		return maintainTuiFn(ctx, st, stdout)
	}
	return maintainOpsRun(ctx, st, args, stdin, stdout)
}

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

func (s *realStore) ClearVaultSourceChanged(ctx context.Context, path string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE vault_files SET content_changed_at = NULL
		 WHERE path = $1 AND content_changed_at IS NOT NULL`, path)
	if err != nil {
		return 0, fmt.Errorf("recheck source clear: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (s *realStore) MergeVolTag(ctx context.Context, id int, volTag string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET tags = COALESCE(tags, '') || $2
		 WHERE id = $1 AND status = 'active' AND invalid_at IS NULL
		   AND strpos(COALESCE(tags, ''), $2) = 0`, id, volTag)
	if err != nil {
		return 0, fmt.Errorf("apply-vol: %w", err)
	}
	return ct.RowsAffected(), nil
}
