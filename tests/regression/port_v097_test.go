package regression

import (
	"os"
	"testing"

	"github.com/synuns/flint/internal/testengine"
)

func TestDefaultTestDSNUsesDedicatedEngine(t *testing.T) {
	if env := os.Getenv("FLINT_TEST_DSN"); env != "" {
		if got := regTestDSN(); got != env {
			t.Errorf("FLINT_TEST_DSN 오버라이드 무시: regTestDSN = %q, want %q", got, env)
		}
		return
	}
	if got := regTestDSN(); got != testengine.TestDSNDefault() {
		t.Errorf("기본 테스트 DSN이 전용 엔진(5499) 아님: %s", got)
	}
}
