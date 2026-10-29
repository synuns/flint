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
		if !strings.Contains(readmeV097(t, p), "release-v0.9.7") {
			t.Errorf("%s 배지가 v0.9.7 아님", p)
		}
	}
}
