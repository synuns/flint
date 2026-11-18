// history.go — Store 히스토리 계약(5메서드)의 타입·에러·기본구현.
// 방언(Dolt/Doltgres) 구현은 lib/db가 소유하고, 이 층은 계약만 정의한다.
// HistoryStubs는 테스트 fake가 임베딩해 5메서드를 기본구현으로
// 흡수하는 용도(미지원 시 ErrHistoryUnsupported 반환).
package wire

import (
	"context"
	"errors"
	"time"
)

// ErrHistoryUnsupported — Store 구현이 히스토리 계약을 지원하지
// 않을 때 반환. fake 기본구현과 미지원 방언이 공용으로 사용한다.
var ErrHistoryUnsupported error = errors.New("flint: history queries unsupported by this store")

// LogQuery — LogEntries 조회 조건. Hash·Prefix는 커밋 해시 선택,
// BeforeOrAt·Earliest·LatestN·MessageNotLike·MessageLike는 범위·필터링.
// MessageLike·MessageNotLike 모두 빈 값이면 조건 없음(예: 세션
// 태그 message LIKE '%[session:ID]%' 표현).
type LogQuery struct {
	Hash           string
	Prefix         string
	BeforeOrAt     time.Time
	Earliest       bool
	LatestN        int
	MessageNotLike string
	MessageLike    string
}

// LogEntry — 커밋 로그 1건.
type LogEntry struct {
	Hash      string
	Committer string
	Message   string
	Date      time.Time
}

// AsOfQuery — 시점 조회. Where는 raw 꼬리 절(WHERE...ORDER BY
// 포함) 또는 조건-only(접두 WHERE 자동 부여) 양형식 — 내부 산출물만
// 전달한다(사용자 raw 유입 금지).
type AsOfQuery struct {
	Hash    string
	Table   string
	Columns []string
	Where   string
	Args    []any
	Limit   int
}

// KnowledgeDiffRow — 두 커밋 간 knowledge 행 diff 1건.
type KnowledgeDiffRow struct {
	DiffType string
	ID       string
	Topic    string
}

// TableDiffStat — 커밋 1건의 테이블별 변경 통계.
type TableDiffStat struct {
	CommitHash   string
	Table        string
	DataChange   bool
	SchemaChange bool
}

// DiffCommitSummary — 두 커밋 사이 커밋 요약(diff 대상).
type DiffCommitSummary struct {
	Hash         string
	Message      string
	Date         time.Time
	DataChange   bool
	SchemaChange bool
}

// HistoryStubs — 히스토리 5메서드 기본구현. fake에 임베딩하면
// 미지원 반환으로 Store 인터페이스를 충족시킨다.
type HistoryStubs struct{}

// LogEntries — 기본구현: 미지원.
func (HistoryStubs) LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	return nil, ErrHistoryUnsupported
}

// QueryAsOf — 기본구현: 미지원.
func (HistoryStubs) QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error) {
	return nil, ErrHistoryUnsupported
}

// DiffRows — 기본구현: 미지원.
func (HistoryStubs) DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error) {
	return nil, ErrHistoryUnsupported
}

// DiffStat — 기본구현: 미지원.
func (HistoryStubs) DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error) {
	return nil, ErrHistoryUnsupported
}

// DiffCommits — 기본구현: 미지원.
func (HistoryStubs) DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error) {
	return nil, ErrHistoryUnsupported
}
