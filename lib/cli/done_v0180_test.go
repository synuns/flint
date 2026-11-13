// done_v0180_test.go — v0.18.0 R3: done의 회상 경고. 오늘 회상(read) 0회에
// 기록(write)이 임계 이상이면 세션 종료 시 경고 한 줄(stale 경고와 같은
// 패턴 — 임계값 기반·비차단, §13 원칙 4 준수).
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

// recallFake — v1.2.3 T1(R17 A1) 원천 갱신: write는 daily:write:<오늘>
// 플랫 키 int, read는 read_events 당일 COUNT를 반환하는 doneFake(실제
// 생산자 원천 — RecordUsage 플랫 키·read_events 일원화 이후 계약).
func recallFake(t *testing.T, read, write int) *doneFake {
	t.Helper()
	today := time.Now().UTC().Format("2006-01-02")
	d := &doneFake{fakeStore: &fakeStore{}}
	d.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			if len(args) > 0 && fmt.Sprint(args[0]) == "daily:write:"+today {
				return [][]any{{strconv.Itoa(write)}}, nil
			}
			return nil, nil // thresholds 미설정 → 기본값
		case strings.Contains(sql, "FROM read_events"):
			return [][]any{{int64(read)}}, nil
		}
		return nil, nil
	}
	return d
}

// TestDoneRecallWarn — 회상 0·기록 3(기본 임계)이면 recall 경고 줄·마커 기록.
func TestDoneRecallWarn(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir()) // 마커가 실사용자 config에 남지 않게
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

// TestDoneRecallMarkerWritten — 회상 부재 시 recall 마커가 남는다(다음
// SessionStart nudge 소비 연결). v0.18.2: 별도 파일(recall-hint.json).
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
