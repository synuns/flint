// history.go — the types, errors, and default implementations of the Store history
// contract (5 methods). The dialect (Dolt/Doltgres) implementation is owned by lib/db;
// this layer defines only the contract. HistoryStubs is for test fakes to embed,
// absorbing the 5 methods as default implementations (returning ErrHistoryUnsupported when unsupported).
package wire

import (
	"context"
	"errors"
	"time"
)

// ErrHistoryUnsupported — returned when a Store implementation does not support
// the history contract. Shared by the fake defaults and unsupported dialects.
var ErrHistoryUnsupported error = errors.New("flint: history queries unsupported by this store")

// LogQuery — the LogEntries lookup conditions. Hash and Prefix select the commit
// hash; BeforeOrAt, Earliest, LatestN, MessageNotLike, and MessageLike are range
// and filtering. Both MessageLike and MessageNotLike are no-conditions when
// empty (e.g. expressing the session-tag message LIKE '%[session:ID]%').
type LogQuery struct {
	Hash           string
	Prefix         string
	BeforeOrAt     time.Time
	Earliest       bool
	LatestN        int
	MessageNotLike string
	MessageLike    string
}

// LogEntry — one commit-log entry.
type LogEntry struct {
	Hash      string
	Committer string
	Message   string
	Date      time.Time
}

// AsOfQuery — a point-in-time query. Where is either a raw trailing clause
// (including WHERE...ORDER BY) or conditions-only (the WHERE prefix is added
// automatically) — only internal products are passed (no user raw input).
type AsOfQuery struct {
	Hash    string
	Table   string
	Columns []string
	Where   string
	Args    []any
	Limit   int
}

// KnowledgeDiffRow — one knowledge row diff between two commits.
type KnowledgeDiffRow struct {
	DiffType string
	ID       string
	Topic    string
}

// TableDiffStat — the per-table change statistics of one commit.
type TableDiffStat struct {
	CommitHash   string
	Table        string
	DataChange   bool
	SchemaChange bool
}

// DiffCommitSummary — the commit summary between two commits (the diff targets).
type DiffCommitSummary struct {
	Hash         string
	Message      string
	Date         time.Time
	DataChange   bool
	SchemaChange bool
}

// HistoryStubs — the default implementations of the 5 history methods. Embedding
// in a fake satisfies the Store interface by returning unsupported.
type HistoryStubs struct{}

// LogEntries — the default implementation: unsupported.
func (HistoryStubs) LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	return nil, ErrHistoryUnsupported
}

// QueryAsOf — the default implementation: unsupported.
func (HistoryStubs) QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error) {
	return nil, ErrHistoryUnsupported
}

// DiffRows — the default implementation: unsupported.
func (HistoryStubs) DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error) {
	return nil, ErrHistoryUnsupported
}

// DiffStat — the default implementation: unsupported.
func (HistoryStubs) DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error) {
	return nil, ErrHistoryUnsupported
}

// DiffCommits — the default implementation: unsupported.
func (HistoryStubs) DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error) {
	return nil, ErrHistoryUnsupported
}
