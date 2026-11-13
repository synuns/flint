// counters_v1200_test.go — v1.2.0 T4 Review Focus: 검수(curation) 카운터
// (maintain supersede 성공·expire-pending 확정 행수·prune 삭제 행수 daily
// 플랫 키 증분 — dry-run 미계상)·발화(fired) 카운터(nudge distill·recall·
// sessionhook 갱신 안내 update 3종·doctor warn 종류별 fired_doctor_<check>
// — ok·skip은 미계상). 카운터 UPSERT는 st.Query 경로라 인자 캡처로 검증.
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

// capturedQueryV — fakeStore.Query가 본 (sql, args) 1건.
type capturedQueryV struct {
	sql  string
	args []any
}

// counterCaptureV — 카운터 UPSERT 인자 수집기(queryFn 경유).
type counterCaptureV struct {
	queries []capturedQueryV
}

// fn — 인자 기록 후 영(零) 응답(조회 응답이 필요한 서브테스트는 wrap).
func (c *counterCaptureV) fn(sql string, args []any) ([][]any, error) {
	c.queries = append(c.queries, capturedQueryV{sql, args})
	return nil, nil
}

// wrap — 인자 기록 + 서브테스트 응답 주입.
func (c *counterCaptureV) wrap(answer func(sql string, args []any) ([][]any, error)) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		c.queries = append(c.queries, capturedQueryV{sql, args})
		if answer != nil {
			return answer(sql, args)
		}
		return nil, nil
	}
}

// sum — 접두 매칭 카운터 키의 증분 합(daily 2인자·fired 1인자 공통).
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

// TestCurationCounters — Review Focus: maintain 검수 경로 → daily 카운터.
// supersede 성공 1·expire-pending 확정 행수·prune 삭제 행수(Repo 위임
// 반환값). dry-run은 갱신이 없으므로 미계상.
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
		// fakeMaintStore.PruneInvalidated 반환 = 2행(관례) — 삭제 행수 계상.
		if got := cap.sum("daily:maintain_prune_rows:" + today); got != 2 {
			t.Errorf("maintain_prune_rows 증분=%d, want 2", got)
		}
	})
}

// TestFiredCounters — Review Focus: 발화 카운터. nudge distill(Stop 넛지)·
// recall(SessionStart 회상 안내)·update(sessionhook 갱신 안내) 3종·doctor
// warn 종류별(fired_doctor_<check>). 미발화(마커 부재·ok 판정)는 0.
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
		t.Setenv("FLINT_CONFIG_DIR", dir) // 마커 없음
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
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir()) // 라벨·캐시 파일 격리
		injectSessionLabelFn(t)                   // hot 계측 seam no-op 배선(ops 단독 실행)
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
		f := &fakeStore{queryFn: cap.fn} // meta 무응답 → last_backup 미설정 warn
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
		f := &fakeStore{queryFn: cap.fn} // Ping 성공 → db ok
		out, code := runDoctorArgs(t, f, "--check", "db")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if got := cap.sum("fired_doctor_"); got != 0 {
			t.Errorf("fired_doctor_* 증분=%d, want 0(ok 판정)", got)
		}
	})
}
