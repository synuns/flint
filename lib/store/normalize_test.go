package store

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeTagsSorted(t *testing.T) {
	in := map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"tags": []any{"b", "a"}}
	got := Normalize("knowledge", in)
	if got["tags"] != ":a:b:" {
		t.Errorf("Normalize tags = %v, want :a:b:", got["tags"])
	}
}

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

func TestNormalizeTimestampsUTC(t *testing.T) {
	got := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"valid_at": "2026-10-21T10:00:00+09:00"})
	if got["valid_at"] != "2026-10-21T01:00:00Z" {
		t.Errorf("valid_at UTC 정규화 = %v", got["valid_at"])
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"tags": []any{"b", "a"}}
	Normalize("knowledge", in)
	if _, mutated := in["domain"]; mutated {
		t.Error("Normalize가 입력 맵을 변경함")
	}
}

func TestCanonicalJSONKeyOrder(t *testing.T) {
	got := CanonicalJSON(map[string]any{"b": 1, "a": 2})
	want := `{"a":2,"b":1}`
	if got != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONStable(t *testing.T) {
	p := map[string]any{"topic": "x", "tags": []any{"b", "a"}, "n": 1.5}
	a, b := CanonicalJSON(p), CanonicalJSON(p)
	if a != b {
		t.Error("CanonicalJSON 비결정적 출력")
	}
}

func TestRelationVocabularyRegistered(t *testing.T) {
	for _, rel := range []string{"refines", "supports", "contradicts", "duplicates"} {
		if !ValidRelation(rel) {
			t.Errorf("ValidRelation(%q) = false, want true(등록 어휘)", rel)
		}
	}
	if !ValidRelation("Refines") || !ValidRelation(" SUPPORTS ") {
		t.Error("대소문자·공백 정규화 실패 — normalizeVocab 관례 위반")
	}
}

func TestRelationVocabularyRejectsUnknown(t *testing.T) {
	got := CheckRelations([]string{"refines", "supersedes", "blocks"})
	if len(got) != 2 {
		t.Fatalf("CheckRelations = %v, want 2 거부(등록 1통과·미등록 2거부)", got)
	}
	for _, m := range got {
		if !strings.HasPrefix(m, "rejected relation: ") {
			t.Errorf("거부 클래스 아님: %q (want 'rejected relation: ' prefix)", m)
		}
	}
	if !strings.Contains(got[0], "supersedes") {
		t.Errorf("supersedes 거부 메시지 부재: %v", got)
	}
	if len(CheckRelations(nil)) != 0 {
		t.Error("nil 입력은 통과 — 선택 필드 관례 위반")
	}
}

func TestHashCanonicalExcludesRelations(t *testing.T) {
	base := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9}
	withRel := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"relations": []any{map[string]any{"to": 2, "relation": "refines"}}}
	if HashCanonical(base) != HashCanonical(withRel) {
		t.Errorf("relations 유무로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(withRel))
	}
}

func TestHashCanonicalExcludesConditions(t *testing.T) {
	base := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9}
	withCond := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"conditions": "when clause A expires"}
	if HashCanonical(base) != HashCanonical(withCond) {
		t.Errorf("conditions 유무로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(withCond))
	}

	other := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"conditions": "different"}
	if HashCanonical(base) != HashCanonical(other) {
		t.Errorf("conditions 값 차이로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(other))
	}
}

func TestHashCanonicalExcludesV1240Fields(t *testing.T) {
	base := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9}
	withKind := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"knowledge_kind": "preference", "source_path": "demo/notes/a.md"}
	if HashCanonical(base) != HashCanonical(withKind) {
		t.Errorf("knowledge_kind·source_path 부재/존재로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(withKind))
	}

	other := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"knowledge_kind": "fact", "source_path": "demo/notes/other.md"}
	if HashCanonical(withKind) != HashCanonical(other) {
		t.Errorf("knowledge_kind·source_path 값 차이로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(withKind), HashCanonical(other))
	}
}

func TestCheckRelationsEchoTruncation(t *testing.T) {
	const prefix = "rejected relation: "
	long := strings.Repeat("거", 250)
	got := CheckRelations([]string{long})
	if len(got) != 1 {
		t.Fatalf("CheckRelations = %d messages, want 1", len(got))
	}
	msg := got[0]
	if !strings.HasPrefix(msg, prefix) {
		t.Fatalf("거부 클래스 아님: %q", msg)
	}
	echo := strings.TrimPrefix(msg, prefix)
	if i := strings.Index(echo, " (relations vocabulary: "); i >= 0 {
		echo = echo[:i]
	}
	r := []rune(echo)
	if len(r) != 201 {
		t.Fatalf("echo rune 수=%d, want 201 (200 절단+생략부호) — echo=%q…", len(r), string(r[:min(20, len(r))]))
	}
	if !strings.HasSuffix(echo, "…") {
		t.Fatalf("echo 미생략부호 종결: %q 마지막 3rune=%q", echo, string(r[len(r)-3:]))
	}
}
