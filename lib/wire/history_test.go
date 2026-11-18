// history_test.go — Store 히스토리 계약(5메서드) 타입·기본구현 검증.
// HistoryStubs는 fake 임베딩용 기본구현: 임베드한 fake가 Store를 충족.
package wire

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubBaseStore — 기존 7메서드만 구현한 최소 fake. HistoryStubs를
// 임베드해 히스토리 5메서드를 기본구현으로 흡수하는지 검증한다.
type stubBaseStore struct {
	HistoryStubs
}

func (s *stubBaseStore) Migrate(ctx context.Context) error                      { return nil }
func (s *stubBaseStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return nil, nil
}
func (s *stubBaseStore) ExistsHash(ctx context.Context, h string) (bool, error) {
	return false, nil
}
func (s *stubBaseStore) ExistsProject(ctx context.Context, n string) (bool, error) {
	return false, nil
}
func (s *stubBaseStore) Commit(ctx context.Context, m string) (string, error)   { return "", nil }
func (s *stubBaseStore) Ping(ctx context.Context) error                         { return nil }
func (s *stubBaseStore) Close()                                                 {}
func (s *stubBaseStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return nil, nil
}

// HistoryStubs 임베딩만으로 Store 전체를 충족해야 한다.
var _ Store = &stubBaseStore{}

func TestHistoryTypesLiterals(t *testing.T) {
	// e.g. 값 리터럴 — 필드명·구조 확정(계약 고정).
	q := LogQuery{Hash: "abc1", Prefix: "abc", BeforeOrAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), LatestN: 2, MessageNotLike: "telemetry: %", MessageLike: "%[session: s-1]%"}
	if q.Hash != "abc1" || q.Prefix != "abc" || q.LatestN != 2 || q.MessageNotLike != "telemetry: %" || q.MessageLike != "%[session: s-1]%" {
		t.Fatalf("LogQuery 리터럴 불일치: %+v", q)
	}
	// MessageLike 빈 값=조건 없음(제로값 그대로), 설정 시 값 반영.
	q0 := LogQuery{}
	if q0.MessageLike != "" {
		t.Fatalf("MessageLike 제로값이 빈 문자열 아님: %q", q0.MessageLike)
	}
	e := LogEntry{Hash: "abc1", Committer: "flint", Message: "audit: demo", Date: time.Date(2026, 11, 18, 0, 0, 1, 0, time.UTC)}
	if e.Committer != "flint" || e.Message != "audit: demo" {
		t.Fatalf("LogEntry 리터럴 불일치: %+v", e)
	}
	a := AsOfQuery{Hash: "abc1", Table: "knowledge", Columns: []string{"count(*)"}, Where: "topic = $1", Args: []any{"demo"}, Limit: 5, FallbackTable: "knowledge"}
	if a.Table != "knowledge" || len(a.Columns) != 1 || a.FallbackTable != "knowledge" {
		t.Fatalf("AsOfQuery 리터럴 불일치: %+v", a)
	}
	d := KnowledgeDiffRow{DiffType: "added", ID: "k-7", Topic: "demo topic"}
	if d.DiffType != "added" || d.ID != "k-7" || d.Topic != "demo topic" {
		t.Fatalf("KnowledgeDiffRow 리터럴 불일치: %+v", d)
	}
	s := TableDiffStat{CommitHash: "a1", Table: "knowledge", DataChange: true, SchemaChange: false}
	if !s.DataChange || s.SchemaChange {
		t.Fatalf("TableDiffStat 리터럴 불일치: %+v", s)
	}
	c := DiffCommitSummary{Hash: "b2", Message: "audit: demo", Date: time.Date(2026, 11, 18, 0, 0, 1, 0, time.UTC), DataChange: true, SchemaChange: false}
	if c.Hash != "b2" || !c.DataChange {
		t.Fatalf("DiffCommitSummary 리터럴 불일치: %+v", c)
	}
}

func TestHistoryStubsReturnUnsupported(t *testing.T) {
	var st Store = &stubBaseStore{}
	ctx := context.Background()
	if _, err := st.LogEntries(ctx, LogQuery{Hash: "abc1"}); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("LogEntries: ErrHistoryUnsupported 아님: %v", err)
	}
	if _, err := st.QueryAsOf(ctx, AsOfQuery{Hash: "abc1", Table: "knowledge", Columns: []string{"count(*)"}}); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("QueryAsOf: ErrHistoryUnsupported 아님: %v", err)
	}
	if _, err := st.DiffRows(ctx, "a1", "b2"); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("DiffRows: ErrHistoryUnsupported 아님: %v", err)
	}
	if _, err := st.DiffStat(ctx, []string{"a1"}); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("DiffStat: ErrHistoryUnsupported 아님: %v", err)
	}
	if _, err := st.DiffCommits(ctx, "a1", "b2"); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("DiffCommits: ErrHistoryUnsupported 아님: %v", err)
	}
}
