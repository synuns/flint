package store

import (
	"regexp"
	"testing"
)

// TestHashStable — 동일 입력 2회 동일 해시, hex 64자.
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

// TestHashDistinct — 다른 입력은 다른 해시.
func TestHashDistinct(t *testing.T) {
	if ContentHash(`{"a":1}`) == ContentHash(`{"a":2}`) {
		t.Error("다른 입력에 동일 해시")
	}
}

// TestHashPipelinePath — CanonicalJSON → ContentHash 파이프라인 경로(T4→T5 교차점).
func TestHashPipelinePath(t *testing.T) {
	n := Normalize("knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9,
		"tags": []any{"b", "a"}})
	h := ContentHash(HashCanonical(n))
	if len(h) != 64 {
		t.Errorf("HashCanonical→ContentHash 경로 결과 길이 = %d, want 64", len(h))
	}
}

// TestHashStableAcrossRuns — 동일 payload 2회 Normalize는 서로 다른 valid_at(now)를
// 갖지만 해시는 동일해야 한다(시딩 멱등, duplicate 판정 회귀).
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

// TestHashIgnoresVolatileFields — valid_at·ts 값이 달라도 해시는 동일해야 한다.
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

// TestHashDistinctOnContent — 내용이 다르면 해시도 달라야 한다(제외 과잉 방지).
func TestHashDistinctOnContent(t *testing.T) {
	a := ContentHash(HashCanonical(map[string]any{"topic": "x", "summary": "y", "confidence": 0.9}))
	b := ContentHash(HashCanonical(map[string]any{"topic": "z", "summary": "y", "confidence": 0.9}))
	if a == b {
		t.Error("내용이 다른데 해시 동일")
	}
}
