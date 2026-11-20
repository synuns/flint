// realstore_supply.go — realStore method extraction file accompanying the wave-3 ops
// move (report, report_html, backup,
// sigverify, rules_sync) — the same pattern continued from realstore_diag and realstore_care.
// The Dump delegation in backup.go
// (the ops dumpStore contract — the RunBackup injected st takes precedence for the dump path) lands here. The physical pool is opened and closed inside lib/backup.Dump (defer).
package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/backup"
)

func (s *realStore) Dump(ctx context.Context, w io.Writer) error {
	return backup.Dump(ctx, dsnFromEnv(), w)
}
