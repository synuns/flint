package docs

import (
	"os"
	"testing"
)

// docStr — docs 패키지 공유 문서 로드 헬퍼(v0.16.2 통합).
func docStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
