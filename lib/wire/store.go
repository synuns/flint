package wire

import (
	"context"
	"errors"
)

type Store interface {
	Migrate(ctx context.Context) error
	InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error)
	ExistsHash(ctx context.Context, hash string) (bool, error)
	ExistsProject(ctx context.Context, name string) (bool, error)
	Commit(ctx context.Context, msg string) (string, error)
	Ping(ctx context.Context) error
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()

	LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error)
	QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error)
	DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error)
	DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error)
	DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error)
}

func QueryOnce(ctx context.Context, st Store, sql string, args []any) ([][]any, error) {
	rows, err := st.Query(ctx, sql, args...)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return rows, err
	}
	rows, err = st.Query(ctx, sql, args...)
	return rows, err
}
