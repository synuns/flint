package store

import (
	"strings"
	"testing"
	"time"
)

// TestNormalizeTagsSorted — tags must become a single ":a:b:" string after sorting (hash stability).
func TestNormalizeTagsSorted(t *testing.T) {
	in := map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"tags": []any{"b", "a"}}
	got := Normalize("knowledge", in)
	if got["tags"] != ":a:b:" {
		t.Errorf("Normalize tags = %v, want :a:b:", got["tags"])
	}
}

// TestNormalizeDefaults — the 3 defaults: domain main, origin agent, valid_at now.
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

// TestNormalizeTimestampsUTC — explicit timestamps are normalized to UTC.
func TestNormalizeTimestampsUTC(t *testing.T) {
	got := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"valid_at": "2026-10-21T10:00:00+09:00"})
	if got["valid_at"] != "2026-10-21T01:00:00Z" {
		t.Errorf("valid_at UTC 정규화 = %v", got["valid_at"])
	}
}

// TestNormalizeDoesNotMutateInput — the original map is not mutated.
func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"tags": []any{"b", "a"}}
	Normalize("knowledge", in)
	if _, mutated := in["domain"]; mutated {
		t.Error("Normalize가 입력 맵을 변경함")
	}
}

// TestCanonicalJSONKeyOrder — serialized with sorted keys.
func TestCanonicalJSONKeyOrder(t *testing.T) {
	got := CanonicalJSON(map[string]any{"b": 1, "a": 2})
	want := `{"a":2,"b":1}`
	if got != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

// TestCanonicalJSONStable — serialization must be identical across calls (a hash-stability precondition).
func TestCanonicalJSONStable(t *testing.T) {
	p := map[string]any{"topic": "x", "tags": []any{"b", "a"}, "n": 1.5}
	a, b := CanonicalJSON(p), CanonicalJSON(p) // comparing 2 calls (detecting non-determinism)
	if a != b {
		t.Error("CanonicalJSON 비결정적 출력")
	}
}

// TestRelationVocabularyRegistered — the schema v14 (v1.20.0) relation
// vocabulary: exactly the 4 registered values pass ValidRelation
// (case-insensitive — the normalizeVocab convention).
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

// TestRelationVocabularyRejectsUnknown — an unregistered value yields the
// distinct "rejected relation:" class (the colon-token rejection
// precedent — the write path maps this class to exit 3, not a warning).
// supersedes is deliberately rejected: lineage stays in
// knowledge.superseded_by (the dual-source-of-truth avoidance).
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

// TestHashCanonicalExcludesRelations — relations joined the hashVolatile
// exclusion (v1.20.0 schema v14): the same body with·without a relations
// array hashes identically (dedup preservation — links are metadata, not
// content).
func TestHashCanonicalExcludesRelations(t *testing.T) {
	base := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9}
	withRel := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"relations": []any{map[string]any{"to": 2, "relation": "refines"}}}
	if HashCanonical(base) != HashCanonical(withRel) {
		t.Errorf("relations 유무로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(withRel))
	}
}

// TestHashCanonicalExcludesConditions — conditions joined the hashVolatile
// exclusion (v1.22.0 schema v17): the same body with·without a conditions
// string hashes identically (dedup preservation — the recheck tag is
// metadata, not content, the relations precedent).
func TestHashCanonicalExcludesConditions(t *testing.T) {
	base := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9}
	withCond := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"conditions": "when clause A expires"}
	if HashCanonical(base) != HashCanonical(withCond) {
		t.Errorf("conditions 유무로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(withCond))
	}
	// a differing conditions value also keeps the hash — the tag never
	// enters the content identity.
	other := map[string]any{"topic": "t", "summary": "s", "confidence": 0.9,
		"conditions": "different"}
	if HashCanonical(base) != HashCanonical(other) {
		t.Errorf("conditions 값 차이로 해시 변동 — hashVolatile 계약 위반: %q vs %q",
			HashCanonical(base), HashCanonical(other))
	}
}

// TestCheckRelationsEchoTruncation — v1.20.2 (r88): the rejection echo
// caps the echoed value at 200 runes. The write path's 64-byte
// parseLinkSpecs cap keeps values that long unreachable from there, so
// this direct call is what keeps the defense layer verified for other
// CheckRelations callers: an echo must never re-print an unbounded
// input string in full. Multibyte input proves the cap counts runes,
// not bytes.
func TestCheckRelationsEchoTruncation(t *testing.T) {
	const prefix = "rejected relation: "
	long := strings.Repeat("거", 250) // 250 runes, 750 bytes
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
	if len(r) != 201 { // 200 runes + the ellipsis
		t.Fatalf("echo rune 수=%d, want 201 (200 절단+생략부호) — echo=%q…", len(r), string(r[:min(20, len(r))]))
	}
	if !strings.HasSuffix(echo, "…") {
		t.Fatalf("echo 미생략부호 종결: %q 마지막 3rune=%q", echo, string(r[len(r)-3:]))
	}
}
