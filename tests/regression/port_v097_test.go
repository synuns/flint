package regression

import "testing"

func TestDefaultTestDSNUses5434(t *testing.T) {
	got := regTestDSN()
	want := "postgresql://flint:flint-dev@localhost:5434/flint_test"
	if got != want {
		t.Errorf("기본 테스트 DSN이 5434 아님: %s", got)
	}
}
