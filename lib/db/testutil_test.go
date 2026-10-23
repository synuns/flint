package db

import (
	"os"
	"testing"
)

// testDSN은 slow 테스트용 DSN을 반환한다.
// FLINT_DSN이 비빈이면 그 값을 그대로 반환하고, 빈이면 스킵한다.
// 기존의 5433 기본값 폴백은 폐지 — 지역 DB 없는 환경에서 실패 대신 스킵.
// (플랜 표기 TestDSN — go test가 Test* 이름에 func(t *testing.T)를 강제하므로 비내보내기로 둔다.)
func testDSN(tb testing.TB) string {
	dsn := os.Getenv("FLINT_DSN")
	if dsn == "" {
		tb.Skip("FLINT_DSN not set; set it to a test database DSN")
	}
	return dsn
}

// TestDSNReturnsEnv — FLINT_DSN 설정 시 해당 값을 그대로 반환한다.
func TestDSNReturnsEnv(t *testing.T) {
	want := "postgresql://flint:flint-dev@localhost:5435/flint-test"
	t.Setenv("FLINT_DSN", want)
	if got := testDSN(t); got != want {
		t.Fatalf("testDSN = %q, want %q", got, want)
	}
}
