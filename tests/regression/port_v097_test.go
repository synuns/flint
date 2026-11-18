package regression

import (
	"os"
	"testing"
)

// TestDefaultTestDSNUses5434 — 회귀 의도는 기본(오버라이드 미설정) =
// 5434/flint_test. FLINT_TEST_DSN 환경 오버라이드가 설정된 실행(검증
// 스위트 등)에서는 기본값 단언이 성립하지 않으므로, 그때는 regTestDSN이
// env 값을 그대로 쓰는지 동등 단언 후 리턴한다(스킵 아님 — 오버라이드
// 경로 자체가 이 테스트의 검증 대상이기도 하다). 미설정 실행에서는
// 기존 기본값 단언이 그대로 회귀(기본=5434)를 지킨다.
func TestDefaultTestDSNUses5434(t *testing.T) {
	if env := os.Getenv("FLINT_TEST_DSN"); env != "" {
		if got := regTestDSN(); got != env {
			t.Errorf("FLINT_TEST_DSN 오버라이드 무시: regTestDSN = %q, want %q", got, env)
		}
		return
	}
	got := regTestDSN()
	want := "postgresql://flint:flint-dev@localhost:5434/flint_test"
	if got != want {
		t.Errorf("기본 테스트 DSN이 5434 아님: %s", got)
	}
}
