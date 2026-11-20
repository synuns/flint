// Package wire — the flint CLI contract layer. Owns the Store interface, dispatch
// Run, registry, DSN resolution, the one-line error JSON, and meta KV access.
// Command implementations (lib/cli) connect to this layer via factory functions and Register.
package wire

import "context"

// Store — the store operations commands need. Tests inject fakes.
type Store interface {
	Migrate(ctx context.Context) error
	InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error)
	ExistsHash(ctx context.Context, hash string) (bool, error)
	ExistsProject(ctx context.Context, name string) (bool, error)
	Commit(ctx context.Context, msg string) (string, error)
	Ping(ctx context.Context) error
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()
	// the 5 history-contract methods — types and default implementations in history.go.
	LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error)
	QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error)
	DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error)
	DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error)
	DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error)
}

// QueryOnce — retries reader-family queries once, then propagates the failure (spec §7 reads).
func QueryOnce(ctx context.Context, st Store, sql string, args []any) ([][]any, error) {
	rows, err := st.Query(ctx, sql, args...)
	if err != nil {
		rows, err = st.Query(ctx, sql, args...)
	}
	return rows, err
}
