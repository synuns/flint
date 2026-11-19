// conf_floor_wire_test.go — regression of the wiring that brings the meta conf_floor into
// the write classification (runWrite -> loadThresholds -> Classify(kind, conf, floor) value passing).
// Global injection (SetClassifyThresholds) is abolished — the classification basis is always the
// meta value read by this write (reapplied immediately on update, TUNING.md §2).
package cli

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// uniqTopic — avoids shared real-DB content_hash residue collisions: a unique topic per run
// separates the hashes so the classification never falls into duplicate even with -count=2
// or a residual DB.
func uniqTopic(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("cw-%s-%d", t.Name(), time.Now().UnixNano())
}

// TestWriteConsumesConfFloor — with meta conf_floor=0.6 loaded, confidence 0.7 is
// classified and stored as active (regression: with the fixed 0.8 it would be pending).
func TestWriteConsumesConfFloor(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"conf_floor", "0.6"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+uniqTopic(t)+`","summary":"s","confidence":0.7}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "active" {
		t.Fatalf("conf_floor=0.6에서 conf 0.7 판정=%v, want active", f.inserted)
	}
}

// TestWriteConfFloorLoadFailureKeepsDefault — on a meta lookup failure, the default 0.8
// is unchanged (backward compatibility: a classification-load failure must not change the write result or exit).
func TestWriteConfFloorLoadFailureKeepsDefault(t *testing.T) {
	f := &fakeStore{queryErr: errors.New("meta down")}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+uniqTopic(t)+`","summary":"s","confidence":0.7}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "pending" {
		t.Fatalf("조회 실패 시 기본 0.8 판정=%v, want pending", f.inserted)
	}
}

// TestWriteConfFloorZeroAccepted — conf_floor=0 (the lower bound of the accepted range 0..1)
// is also applied per the parser and TUNING.md (regression: a classification-side boundary rejection would make it pending).
func TestWriteConfFloorZeroAccepted(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"conf_floor", "0"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+uniqTopic(t)+`","summary":"s","confidence":0.5}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "active" {
		t.Fatalf("conf_floor=0에서 conf 0.5 판정=%v, want active", f.inserted)
	}
}
