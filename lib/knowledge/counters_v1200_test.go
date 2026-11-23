package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

type capturedQueryV struct {
	sql  string
	args []any
}

type counterCaptureV struct {
	queries []capturedQueryV
}

func (c *counterCaptureV) fn(sql string, args []any) ([][]any, error) {
	c.queries = append(c.queries, capturedQueryV{sql, args})
	return nil, nil
}

func (c *counterCaptureV) sum(prefix string) int {
	n := 0
	for _, q := range c.queries {
		if len(q.args) == 0 {
			continue
		}
		k, ok := q.args[0].(string)
		if !ok || !strings.HasPrefix(k, prefix) {
			continue
		}
		if len(q.args) > 1 {
			if v, err := strconv.Atoi(fmt.Sprint(q.args[1])); err == nil {
				n += v
			}
			continue
		}
		n++
	}
	return n
}

func counterToday() string { return time.Now().UTC().Format("2006-01-02") }

func TestCurationCounters(t *testing.T) {
	today := counterToday()

	t.Run("approve 승격 성공", func(t *testing.T) {
		cap := &counterCaptureV{}
		f := &approveStoreFake{fakeStore: &fakeStore{queryFn: cap.fn}}
		var out bytes.Buffer
		if code := RunApprove(context.Background(), f, []string{"--id", "7"},
			strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if got := cap.sum("daily:approve_done:" + today); got != 1 {
			t.Errorf("approve_done 증분=%d, want 1", got)
		}
		if got := cap.sum("daily:approve_not_found:" + today); got != 0 {
			t.Errorf("approve_not_found 증분=%d, want 0(미발화)", got)
		}
	})

	t.Run("approve not_found", func(t *testing.T) {
		cap := &counterCaptureV{}
		f := &approveStoreFake{fakeStore: &fakeStore{queryFn: cap.fn}, approveErr: db.ErrNotFound}
		var out bytes.Buffer
		if code := RunApprove(context.Background(), f, []string{"--id", "999"},
			strings.NewReader(""), &out); code != 1 {
			t.Fatalf("exit=%d, want 1; out=%s", code, out.String())
		}
		if got := cap.sum("daily:approve_not_found:" + today); got != 1 {
			t.Errorf("approve_not_found 증분=%d, want 1", got)
		}
		if got := cap.sum("daily:approve_done:" + today); got != 0 {
			t.Errorf("approve_done 증분=%d, want 0(승격 실패 경로)", got)
		}
	})

	t.Run("approve supersede 오류", func(t *testing.T) {
		cap := &counterCaptureV{}

		f := &approveStoreFake{fakeStore: &fakeStore{queryFn: cap.fn}}
		var out bytes.Buffer
		if code := RunApprove(context.Background(), f, []string{"--id", "7", "--supersede", "3"},
			strings.NewReader(""), &out); code != 1 {
			t.Fatalf("exit=%d, want 1; out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), "supersede_error") {
			t.Fatalf("out=%q, want supersede_error 보고", out.String())
		}
		if got := cap.sum("daily:approve_supersede_err:" + today); got != 1 {
			t.Errorf("approve_supersede_err 증분=%d, want 1", got)
		}
		if got := cap.sum("daily:approve_done:" + today); got != 1 {
			t.Errorf("approve_done 증분=%d, want 1(승격 자체는 성공)", got)
		}
	})

	t.Run("import 적립·스킵", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "CLAUDE.md")
		body := "# 규칙\n## 배포\n배포는 main 머지 후 태그로 한다.\n## 테스트\n테스트는 커밋 전 전량 실행한다.\n"
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatalf("픽스처 작성 오류: %v", err)
		}

		cap := &counterCaptureV{}
		st := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		if code := RunImport(context.Background(), st, []string{file, "--commit"},
			strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if got := cap.sum("daily:import_added:" + today); got != 2 {
			t.Errorf("import_added 증분=%d, want 2", got)
		}
		if got := cap.sum("daily:import_skipped:" + today); got != 0 {
			t.Errorf("import_skipped 증분=%d, want 0", got)
		}

		cap2 := &counterCaptureV{}
		st2 := &fakeStore{queryFn: cap2.fn, exists: true}
		var out2 bytes.Buffer
		if code := RunImport(context.Background(), st2, []string{file, "--commit"},
			strings.NewReader(""), &out2); code != 0 {
			t.Fatalf("재임포트 exit=%d, want 0; out=%s", code, out2.String())
		}
		if got := cap2.sum("daily:import_skipped:" + today); got != 2 {
			t.Errorf("재임포트 import_skipped 증분=%d, want 2", got)
		}
		if got := cap2.sum("daily:import_added:" + today); got != 0 {
			t.Errorf("재임포트 import_added 증분=%d, want 0", got)
		}
	})

	t.Run("import dry-run 미계상", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "CLAUDE.md")
		if err := os.WriteFile(file, []byte("# 규칙\n## 배포\n배포는 main 머지 후 태그로 한다.\n"), 0o644); err != nil {
			t.Fatalf("픽스처 작성 오류: %v", err)
		}
		cap := &counterCaptureV{}
		st := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		if code := RunImport(context.Background(), st, []string{file},
			strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if got := cap.sum("daily:import_added:" + today); got != 0 {
			t.Errorf("dry-run import_added 증분=%d, want 0", got)
		}
		if got := cap.sum("daily:import_skipped:" + today); got != 0 {
			t.Errorf("dry-run import_skipped 증분=%d, want 0", got)
		}
	})
}

func TestFiredCounters(t *testing.T) {
	cap := &counterCaptureV{}
	d := &doneFake{fakeStore: &fakeStore{queryFn: cap.fn}, stubbed: true, runs: 3}
	out, code := mustDone(t, d, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"stale"`) {
		t.Fatalf("out=%q, want stale 경고", out)
	}
	if got := cap.sum("fired_done_stale"); got != 1 {
		t.Errorf("fired_done_stale 증분=%d, want 1", got)
	}

	capOK := &counterCaptureV{}
	dOK := &doneFake{fakeStore: &fakeStore{queryFn: capOK.fn}, stubbed: true, runs: 1}
	outOK, codeOK := mustDone(t, dOK, []string{"--session", "claude"})
	if codeOK != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", codeOK, outOK)
	}
	if got := capOK.sum("fired_done_stale"); got != 0 {
		t.Errorf("ok 경로 fired_done_stale 증분=%d, want 0", got)
	}
}
