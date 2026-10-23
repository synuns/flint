// read_v08_test.go — T83 read --as-of fast 테스트(fake Store 질의 캡처).
// SUPERLITE_REAL=1이면 ParseAsOfSpec 실구현이 실 dolt_log 질의로 돌아가는
// seam 검증(delegatingFake — 스텑 미지정 연산은 실구현 위임).
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// asofFakeOrReal — 기본 fakeStore, SUPERLITE_REAL=1이면 delegatingFake로
// 실구현을 랩해 반환(생산자 ParseAsOfSpec의 실코드 경로 seam).
func asofFakeOrReal(t *testing.T, f *fakeStore) Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		real, err := newRealStore(context.Background(), dsnFromEnv())
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(real.Close)
		return &delegatingFake{fake: f, real: real}
	}
	return f
}

// asofQueryFn — dolt_log 시각 직전 커밋 1건(해시 고정)·커밋 시각·
// active_knowledge 1행. 캡처는 fakeStore.queries가 담당.
func asofQueryFn(sql string, args []any) ([][]any, error) {
	switch {
	case strings.Contains(sql, "FROM dolt_log WHERE date <= $1"):
		return [][]any{{"he08irluqn3v2m1k9x7c4jw5z6b8d0f"}}, nil
	case strings.Contains(sql, "WHERE commit_hash = $1"):
		return [][]any{{time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}}, nil
	case strings.Contains(sql, "FROM active_knowledge"):
		return [][]any{{int32(3), "토픽", "요약", ":a:", nil, "main", 0.9, "active"}}, nil
	}
	return nil, nil
}

// asofReadSQL — 캡처된 질의 중 active_knowledge 조회 하나를 반환.
func asofReadSQL(f *fakeStore) string {
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") {
			return q
		}
	}
	return ""
}

// TestReadAsOfAddsField — Review Focus: --as-of 7d → 해시 확정 → 질의에
// ` AS OF '<hash>'` 접합(RankOrder·LIMIT 불변) + 출력에 as_of{hash,commit_date}.
func TestReadAsOfAddsField(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: asofQueryFn}
	st := asofFakeOrReal(t, f)
	if code := runRead(ctx, st, []string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	want := "FROM active_knowledge AS OF 'he08irluqn3v2m1k9x7c4jw5z6b8d0f'"
	if got := asofReadSQL(f); !strings.Contains(got, want) {
		t.Errorf("AS OF 접합 실패:\n got %q\nwant 접미 %q", got, want)
	}
	// 기존 질의 불변 — RankOrder 정렬 절·LIMIT 유지.
	if got := asofReadSQL(f); !strings.Contains(got, "ORDER BY (lower(topic)=lower('토픽')) DESC") {
		t.Errorf("RankOrder 절 변형: %q", got)
	}
	if !strings.Contains(asofReadSQL(f), "LIMIT") {
		t.Errorf("LIMIT 절 유실: %q", asofReadSQL(f))
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
	f2 := &fakeStore{queryFn: asofQueryFn}
	if code := runRead(ctx, asofFakeOrReal(t, f2), []string{"토픽"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("미지정 exit = %d, want 0", code)
	}
	if strings.Contains(out2.String(), "as_of") {
		t.Errorf("미지정인데 as_of 출력: %q", out2.String())
	}
}

// TestReadAsOfInvalidExit3 — Review Focus: 무효 스펙("2 days ago")은
// exit 3 한 줄 JSON, active_knowledge 질의 자체를 내지 않는다.
func TestReadAsOfInvalidExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: asofQueryFn}
	code := runRead(ctx, asofFakeOrReal(t, f), []string{"--as-of", "2 days ago", "토픽"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"error":"invalid_as_of"`) {
		t.Errorf("한 줄 JSON invalid_as_of 여부: %q", out.String())
	}
	if asofReadSQL(f) != "" {
		t.Errorf("무효 스펙인데 active_knowledge 질의 실행: %q", asofReadSQL(f))
	}
}

// TestReadAsOfLimitRegression — Review Focus: 기존 인자(--limit 등) 조합 —
// AS OF와 동시 사용 시 필터·LIMIT가 그대로 조립된다.
func TestReadAsOfLimitRegression(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: asofQueryFn}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"--as-of", "7d", "토픽", "--limit", "3"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := asofReadSQL(f)
	if !strings.Contains(got, " AS OF 'he08irluqn3v2m1k9x7c4jw5z6b8d0f'") {
		t.Errorf("AS OF 접미 유실: %q", got)
	}
	if !strings.Contains(got, "LIKE lower($1)") || !strings.Contains(got, "LIMIT $2") {
		t.Errorf("q LIKE·LIMIT 인자 번호 밀림: %q", got)
	}
}

// TestReadAsOfMissingValueExit3 — 값 없는 --as-of(마지막 인자)는 조용한
// 현재 시점 조회가 아니라 무효 스펙 exit 3(잘못된 결과 방지 — status 규칙).
func TestReadAsOfMissingValueExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: asofQueryFn}
	code := runRead(ctx, asofFakeOrReal(t, f), []string{"--as-of"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"invalid_as_of"`) {
		t.Errorf("invalid_as_of 한 줄 JSON 아님: %q", out.String())
	}
	if asofReadSQL(f) != "" {
		t.Errorf("무효 스펙인데 active_knowledge 질의 실행: %q", asofReadSQL(f))
	}
}

// TestReadAsOfViewColumnHint — 뷰-컬럼 미호환 오류(column ... does not
// exist)는 기본 테이블(knowledge) 조회 안내 문구로 랩된다.
func TestReadAsOfViewColumnHint(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return nil, errors.New(`column "confidence" does not exist`)
		}
		return asofQueryFn(sql, args)
	}
	f := &fakeStore{queryFn: fn}
	code := runRead(ctx, asofFakeOrReal(t, f), []string{"--as-of", "7d"}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("오류 주입인데 exit 0")
	}
	if !strings.Contains(out.String(), "active_knowledge") || !strings.Contains(out.String(), "knowledge") {
		t.Errorf("기본 테이블 안내 문구 누락: %q", out.String())
	}
}
