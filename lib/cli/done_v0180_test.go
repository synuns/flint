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

func recallFake(t *testing.T, read, write int) *doneFake {
	t.Helper()
	today := time.Now().UTC().Format("2006-01-02")

	d := &doneFake{fakeStore: &fakeStore{}, stubbed: true}
	d.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			if len(args) > 0 && fmt.Sprint(args[0]) == "daily:write:"+today {
				return [][]any{{strconv.Itoa(write)}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "FROM read_events"):
			return [][]any{{int64(read)}}, nil
		}
		return nil, nil
	}
	return d
}

func TestDoneRecallWarn(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
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
