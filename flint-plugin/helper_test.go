package flintplugin

import (
	"encoding/json"
	"os"
	"testing"
)

// readDoc — flint-plugin 패키지 공유 JSON 로드 헬퍼(v0.16.2 통합).
func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s 파싱: %v", path, err)
	}
	return m
}
