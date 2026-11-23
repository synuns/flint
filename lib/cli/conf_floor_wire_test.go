package cli

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func uniqTopic(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("cw-%s-%d", t.Name(), time.Now().UnixNano())
}

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
