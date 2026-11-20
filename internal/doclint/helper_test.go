package doclint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docPath — docs 패키지 시절(docs/ 루트에서 실행)의 상대경로 리터럴을
// internal/doclint 위치에서 해석한다. 경로 표기는 docs 시절 그대로
// 보존하고(콘텐츠 단얱 리터럴과의 혼동 없음), 실제 파일 위치는
// 여기서만 매핑한다.
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

// docStr — 문서 로드 공유 헬퍼(v0.16.2 통합, internal/doclint 이관).
func docStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(docPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
