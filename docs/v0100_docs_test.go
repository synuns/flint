package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0100(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeBadgeIsV0100(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0100(t, p), "release-v0.10.0") {
			t.Errorf("%s 배지가 v0.10.0 아님", p)
		}
	}
}

func TestReadmeMentionsSuggest(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStrV0100(t, p)
		if !strings.Contains(s, "suggestions") {
			t.Errorf("%s read 제안 미언급", p)
		}
		if !strings.Contains(s, "sha256") {
			t.Errorf("%s 백업 무결성 미언급", p)
		}
	}
}
