package regression

import (
	"os"
	"testing"
)

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
