// counters_v1200_test.go — the curation counters
// (maintain supersede success, expire-pending confirmed row counts, and prune deleted
// rows as daily flat-key increments — not counted in dry-run) and the fired counters
// (the 3 nudge kinds distill, recall, and the sessionhook update notice, plus per-doctor-warn-kind
// fired_doctor_<check> — ok and skip not counted). The counter UPSERT goes through the st.Query path, so it is verified by argument capture.
package ops

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// the one (sql, args) the fakeStore.Query saw.
type capturedQueryV struct {
	sql  string
	args []any
}

// counterCaptureV — the collector for counter UPSERT arguments (via queryFn).
type counterCaptureV struct {
	queries []capturedQueryV
}

// fn — records arguments then answers zero (subtests needing query responses use wrap).
func (c *counterCaptureV) fn(sql string, args []any) ([][]any, error) {
	c.queries = append(c.queries, capturedQueryV{sql, args})
	return nil, nil
}

// wrap — records arguments + injects the subtest's responses.
func (c *counterCaptureV) wrap(answer func(sql string, args []any) ([][]any, error)) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		c.queries = append(c.queries, capturedQueryV{sql, args})
		if answer != nil {
			return answer(sql, args)
		}
		return nil, nil
	}
}

// sum — the increment sum of prefix-matched counter keys (common to daily's 2 args and fired's 1).
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

// TestCurationCounters — the maintain curation paths -> daily counters.
// supersede success 1, expire-pending confirmed row counts, and prune deleted rows (the
// Repo delegation return values). Dry-run performs no updates, so nothing is counted.
func TestCurationCounters(t *testing.T) {
	today := counterToday()

	t.Run("maintain supersede 성공", func(t *testing.T) {
		shortRetry(t)
		cap := &counterCaptureV{}
		f := &fakeMaintStore{fakeStore: &fakeStore{}}
		f.queryFn = cap.wrap(func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "SELECT (SELECT status") {
				return [][]any{{"active", time.Now()}}, nil
			}
			return nil, nil
		})
		out, code := runMaintainArgs(t, f, "--supersede", "5", "--by", "9")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if got := cap.sum("daily:maintain_supersede:" + today); got != 1 {
			t.Errorf("maintain_supersede 증분=%d, want 1", got)
		}
	})

	t.Run("maintain expire-pending 확정 행수", func(t *testing.T) {
		shortRetry(t)
		cap := &counterCaptureV{}
		f := &fakeMaintStore{fakeStore: &fakeStore{}}
		f.queryFn = cap.wrap(func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "status = 'pending'") {
				old := time.Now().Add(-45 * 24 * time.Hour)
				return [][]any{{int64(7), "토픽", "요약", old}, {int64(8), "토픽2", "요약2", old}}, nil
			}
			return nil, nil
		})
		out, code := runMaintainArgs(t, f, "--expire-pending", "30", "--commit")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if got := cap.sum("daily:maintain_expire:" + today); got != 2 {
			t.Errorf("maintain_expire 증분=%d, want 2(확정 행수)", got)
		}
	})

	t.Run("maintain expire-pending dry-run 미계상", func(t *testing.T) {
		cap := &counterCaptureV{}
		f := &fakeMaintStore{fakeStore: &fakeStore{}}
		f.queryFn = cap.wrap(func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "status = 'pending'") {
				return [][]any{{int64(7), "토픽", "요약", time.Now().Add(-45 * 24 * time.Hour)}}, nil
			}
			return nil, nil
		})
		out, code := runMaintainArgs(t, f, "--expire-pending", "30")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if got := cap.sum("daily:maintain_expire:" + today); got != 0 {
			t.Errorf("dry-run maintain_expire 증분=%d, want 0", got)
		}
	})

	t.Run("maintain prune 삭제 행수", func(t *testing.T) {
		shortRetry(t)
		cap := &counterCaptureV{}
		f := &fakeMaintStore{fakeStore: &fakeStore{}}
		f.queryFn = cap.wrap(func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "status = 'invalidated'") {
				return [][]any{{int64(3)}, {int64(4)}}, nil
			}
			return nil, nil
		})
		out, code := runMaintainArgs(t, f, "--prune-invalidated", "10", "--commit")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		// the fakeMaintStore.PruneInvalidated return = 2 rows (convention) — the deleted rows counted.
		if got := cap.sum("daily:maintain_prune_rows:" + today); got != 2 {
			t.Errorf("maintain_prune_rows 증분=%d, want 2", got)
		}
	})
}

// TestFiredCounters — the fired counters. The 3 nudge kinds (distill — the Stop nudge —
// recall — the SessionStart recall notice — and update — the sessionhook update notice)
// plus per-doctor-warn-kind (fired_doctor_<check>). Not fired (no marker, ok verdict) is 0.
func TestFiredCounters(t *testing.T) {
	t.Run("nudge distill 발화", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("FLINT_CONFIG_DIR", dir)
		if err := WriteTypedMarker(MarkerKindDistill, "s-1", "compact"); err != nil {
			t.Fatalf("distill 마커 오류: %v", err)
		}
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		stdin := `{"hook_event_name":"Stop","session_id":"s-1","stop_hook_active":false}`
		if code := RunNudge(context.Background(), f, nil, strings.NewReader(stdin), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), NudgePendingLine) {
			t.Fatalf("out=%q, want distill 넛지", out.String())
		}
		if got := cap.sum("fired_nudge_distill"); got != 1 {
			t.Errorf("fired_nudge_distill 증분=%d, want 1", got)
		}
		if got := cap.sum("fired_nudge_recall"); got != 0 {
			t.Errorf("fired_nudge_recall 증분=%d, want 0", got)
		}
	})

	t.Run("nudge recall 발화", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("FLINT_CONFIG_DIR", dir)
		if err := WriteTypedMarker(MarkerKindRecall, "s-1", NudgeReasonRecall); err != nil {
			t.Fatalf("recall 마커 오류: %v", err)
		}
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		stdin := `{"hook_event_name":"SessionStart","session_id":"s-1"}`
		if code := RunNudge(context.Background(), f, nil, strings.NewReader(stdin), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), NudgeRecallStartLine) {
			t.Fatalf("out=%q, want recall 안내", out.String())
		}
		if got := cap.sum("fired_nudge_recall"); got != 1 {
			t.Errorf("fired_nudge_recall 증분=%d, want 1", got)
		}
		if got := cap.sum("fired_nudge_distill"); got != 0 {
			t.Errorf("fired_nudge_distill 증분=%d, want 0", got)
		}
	})

	t.Run("nudge 미발화 0", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("FLINT_CONFIG_DIR", dir) // no marker
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		if code := RunNudge(context.Background(), f, nil,
			strings.NewReader(`{"hook_event_name":"SessionStart"}`), &out); code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		for _, key := range []string{"fired_nudge_distill", "fired_nudge_recall"} {
			if got := cap.sum(key); got != 0 {
				t.Errorf("%s 증분=%d, want 0(마커 부재)", key, got)
			}
		}
	})

	t.Run("sessionhook 갱신 안내 발화", func(t *testing.T) {
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir()) // isolate the label and cache files
		injectSessionLabelFn(t)                   // hot metering seams wired to no-ops (standalone ops run)
		prev := MaybeUpdateNoticeFn
		MaybeUpdateNoticeFn = func() string { return "[flint] v1.1.0 → v1.2.0 갱신 가능" }
		t.Cleanup(func() { MaybeUpdateNoticeFn = prev })
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		if code := RunSessionStart(context.Background(), f, nil,
			strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), "갱신 가능") {
			t.Fatalf("out=%q, want 갱신 안내 1줄", out.String())
		}
		if got := cap.sum("fired_nudge_update"); got != 1 {
			t.Errorf("fired_nudge_update 증분=%d, want 1", got)
		}
	})

	t.Run("sessionhook 갱신 안내 미발화 0", func(t *testing.T) {
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
		injectSessionLabelFn(t)
		prev := MaybeUpdateNoticeFn
		MaybeUpdateNoticeFn = func() string { return "" }
		t.Cleanup(func() { MaybeUpdateNoticeFn = prev })
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn}
		var out bytes.Buffer
		if code := RunSessionStart(context.Background(), f, nil,
			strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if got := cap.sum("fired_nudge_update"); got != 0 {
			t.Errorf("fired_nudge_update 증분=%d, want 0(무안내)", got)
		}
	})

	t.Run("doctor warn 종류별 발화", func(t *testing.T) {
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn} // meta unanswered -> last_backup unset warn
		out, code := runDoctorArgs(t, f, "--check", "backup-age")
		if code != 0 {
			t.Fatalf("exit=%d, want 0(warn은 0); out=%s", code, out)
		}
		if !strings.Contains(out, `"status":"warn"`) {
			t.Fatalf("out=%q, want warn", out)
		}
		if got := cap.sum("fired_doctor_backup-age"); got != 1 {
			t.Errorf("fired_doctor_backup-age 증분=%d, want 1", got)
		}
	})

	t.Run("doctor ok 미발화 0", func(t *testing.T) {
		cap := &counterCaptureV{}
		f := &fakeStore{queryFn: cap.fn} // Ping succeeds -> db ok
		out, code := runDoctorArgs(t, f, "--check", "db")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if got := cap.sum("fired_doctor_"); got != 0 {
			t.Errorf("fired_doctor_* 증분=%d, want 0(ok 판정)", got)
		}
	})
}
