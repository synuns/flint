package docs

import (
	"os"
	"strings"
	"testing"
)

func readmeV097(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeBadgeIsV097(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(readmeV097(t, p), "release-v0.12.0") {
			t.Errorf("%s 배지가 v0.9.7 아님", p)
		}
	}
}

// TestReadmePortIs5434 — ADR-0010: README의 DSN·포트 안내가 5434로 전환됐는지
// (v0.9.7 리뷰 — EN/KO 각 4곳 5433 잔존 발견의 회귀 방지).
func TestReadmePortIs5434(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := readmeV097(t, p)
		if strings.Contains(s, "5433") {
			t.Errorf("%s에 구 포트 5433 잔존(ADR-0010 위반)", p)
		}
		if !strings.Contains(s, "localhost:5434") {
			t.Errorf("%s에 신 포트 5434 DSN 없음", p)
		}
	}
}
