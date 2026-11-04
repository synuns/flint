package db

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

// testDSN은 slow 테스트용 DSN을 반환한다. testDSNFromEnv 가드가 거짓이면
// 스킵한다(v0.15.0 — 운영 FLINT_DSN 환경에서 slow 테스트가 운영 DB에
// 실행되는 v0.9.3 격리 사각 차단).
// (플랜 표기 TestDSN — go test가 Test* 이름에 func(t *testing.T)를 강제하므로 비내보내기로 둔다.)
func testDSN(tb testing.TB) string {
	dsn, ok := testDSNFromEnv(os.Getenv("FLINT_TEST_DSN"), os.Getenv("FLINT_DSN"))
	if !ok {
		tb.Skip("no test DSN: set FLINT_TEST_DSN, or FLINT_DSN to a *test* database DSN")
	}
	return dsn
}

// testDSNFromEnv — slow 테스트 DSN 결정(가드 순수 함수 — 단위 테스트 대상).
// FLINT_TEST_DSN 비빈이면 그 값을 그대로 쓴다. 폴백 FLINT_DSN은 운영 DSN일
// 수 있어 DB명(url.Path)에 "test" 부문자열이 없으면 거짓을 돌려 스킵시킨다
// — 잘못된 환경에서 조용한 스킵이 운영 DB 오염보다 안전하다. 파싱 불가
// DSN도 거짓(스킵) 처리한다.
func testDSNFromEnv(testDsn, flintDsn string) (dsn string, ok bool) {
	if testDsn != "" {
		return testDsn, true
	}
	if flintDsn == "" {
		return "", false
	}
	u, err := url.Parse(flintDsn)
	if err != nil {
		return "", false
	}
	if !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		return "", false
	}
	return flintDsn, true
}

// TestDSNReturnsEnv — FLINT_TEST_DSN 설정 시 해당 값을 그대로 반환한다.
func TestDSNReturnsEnv(t *testing.T) {
	want := "postgresql://flint:flint-dev@localhost:5435/flint-test"
	t.Setenv("FLINT_TEST_DSN", want)
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5433/flint")
	if got := testDSN(t); got != want {
		t.Fatalf("testDSN = %q, want %q", got, want)
	}
}

// TestDSNFromEnvGuard — 폴백 FLINT_DSN 가드 단위 테스트(v0.15.0 결함 회귀):
// 운영 DB명(flint, "test" 부문자열 없음)은 스킵, 테스트 DB명은 통과.
func TestDSNFromEnvGuard(t *testing.T) {
	cases := []struct {
		name              string
		testDsn, flintDsn string
		wantDsn           string
		wantOK            bool
	}{
		{"test 우선 — 운영 FLINT_DSN 무시", "postgresql://f:f@h:5434/flint_test", "postgresql://flint:flint-dev@localhost:5433/flint", "postgresql://f:f@h:5434/flint_test", true},
		{"폴백 — DB명에 test 포함", "", "postgresql://flint:flint-dev@localhost:5434/flint_test", "postgresql://flint:flint-dev@localhost:5434/flint_test", true},
		{"폴백 — 운영 DB명은 스킵", "", "postgresql://flint:flint-dev@localhost:5433/flint", "", false},
		{"폴백 — 파싱 불가 DSN은 스킵", "", "://bad dsn", "", false},
		{"둘 다 비면 스킵", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := testDSNFromEnv(tc.testDsn, tc.flintDsn)
			if ok != tc.wantOK || got != tc.wantDsn {
				t.Errorf("testDSNFromEnv(%q, %q) = (%q, %v), want (%q, %v)",
					tc.testDsn, tc.flintDsn, got, ok, tc.wantDsn, tc.wantOK)
			}
		})
	}
}
