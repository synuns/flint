// import_migrate.go — CLI entry for `flint import --format jsonl|pam`.
// The implementation is owned by lib/knowledge/import_jsonl.go (core) — cli re-exports only the
// contract signature entry (st, args, stdin, stdout) int (the existing
// knowledge_alias.go convention. Command routing is a one-line delegation to
// knowledge.go RunImportRouted).
package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// RunImportMigrate — entry for `flint import --format jsonl|pam [FILE]` (contract
// signature). Invalid format or broken jsonl exits 3; server-down exits 2.
func RunImportMigrate(st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return knowledge.RunImportMigrate(context.Background(), st, args, stdin, stdout)
}

// SetSupersededRef — delegates lineage-reference restoration (the Supersede delegation
// convention in realstore_care.go — writes go through the single db.Repo path).
func (s *realStore) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	return s.repo.SetSupersededRef(ctx, oldID, newID)
}

// compile guard — prevents db.Repo method signature drift.
var _ interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
} = (*db.Repo)(nil)
