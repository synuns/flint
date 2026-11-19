package store

import (
	"regexp"
	"testing"
)

// TestHashStable — the same input twice gives the same hash, 64 hex chars.
func TestHashStable(t *testing.T) {
	h1 := ContentHash(`{"a":1}`)
	h2 := ContentHash(`{"a":1}`)
	if h1 != h2 {
		t.Fatalf("ContentHash 동일 입력에 다른 해시: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("ContentHash 길이 = %d, want 64", len(h1))
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h1) {
		t.Errorf("ContentHash가 sha256 hex가 아님: %s", h1)
	}
}

// TestHashDistinct — different inputs give different hashes.
func TestHashDistinct(t *testing.T) {
	if ContentHash(`{"a":1}`) == ContentHash(`{"a":2}`) {
		t.Error("다른 입력에 동일 해시")
	}
}

// TestHashPipelinePath — the CanonicalJSON -> ContentHash pipeline path.
func TestHashPipelinePath(t *testing.T) {
	n := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"tags": []any{"b", "a"}})
	h := ContentHash(HashCanonical(n))
	if len(h) != 64 {
		t.Errorf("HashCanonical→ContentHash 경로 결과 길이 = %d, want 64", len(h))
	}
}

// TestHashStableAcrossRuns — normalizing the same payload twice yields different
// valid_at (now) values, but the hash must be identical (seeding idempotence, a duplicate-verdict regression).
func TestHashStableAcrossRuns(t *testing.T) {
	payload := func() map[string]any {
		return map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
			"tags": []any{"b", "a"}}
	}
	h1 := ContentHash(HashCanonical(Normalize("knowledge", payload())))
	h2 := ContentHash(HashCanonical(Normalize("knowledge", payload())))
	if h1 != h2 {
		t.Fatalf("동일 payload 재전송 해시 불일치: %s vs %s", h1, h2)
	}
}

// TestHashIgnoresVolatileFields — even with different valid_at and ts values, the hash must be identical.
func TestHashIgnoresVolatileFields(t *testing.T) {
	base := func(validAt, ts string) map[string]any {
		return map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
			"valid_at": validAt, "ts": ts}
	}
	h1 := ContentHash(HashCanonical(base("2026-10-21T01:00:00Z", "2026-10-21T01:00:00Z")))
	h2 := ContentHash(HashCanonical(base("2027-01-01T00:00:00Z", "2027-01-01T00:00:00Z")))
	if h1 != h2 {
		t.Errorf("휘발성 필드가 해시에 유입됨: %s vs %s", h1, h2)
	}
}

// TestHashDistinctOnContent — different content must give different hashes (preventing over-exclusion).
func TestHashDistinctOnContent(t *testing.T) {
	a := ContentHash(HashCanonical(map[string]any{"topic": "x", "summary": "y", "confidence": 0.9}))
	b := ContentHash(HashCanonical(map[string]any{"topic": "z", "summary": "y", "confidence": 0.9}))
	if a == b {
		t.Error("내용이 다른데 해시 동일")
	}
}
