// dispatch_v0132_test.go — 심층 리뷰 N9(mcp)·N11 회귀. N9: dispatchRead은
// 외부 입력 query를 플래그 파싱으로부터 보호하도록 "--" 뒤에 조립한다.
// N11: 실구현 seam(installSimple)은 FLINT_TEST_DSN 설정 시만 실DB를 허용
// (운영 기본 DSN 하드코딩·접속 금지). 신규 파일(기존 테스트 수정 금지).
package mcp

import (
	"context"
	"testing"
)

// TestDispatchReadFlagLikeQueryGuard — N9: query "--json"은 CLI 인자에서
// "--" 뒤에 놓여 검색어로 전달된다(플래그 오인 방지 — 출력 모드 불변은
// lib/cli 파서 회귀 TestReadDashDashTreatsRestAsQuery가 담당).
func TestDispatchReadFlagLikeQueryGuard(t *testing.T) {
	calls, _ := installRun(t, "", 0)
	DispatchTool("flint_read", map[string]any{"query": "--json"})
	wantArgs(t, calls, []string{"read", "--", "--json"})
}

// TestSimpleStoreTestDSNGate — N11: SUPERLITE_REAL=1이라도 FLINT_TEST_DSN
// 미설정이면 installSimple은 실구현이 아니라 fake 경로 — 운영 기본 DSN
// 접속 시도 부재. 설정 시에만 (dsn, true).
func TestSimpleStoreTestDSNGate(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
	if dsn, real := seamRealDSN(); real || dsn != "" {
		t.Fatalf("seamRealDSN = (%q, %v), want (\"\", false) — 미설정 시 fake 폴백", dsn, real)
	}
	fake, real := installSimple(t, [][]any{{1}})
	if real {
		t.Fatal("FLINT_TEST_DSN 미설정인데 실구현 경로 — 운영 DB 접속 시도 위험")
	}
	st, err := NewSimpleStore("postgresql://u:p@h:5432/d")
	if err != nil {
		t.Fatalf("NewSimpleStore(fake): %v", err)
	}
	if _, err := st.Query(context.Background(), `SELECT 1`); err != nil {
		t.Fatalf("Query(fake): %v", err)
	}
	st.Close()
	if len(fake.queries) != 1 || !fake.closed {
		t.Fatalf("fake 기록 queries=%d closed=%v, want 1/true", len(fake.queries), fake.closed)
	}

	const want = "postgresql://flint:t@h:5434/flint_ci"
	t.Setenv("FLINT_TEST_DSN", want)
	if dsn, real := seamRealDSN(); !real || dsn != want {
		t.Fatalf("seamRealDSN = (%q, %v), want (%q, true)", dsn, real, want)
	}
}
