// echo_test.go — v1.17.0 T1: the EchoScan contract (pure scoring,
// bucketing, caps, filter against the fake store; the real-DB scan is the
// slow seam below).
package knowledge

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// echoTestTopic — the shared synthetic topic of the fast fixtures.
const echoTestTopic = "데모 프로젝트 운영 기록"

// The fixture summary triple (computed against lib/query measures):
//   - dup: identical row -> multiset 1.0 -> duplicates
//   - contradicts: same topic, different claim -> multiset 0.5151.., set 0.508,
//     topic dice 1.0 -> contradicts
//   - excluded: same topic but a long disjoint summary -> set 0.255 < 0.3 -> excluded
const (
	echoDupSummary       = "회의록 오늘 확정된 마일스톤은 3개다"
	echoContraSummary    = "회의록 다음 주는 전체 휴가 기간이다"
	echoExcludedSummary  = "텔레비전 편성표 조정 작업 기록과 성수기 특별 편성 반영시 검토해야 할 광고 단가 구간표 초안 내용을 함께 기록한다"
	echoUnrelatedTopic   = "개인 독서 목록 정리"
	echoUnrelatedSummary = "올해 읽은 책 다섯 권 감상"
)

// echoFixtures — the standard fake rows (id, topic, summary).
func echoFixtures() [][]any {
	return [][]any{
		{int64(1), echoTestTopic, echoDupSummary},
		{int64(2), echoTestTopic, echoContraSummary},
		{int64(3), echoTestTopic, echoExcludedSummary},
		{int64(4), echoUnrelatedTopic, echoUnrelatedSummary},
	}
}

// TestEchoScanBucketsAndFilter — the three-way bucketing: an identical row
// is duplicates (multiset 1.0), a same-topic different-claim row is
// contradicts, a below-min-score row and a fully unrelated row are excluded.
// The scan SQL carries the active filter (invalid_at IS NULL) and the
// suggest_scan_cap row cap (LIMIT 5000).
func TestEchoScanBucketsAndFilter(t *testing.T) {
	st := &fakeStore{rows: echoFixtures()}
	got, err := EchoScan(context.Background(), st, echoTestTopic, echoDupSummary)
	if err != nil {
		t.Fatalf("EchoScan 에러: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("후보 %d건, want 2 (dup+contradicts): %+v", len(got), got)
	}
	// Score-descending order: the duplicate (1.0) precedes the contradiction.
	if got[0].ID != 1 || got[0].Relation != "duplicates" {
		t.Errorf("첫 후보 = %+v, want id=1 duplicates", got[0])
	}
	if math.Abs(got[0].Score-1.0) > 1e-9 {
		t.Errorf("duplicates score = %v, want 1.0", got[0].Score)
	}
	if got[1].ID != 2 || got[1].Relation != "contradicts" {
		t.Errorf("둘째 후보 = %+v, want id=2 contradicts", got[1])
	}
	// The contradiction carries its multiset score (0.5151.. = 34/66).
	if math.Abs(got[1].Score-34.0/66.0) > 1e-9 {
		t.Errorf("contradicts score = %v, want %v", got[1].Score, 34.0/66.0)
	}
	if got[1].Topic != echoTestTopic {
		t.Errorf("Topic = %q, want %q", got[1].Topic, echoTestTopic)
	}
	// The scan SQL carries the active filter and the default row cap.
	joined := strings.Join(st.queries, ";")
	if !strings.Contains(joined, "invalid_at IS NULL") {
		t.Errorf("스캔 SQL에 active 필터 부재: %q", joined)
	}
	if !strings.Contains(joined, "LIMIT 5000") {
		t.Errorf("스캔 SQL에 LIMIT 5000 부재: %q", joined)
	}
}

// TestEchoScanTopKAndTieOrder — with more qualified rows than echo_top_k
// (default 5), only the top 5 return; equal scores tie-break by ascending
// id. The SetEchoThresholds seam narrows k (invalid values are ignored).
func TestEchoScanTopKAndTieOrder(t *testing.T) {
	t.Cleanup(func() { SetEchoThresholds(5, 0.3, 0.4) })
	var rows [][]any
	for i := 1; i <= 7; i++ {
		rows = append(rows, []any{int64(i), echoTestTopic, echoDupSummary})
	}
	st := &fakeStore{rows: rows}
	got, err := EchoScan(context.Background(), st, echoTestTopic, echoDupSummary)
	if err != nil {
		t.Fatalf("EchoScan 에러: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("후보 %d건, want 5 (echo_top_k 기본)", len(got))
	}
	for i, c := range got {
		if c.ID != i+1 {
			t.Errorf("동점 %d번째 id = %d, want %d (오름차순)", i, c.ID, i+1)
		}
	}
	// Seam override: topK 2 narrows the result; 0 is ignored (default kept).
	SetEchoThresholds(2, 0.3, 0.4)
	got, err = EchoScan(context.Background(), st, echoTestTopic, echoDupSummary)
	if err != nil {
		t.Fatalf("EchoScan(seam) 에러: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("seam topK=2 후보 %d건, want 2", len(got))
	}
	SetEchoThresholds(0, 0, 0)
	got, err = EchoScan(context.Background(), st, echoTestTopic, echoDupSummary)
	if err != nil {
		t.Fatalf("EchoScan(seam 무효값) 에러: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("무효 seam 값 후보 %d건, want 2 (직전 유효값 유지)", len(got))
	}
}

// TestEchoScanInputCap — the input is combined (topic+" "+summary) and
// truncated to 4096 runes before scoring: a row holding exactly that
// truncation scores a full 1.0 duplicate.
func TestEchoScanInputCap(t *testing.T) {
	topic := strings.Repeat("가", 3000)
	summary := strings.Repeat("나", 3000) // combined 6001 runes -> 4096
	wantSummary := strings.Repeat("나", 1095)
	st := &fakeStore{rows: [][]any{{int64(9), topic, wantSummary}}}
	got, err := EchoScan(context.Background(), st, topic, summary)
	if err != nil {
		t.Fatalf("EchoScan 에러: %v", err)
	}
	if len(got) != 1 || got[0].Relation != "duplicates" {
		t.Fatalf("후보 = %+v, want 1건 duplicates", got)
	}
	if math.Abs(got[0].Score-1.0) > 1e-9 {
		t.Errorf("score = %v, want 1.0 (4096 룬 절단 일치)", got[0].Score)
	}
}

// TestEchoScanQueryErrorPropagates — a scan failure returns (nil, err)
// (the caller omits the echo field quietly).
func TestEchoScanQueryErrorPropagates(t *testing.T) {
	st := &fakeStore{queryErr: errors.New("boom")}
	got, err := EchoScan(context.Background(), st, echoTestTopic, echoDupSummary)
	if err == nil {
		t.Fatalf("오류 미전파 (got=%+v)", got)
	}
	if got != nil {
		t.Errorf("오류 시 후보 = %+v, want nil", got)
	}
}

// echoPoolStore — the minimal real-DB wire.Store for the slow scenario
// (only Query works — the survivedPoolStore convention).
type echoPoolStore struct {
	pool *pgxpool.Pool
	wire.HistoryStubs
}

func (s *echoPoolStore) Migrate(ctx context.Context) error { return nil }
func (s *echoPoolStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return nil, nil
}
func (s *echoPoolStore) ExistsHash(ctx context.Context, h string) (bool, error) {
	return false, nil
}
func (s *echoPoolStore) ExistsProject(ctx context.Context, n string) (bool, error) {
	return false, nil
}
func (s *echoPoolStore) Commit(ctx context.Context, m string) (string, error) {
	return "", nil
}
func (s *echoPoolStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *echoPoolStore) Close()                         { s.pool.Close() }
func (s *echoPoolStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// TestEchoScanRealSlow — slow (SUPERLITE_REAL=1 + FLINT_TEST_DSN): the
// real-DB scan finds the seeded duplicate (identical row -> duplicates)
// and contradiction (same topic, different claim) rows; the unrelated
// seed stays out. The same fixture strings as the fake tests — the
// real-DB row shapes (pgx numerics, strings) must classify identically.
func TestEchoScanRealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real db unavailable: %v", err)
	}
	hashPrefix := "v1170_t1_echo_"
	ins := `INSERT INTO knowledge (topic, summary, tags, confidence, content_hash)
		VALUES ($1, $2, ':a:', 0.9, $3)`
	seeds := []struct {
		id      int
		topic   string
		summary string
	}{
		{0, echoTestTopic, echoDupSummary},
		{0, echoTestTopic, echoContraSummary},
		{0, echoUnrelatedTopic, echoUnrelatedSummary},
	}
	for i, s := range seeds {
		h := hashPrefix + string(rune('a'+i))
		if _, err := pool.Exec(ctx, ins, s.topic, s.summary, h); err != nil {
			t.Skipf("real seeding unavailable: %v", err)
		}
	}
	// cleanup LIFO: delete the seeded rows first, then close the pool.
	t.Cleanup(pool.Close)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM knowledge WHERE content_hash LIKE $1`, hashPrefix+"%"); err != nil {
			t.Errorf("시딩 정리 실패: %v", err)
		}
	})

	got, err := EchoScan(ctx, &echoPoolStore{pool: pool}, echoTestTopic, echoDupSummary)
	if err != nil {
		t.Fatalf("EchoScan(real) 에러: %v", err)
	}
	rel := map[string]string{}
	for _, c := range got {
		rel[c.Topic] = c.Relation
	}
	if rel[echoTestTopic] == "" {
		t.Fatalf("시딩 토픽 후보 부재: %+v", got)
	}
	// Both the duplicate and the contradiction candidates carry the shared
	// topic — at least one duplicates and one contradicts must appear.
	seenDup, seenContra := false, false
	for _, c := range got {
		switch c.Relation {
		case "duplicates":
			seenDup = true
		case "contradicts":
			seenContra = true
		}
	}
	if !seenDup {
		t.Errorf("duplicates 후보 부재: %+v", got)
	}
	if !seenContra {
		t.Errorf("contradicts 후보 부재: %+v", got)
	}
	for _, c := range got {
		if c.Topic == echoUnrelatedTopic {
			t.Errorf("무관 시딩이 후보로 등장: %+v", c)
		}
	}
}
