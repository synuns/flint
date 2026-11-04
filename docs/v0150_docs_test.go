package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0150(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0150(t *testing.T) {
	if !strings.Contains(docStrV0150(t, "PLAN.md"), "v0.15.0") {
		t.Error("PLAN.md v0.15.0 마일스톤 누락")
	}
}

func TestTuningRecordsWindowed(t *testing.T) {
	s := docStrV0150(t, "TUNING.md")
	if !strings.Contains(s, "read_zero_near") || !strings.Contains(s, "30") {
		t.Error("TUNING.md 기간화 지표·임계(30) 미기록")
	}
}

func TestReadmeExitTable(t *testing.T) {
	cases := []struct{ path, expr string }{
		{"../README.md", "invalid `read` arguments"},
		{"../README.ko.md", "read 인자 규칙 위반"},
	}
	for _, c := range cases {
		s := docStrV0150(t, c.path)
		if !strings.Contains(s, "exit 3") || !strings.Contains(s, c.expr) {
			t.Errorf("%s exit 표(exit 3·%s) 누락", c.path, c.expr)
		}
	}
}

// TestReadmeExit3TrailingWording — exit 3의 trailing은 값 없는 trailing
// 플래그(F1/N10)다. read는 위치 인자를 전부 질의로 join하므로 "질의 뒤
// 잔여 텍스트"는 exit 3이 아니라는 오표현을 방어한다.
func TestReadmeExit3TrailingWording(t *testing.T) {
	cases := []struct{ path, want, stale string }{
		{"../README.md", "valueless trailing flags", "trailing text after the query"},
		{"../README.ko.md", "값 없는 trailing 플래그", "질의 뒤 잔여 텍스트"},
	}
	for _, c := range cases {
		s := docStrV0150(t, c.path)
		if !strings.Contains(s, c.want) || strings.Contains(s, c.stale) {
			t.Errorf("%s exit 3 문안: %q 포함·%q 부재여야 함", c.path, c.want, c.stale)
		}
	}
}
