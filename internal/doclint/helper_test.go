package doclint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func docPath(path string) string {
	cands := []string{
		path,
		filepath.Join("../../docs", path),
		filepath.Join("../..", strings.TrimPrefix(path, "../")),
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return path
}

func docStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(docPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
