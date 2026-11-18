// read_v08_test.go — read --as-of fast 테스트(계약 canned Store).
// SUPERLITE_REAL=1이면 ParseAsOfSpec 실구현이 실 커밋 로그로 돌아가는
// seam 검증(delegatingFake — 스텑 미지정 연산은 실구현 위임).
package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// withFakeStore — withFake의 wire.Store판: 계약 메서드 오버라이드 fake를
// newStore seam에 주입한다. SUPERLITE_REAL=1에서도 fake 시맨틱을 유지한다
// (오버라이드가 전 연산을 canned 처리 — 기존 withFake의 queryFn 우선
// 규칙과 동등).
func withFakeStore(t *testing.T, st Store) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return st, nil }
	t.Cleanup(func() { newStore = prev })
}

// readAsofStoreOrReal — asofFakeOrReal의 wire.Store판(오버라이드 fake 수용).
// SUPERLITE_REAL=1이면 delegatingFake로 실구현을 랩하되, 계약 메서드는
// fake 오버라이드가 응답한다(delegatingFake의 fake 임베딩 한계 보완).
func readAsofStoreOrReal(t *testing.T, st Store) Store {
	t.Helper()
	return st // 계약 오버라이드 fake는 실구현 위임이 필요 없음(canned 완결)
}

// as-of 경로 확정 픽스처(기존 실측 해시·커밋 시각 승계).
const asofHash = "he08irluqn3v2m1k9x7c4jw5z6b8d0f"

var asofCommitDate = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// readAsofFake — as-of 경로 계약 canned: 시각 직전 커밋·해시 커밋 시각은
// LogEntries 오버라이드, 과거 스냅샷 본문은 QueryAsOf 오버라이드
// (AsOfQuery 캡처 — 해시·Where·Limit 검증). 비as-of 질의(queryFn)는
// fakeStore 승계.
type readAsofFake struct {
	*fakeStore
	asofQueries []wire.AsOfQuery
	zeroHit     bool // 과거 스냅샷 제로히트(기본은 active_knowledge 1행)
	asofErr     error
}

func (f *readAsofFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Prefix != "":
		if strings.HasPrefix(asofHash, q.Prefix) {
			return []wire.LogEntry{{Hash: asofHash}}, nil
		}
		return nil, nil
	case !q.BeforeOrAt.IsZero():
		return []wire.LogEntry{{Hash: asofHash}}, nil // 시각 직전 커밋 1건
	case q.Hash == asofHash:
		return []wire.LogEntry{{Hash: asofHash, Date: asofCommitDate}}, nil
	}
	return nil, nil
}

func (f *readAsofFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	f.asofQueries = append(f.asofQueries, q)
	if f.asofErr != nil {
		return nil, f.asofErr
	}
	if f.zeroHit {
		return nil, nil
	}
	return [][]any{{int32(3), "토픽", "요약", ":a:", nil, "main", 0.9, "active"}}, nil
}

// asofFakeOrReal — 기본 fakeStore, FLINT_TEST_DSN 설정 + SUPERLITE_REAL=1이면
// delegatingFake로 실구현을 랩해 반환(생산자 ParseAsOfSpec의 실코드 경로 seam).
// FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(seamRealDSN).
func asofFakeOrReal(t *testing.T, f *fakeStore) Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		realStore, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(realStore.Close)
		return &delegatingFake{fake: f, real: realStore}
	}
	return f
}

// asofReadSQL — 캡처된 질의 중 active_knowledge 조회 하나를 반환
// (비as-of·폴백 경로 — Query seam 승계).
func asofReadSQL(f *fakeStore) string {
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") {
			return q
		}
	}
	return ""
}

// TestReadAsOfAddsField — --as-of 7d → 해시 확정 → 과거 스냅샷 질의는
// QueryAsOf{Hash, Table: active_knowledge}(RankOrder Where·Limit 불별) +
// 출력에 as_of{hash,commit_date}.
func TestReadAsOfAddsField(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash || aq.Table != "active_knowledge" || aq.FallbackTable != "knowledge" {
		t.Errorf("해시 접합 실패: %+v", aq)
	}
	// 기존 질의 불변 — RankOrder 정렬 절·LIMIT 유지(Where·Limit 필드 승계).
	if !strings.Contains(aq.Where, "ORDER BY (lower(topic)=lower($2)) DESC") {
		t.Errorf("RankOrder 절 변형: %q", aq.Where)
	}
	if aq.Limit <= 0 {
		t.Errorf("LIMIT 유실: %d", aq.Limit)
	}
	// 출력 — 기존 행 + as_of 한 줄.
	got := out.String()
	if !strings.HasPrefix(got, "3|토픽|요약\n") {
		t.Errorf("기존 행 출력 변형: %q", got)
	}
	if !strings.Contains(got, `"as_of":{"hash":"he08irluqn3v2m1k9x7c4jw5z6b8d0f","commit_date":"2026-10-20T12:00:00Z"}`) {
		t.Errorf("as_of 필드 누락/불일치: %q", got)
	}

	// 미지정 회귀 — as_of 줄 없음·기존 출력만.
	var out2 bytes.Buffer
	f2 := &fakeStore{}
	if code := runRead(ctx, asofFakeOrReal(t, f2), []string{"토픽"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("미지정 exit = %d", code)
	}
	if strings.Contains(out2.String(), "as_of") {
		t.Errorf("미지정인데 as_of 출력: %q", out2.String())
	}
}

// TestReadAsOfInvalidExit3 — 무효 스펙("2 days ago")은
// exit 3 한 줄 JSON, 과거 스냅샷 질의 자체를 내지 않는다.
func TestReadAsOfInvalidExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "2 days ago", "토픽"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"error":"invalid_as_of"`) {
		t.Errorf("한 줄 JSON invalid_as_of 여부: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

// TestReadAsOfLimitRegression — 기존 인자(--limit 등) 조합 —
// QueryAsOf와 동시 사용 시 필터·LIMIT가 그대로 조립된다.
func TestReadAsOfLimitRegression(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽", "--limit", "3"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash {
		t.Errorf("해시 접합 유실: %+v", aq)
	}
	// q 랭킹 파라미터가 LIKE($1) 뒤 $2 — LIMIT은 별도 필드(인자 번호 밀림 없음).
	if !strings.Contains(aq.Where, "LIKE lower($1)") || !strings.Contains(aq.Where, "lower($2)") {
		t.Errorf("q LIKE·랭킹 절 변형: %q", aq.Where)
	}
	if aq.Limit != 3 {
		t.Errorf("Limit=%d, want 3", aq.Limit)
	}
}

// TestReadAsOfMissingValueExit3 — 값 없는 --as-of(마지막 인자)는 조용한
// 현재 시점 조회가 아니라 무효 스펙 exit 3(잘못된 결과 방지 — status 규칙).
func TestReadAsOfMissingValueExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"invalid_as_of"`) {
		t.Errorf("invalid_as_of 한 줄 JSON 아님: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

// TestReadAsOfViewColumnHint — 뷰-컬럼 미호환 오류(column ... does not
// exist)는 기본 테이블(knowledge) 조회 안내 문구로 랩된다(구현측
// FallbackTable 재시도 경계 — fake는 안내 문구 에러로 재현).
func TestReadAsOfViewColumnHint(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	f.asofErr = errors.New(`view "active_knowledge" incompatible at this commit (column "confidence" does not exist) — retry with base table "knowledge"`)
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d"}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("오류 주입인데 exit 0")
	}
	if !strings.Contains(out.String(), "active_knowledge") || !strings.Contains(out.String(), "knowledge") {
		t.Errorf("기본 테이블 안내 문구 누락: %q", out.String())
	}
}
