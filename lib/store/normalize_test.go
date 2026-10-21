package store

import (
	"testing"
	"time"
)

// TestNormalizeTagsSorted — 태그는 정렬 후 ":a:b:" 단일 문자열이어야 한다(해시 안정).
func TestNormalizeTagsSorted(t *testing.T) {
	in := map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"tags": []any{"b", "a"}}
	got := Normalize("knowledge", in)
	if got["tags"] != ":a:b:" {
		t.Errorf("Normalize tags = %v, want :a:b:", got["tags"])
	}
}

// TestNormalizeDefaults — 기본값 3종: domain main, origin agent, valid_at now.
func TestNormalizeDefaults(t *testing.T) {
	got := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9})
	if got["domain"] != "main" {
		t.Errorf("domain = %v, want main", got["domain"])
	}
	if got["origin"] != "agent" {
		t.Errorf("origin = %v, want agent", got["origin"])
	}
	va, ok := got["valid_at"].(string)
	if !ok || va == "" {
		t.Fatalf("valid_at 기본값 미설정: %v", got["valid_at"])
	}
	if _, err := time.Parse(time.RFC3339, va); err != nil {
		t.Errorf("valid_at 기본값이 RFC3339 UTC가 아님: %q", va)
	}
}

// TestNormalizeTimestampsUTC — 명시된 타임스탬프는 UTC로 정규화.
func TestNormalizeTimestampsUTC(t *testing.T) {
	got := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"valid_at": "2026-10-21T10:00:00+09:00"})
	if got["valid_at"] != "2026-10-21T01:00:00Z" {
		t.Errorf("valid_at UTC 정규화 = %v", got["valid_at"])
	}
}

// TestNormalizeDoesNotMutateInput — 원본 맵은 변경하지 않는다.
func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"tags": []any{"b", "a"}}
	Normalize("knowledge", in)
	if _, mutated := in["domain"]; mutated {
		t.Error("Normalize가 입력 맵을 변경함")
	}
}

// TestCanonicalJSONKeyOrder — 키 정렬 직렬화.
func TestCanonicalJSONKeyOrder(t *testing.T) {
	got := CanonicalJSON(map[string]any{"b": 1, "a": 2})
	want := `{"a":2,"b":1}`
	if got != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

// TestCanonicalJSONStable — 직렬화는 호출마다 동일해야 한다(해시 안정 선행 조건).
func TestCanonicalJSONStable(t *testing.T) {
	p := map[string]any{"topic": "x", "tags": []any{"b", "a"}, "n": 1.5}
	if CanonicalJSON(p) != CanonicalJSON(p) {
		t.Error("CanonicalJSON 비결정적 출력")
	}
}
