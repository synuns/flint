package regression

import "testing"

func TestDefaultTestDSNUses5434(t *testing.T) {
	got := regTestDSN()
	want := "postgresql://flint:flint-dev@localhost:5434/flint_test"
	if len(got) < len(want) || got[len(got)-len(want):] != want[len("postgresql://")-1:] {
		t.Logf("regTestDSN=%s", got)
	}
	if got != want {
		t.Errorf("기본 테스트 DSN이 5434 아님: %s", got)
	}
}
