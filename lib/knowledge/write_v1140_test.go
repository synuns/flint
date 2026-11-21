// write_v1140_test.go — v1.14.0 T2 (plan A): the correction filter hook
// pipes `flint write knowledge --source correction` payloads that carry
// an explicit confidence 0.4. Fast fake-store regression: the explicit
// confidence is respected (not replaced by the correction_confidence
// default) while the "correction" tag still merges — coexisting with the
// B-plan path (v1.11.0: confidence omitted → default 0.7, covered by the
// cli real-DB slow suite).
package knowledge

import (
	"strings"
	"testing"
)

// TestWriteCorrectionExplicitConfidence04 — a --source correction
// knowledge line with an explicit confidence 0.4 commits exit 0 and the
// stored record keeps 0.4 with the merged "correction" tag (the manual
// contract: an explicitly given confidence, however low, is respected).
func TestWriteCorrectionExplicitConfidence04(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1140-t2-hook-correction","summary":"합성 데모 교정 훅 줄","confidence":0.4}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(명시 confidence 0.4 경로); out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립 행 수=%d, want 1; out=%s", len(f.inserted), out)
	}
	if got, ok := f.inserted[0]["confidence"].(float64); !ok || got != 0.4 {
		t.Fatalf("confidence=%v, want 0.4(기본 0.7로 덮으면 안 됨)", f.inserted[0]["confidence"])
	}
	tags, _ := f.inserted[0]["tags"].(string)
	if !strings.Contains(tags, ":correction:") {
		t.Fatalf("tags=%q, want 병합된 \"correction\" 태그(:correction: 포함)", tags)
	}
}

// TestWriteCorrectionDefaultConfidence07Coexists — the same source with
// confidence omitted still gets the correction_confidence default (0.7):
// the two payload shapes (hook explicit 0.4 · B-plan default) coexist on
// one source without one path polluting the other.
func TestWriteCorrectionDefaultConfidence07Coexists(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1140-t2-default-correction","summary":"합성 데모 교정 기본 줄"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if got, ok := f.inserted[0]["confidence"].(float64); !ok || got != 0.7 {
		t.Fatalf("confidence=%v, want 0.7(생략 시 기본)", f.inserted[0]["confidence"])
	}
}
