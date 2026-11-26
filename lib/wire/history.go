package wire

import (
	"context"
	"errors"
	"time"
)

var ErrHistoryUnsupported error = errors.New("flint: history queries unsupported by this store")

type LogQuery struct {
	Hash           string
	Prefix         string
	BeforeOrAt     time.Time
	Earliest       bool
	LatestN        int
	MessageNotLike string
	MessageLike    string
}

type LogEntry struct {
	Hash      string
	Committer string
	Message   string
	Date      time.Time
}

type AsOfQuery struct {
	Hash    string
	Table   string
	Columns []string
	Where   string
	Args    []any
	Limit   int
}

type KnowledgeDiffRow struct {
	DiffType string
	ID       string
	Topic    string
}

type TableDiffStat struct {
	CommitHash   string
	Table        string
	DataChange   bool
	SchemaChange bool
}

type DiffCommitSummary struct {
	Hash         string
	Message      string
	Date         time.Time
	DataChange   bool
	SchemaChange bool
}

type HistoryStubs struct{}

func (HistoryStubs) LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	return nil, ErrHistoryUnsupported
}

func (HistoryStubs) QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error) {
	return nil, ErrHistoryUnsupported
}

func (HistoryStubs) DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error) {
	return nil, ErrHistoryUnsupported
}

func (HistoryStubs) DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error) {
	return nil, ErrHistoryUnsupported
}

func (HistoryStubs) DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error) {
	return nil, ErrHistoryUnsupported
}

func (HistoryStubs) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	return 0, "", ErrHistoryUnsupported
}

func (HistoryStubs) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	return ErrHistoryUnsupported
}

func (HistoryStubs) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	return nil
}

func (HistoryStubs) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	return nil
}
