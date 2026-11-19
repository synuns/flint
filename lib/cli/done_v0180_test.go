// done_v0180_test.go — done's recall warning. If today's recalls (read) are 0 while
// writes are at or above the threshold, one warning line appears at session close (the same
// pattern as the stale warning — threshold-based, non-blocking, per §13 principle 4).
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recallFake — source updates: a doneFake where write returns the flat key
// daily:write:<today> as int and read returns today's read_events COUNT (the real
// producer sources — the contract after the RecordUsage flat-key and read_events unification).
func recallFake(t *testing.T, read, write int) *doneFake {
	t.Helper()
	today := time.Now().UTC().Format("2006-01-02")
	// stubbed=true — the stale tally is also pinned to fake zero.
	// If unspecified, in real-DB mode StaleSessionRuns delegates over shared-DB residue, so the
	// stale warning crowds out the recall warning and idempotence breaks (the determination source is queryFn).
	d := &doneFake{fakeStore: &fakeStore{}, stubbed: true}
	d.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			if len(args) > 0 && fmt.Sprint(args[0]) == "daily:write:"+today {
				return [][]any{{strconv.Itoa(write)}}, nil
			}
			return nil, nil // thresholds unset -> defaults
		case strings.Contains(sql, "FROM read_events"):
			return [][]any{{int64(read)}}, nil
		}
		return nil, nil
	}
	return d
}

// TestDoneRecallWarn — recalls 0 + writes 3 (the default threshold) -> a recall warning line and a marker written.
func TestDoneRecallWarn(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir()) // so the marker does not land in the real user's config
	d := recallFake(t, 0, 3)
	withDoneFake(t, d)
	out, code := mustRun(t, []string{"done", "--session", "s1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고도 종료 불가 — §13)", code)
	}
	if !strings.Contains(out, `"status":"recall"`) || !strings.Contains(out, "recalls") {
		t.Fatalf("out=%s, want recall 경고", out)
	}
}

// TestDoneRecallQuiet — 1 recall means no warning (even if writes are at or above the threshold).
func TestDoneRecallQuiet(t *testing.T) {
	d := recallFake(t, 1, 10)
	withDoneFake(t, d)
	out, code := mustRun(t, []string{"done", "--session", "s1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if strings.TrimSpace(out) != `{"status":"ok"}` {
		t.Fatalf("out=%s, want ok(회상 있음 — 경고 없음)", out)
	}
}

// TestDoneRecallBelowThreshold — writes below the threshold (3) mean no warning.
func TestDoneRecallBelowThreshold(t *testing.T) {
	d := recallFake(t, 0, 2)
	withDoneFake(t, d)
	out, code := mustRun(t, []string{"done", "--session", "s1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if strings.TrimSpace(out) != `{"status":"ok"}` {
		t.Fatalf("out=%s, want ok(임계 미만)", out)
	}
}

// TestDoneRecallMarkerWritten — with no recalls, a recall marker remains (connecting to the next
// SessionStart nudge consumption). A separate file (recall-hint.json).
func TestDoneRecallMarkerWritten(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	d := recallFake(t, 0, 3)
	withDoneFake(t, d)
	if _, code := mustRun(t, []string{"done", "--session", "sm"}, ""); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "recall-hint.json")); err != nil {
		t.Fatalf("recall 마커 미기록: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pending-distill.json")); !os.IsNotExist(err) {
		t.Fatalf("증류 마커 파일 오염(분리 계약 위반): %v", err)
	}
}
