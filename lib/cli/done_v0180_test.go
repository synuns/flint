// done_v0180_test.go — v0.18.0 R3: done의 회상 경고. 오늘 회상(read) 0회에
// 기록(write)이 임계 이상이면 세션 종료 시 경고 한 줄(stale 경고와 같은
// 패턴 — 임계값 기반·비차단, §13 원칙 4 준수).
package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// recallFake — daily:오늘 키로 read/write JSON을 반환하는 doneFake.
func recallFake(t *testing.T, read, write int) *doneFake {
	t.Helper()
	today := time.Now().UTC().Format("2006-01-02")
	daily := fmt.Sprintf(`{"write":%d,"read":%d}`, write, read)
	d := &doneFake{fakeStore: &fakeStore{}}
	d.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") && len(args) > 0 && fmt.Sprint(args[0]) == "daily:"+today {
			return [][]any{{daily}}, nil
		}
		if strings.Contains(sql, "FROM meta") {
			return nil, nil // thresholds 미설정 → 기본값
		}
		return nil, nil
	}
	return d
}

// TestDoneRecallWarn — 회상 0·기록 3(기본 임계)이면 recall 경고 줄.
func TestDoneRecallWarn(t *testing.T) {
	d := recallFake(t, 0, 3)
	withDoneFake(t, d)
	out, code := mustRun(t, []string{"done", "--session", "s1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고도 종료 불가 — §13)", code)
	}
	if !strings.Contains(out, `"status":"recall"`) || !strings.Contains(out, "회상") {
		t.Fatalf("out=%s, want recall 경고", out)
	}
}

// TestDoneRecallQuiet — 회상 1회면 경고 없음(기록이 임계 이상이어도).
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

// TestDoneRecallBelowThreshold — 기록이 임계(3) 미만이면 경고 없음.
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
