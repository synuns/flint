package data

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSeedJSONLMatchesDisk(t *testing.T) {
	want, err := os.ReadFile("seed.jsonl")
	if err != nil {
		t.Fatalf("read seed.jsonl: %v", err)
	}
	got := SeedJSONL()
	if string(got) != string(want) {
		t.Fatalf("SeedJSONL이 디스크 seed.jsonl과 불일치 (embed %d bytes, disk %d bytes)", len(got), len(want))
	}
}

func TestSeedJSONLEveryLineJSON(t *testing.T) {
	for i, line := range strings.Split(string(SeedJSONL()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("%d번째 줄 파싱 실패: %v (%q)", i+1, err, line)
		}
		if _, ok := p["kind"].(string); !ok {
			t.Fatalf("%d번째 줄에 kind 문자열 없음: %q", i+1, line)
		}
	}
}
