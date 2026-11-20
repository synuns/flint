// care_test.go — behavioral verification: the maintain, nudge,
// session-start, and session-end registrations (ops.go init), the WriteTypedMarker kind
// separation (knowledge seam compatibility), expire-pending dry-run, and the supersede
// stewardship (detecting unreflected updates). The fakeStore reuses the doctor_test.go conventions (the companion move).
package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// fakeMaintStore — fakeStore + the MaintStore update methods (lookups go via queryFn).
// The transition updates are the Repo delegation methods (batch Invalidate, Supersede).
type fakeMaintStore struct {
	*fakeStore
	invalidatedIDs [][]int
	merged         [][2]int
	supersedeErr   error
	superseded     [][2]int
	prunedAt       []time.Time // PruneInvalidated delegation capture
	recallPrunedAt []time.Time // PruneRecallEvents delegation capture
	recallPruneN   int64       // the PruneRecallEvents return (default 0)
}

func (f *fakeMaintStore) Counts(ctx context.Context) (db.Counts, error) {
	return db.Counts{}, nil
}
func (f *fakeMaintStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	return nil, nil
}
func (f *fakeMaintStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	f.invalidatedIDs = append(f.invalidatedIDs, ids)
	return int64(len(ids)), nil
}
func (f *fakeMaintStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	f.superseded = append(f.superseded, [2]int{oldID, newID})
	if f.supersedeErr != nil {
		return 0, f.supersedeErr
	}
	return 1, nil
}
func (f *fakeMaintStore) Merge(ctx context.Context, fromID, toID int) error {
	f.merged = append(f.merged, [2]int{fromID, toID})
	return nil
}

// PruneInvalidated — Repo delegation-method capture (return = the 2-target-row
// convention — same as the maintain_v0330 prune contract).
func (f *fakeMaintStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	f.prunedAt = append(f.prunedAt, before)
	return 2, nil
}

// PruneRecallEvents — Repo delegation-method capture (return = the recallPruneN setting).
func (f *fakeMaintStore) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	f.recallPrunedAt = append(f.recallPrunedAt, before)
	return f.recallPruneN, nil
}
func (f *fakeMaintStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	return nil, nil
}

// shortRetry — Retry retries run immediately (inheriting the maintain_v08_test convention).
func shortRetry(t *testing.T) {
	t.Helper()
	prev := MaintainRetryDelays
	MaintainRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { MaintainRetryDelays = prev })
}

// runMaintainArgs — runs RunMaintain with a fake store. Returns output and exit.
func runMaintainArgs(t *testing.T, f *fakeMaintStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestCareCommandsRegistered — the 4 wave-2 commands registered (ops.go init — wire.Register).
func TestCareCommandsRegistered(t *testing.T) {
	for _, name := range []string{"maintain", "nudge", "session-start", "session-end"} {
		if _, ok := wire.Lookup(name); !ok {
			t.Errorf("%s 미등록 — ops.go init 누락", name)
		}
	}
}

// TestWriteTypedMarkerKindSeparation — per-kind file separation (distill and recall
// coexist and expire independently). The same surface as the knowledge WriteTypedMarkerFn seam injection contract.
func TestWriteTypedMarkerKindSeparation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if err := WriteTypedMarker(MarkerKindDistill, "s-1", "compact"); err != nil {
		t.Fatalf("distill 기록 오류: %v", err)
	}
	if err := WriteTypedMarker(MarkerKindRecall, "s-1", NudgeReasonRecall); err != nil {
		t.Fatalf("recall 기록 오류: %v", err)
	}
	d, err := ReadTypedMarker(MarkerKindDistill)
	if err != nil || d == nil || d.SessionID != "s-1" || d.Reason != "compact" {
		t.Fatalf("distill 조회=%v/%v, want s-1/compact", d, err)
	}
	r, err := ReadTypedMarker(MarkerKindRecall)
	if err != nil || r == nil || r.Reason != "recall" {
		t.Fatalf("recall 조회=%v/%v, want reason recall(독립 파일)", r, err)
	}
	if err := ClearTypedMarker(MarkerKindRecall); err != nil {
		t.Fatalf("recall 소멸 오류: %v", err)
	}
	if r, _ := ReadTypedMarker(MarkerKindRecall); r != nil {
		t.Error("recall 미소멸")
	}
	if d, _ := ReadTypedMarker(MarkerKindDistill); d == nil {
		t.Error("distill이 recall 소멸에 함께 지워짐 — 종류 분리 위반")
	}
}

// TestMaintainExpirePendingDryRun — dry-run by default: only lists targets, exit 0, no updates.
func TestMaintainExpirePendingDryRun(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{{int64(7), "토픽", "요약", time.Now().Add(-45 * 24 * time.Hour)}}, nil
		}
		return nil, nil
	}
	out, code := runMaintainArgs(t, f, "--expire-pending", "30")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(dry-run); out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"expire-pending"`) || !strings.Contains(out, `"id":7`) {
		t.Fatalf("dry-run 나열 오류: %s", out)
	}
	if len(f.commits) != 0 || strings.Contains(strings.Join(f.queries, ";"), "UPDATE") {
		t.Error("dry-run에서 갱신 발생 — 계약 위반")
	}
}

// TestMaintainPruneInvalidatedAlsoPrunesRecallEvents — the R42-D wiring regression:
// --prune-invalidated also cleans the recall-event retention (recall_event_retention_days,
// default 90) expiries. Dry-run only reports (would_prune_recall_events); --commit does
// the PruneRecallEvents delegation + the audit commit. Re-running after the targets
// vanish is idempotently "no targets" (no delegation re-called).
func TestMaintainPruneInvalidatedAlsoPrunesRecallEvents(t *testing.T) {
	shortRetry(t)
	f := &fakeMaintStore{fakeStore: &fakeStore{}, recallPruneN: 7}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'invalidated'") && strings.Contains(sql, "SELECT") {
			return [][]any{{int64(3)}}, nil
		}
		if strings.Contains(sql, "count(*)") && strings.Contains(sql, "recall_events") {
			return [][]any{{int64(7)}}, nil
		}
		return nil, nil
	}
	out, code := runMaintainArgs(t, f, "--prune-invalidated", "30")
	if code != 0 {
		t.Fatalf("dry-run exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"would_prune_recall_events":7`) || !strings.Contains(out, `"recall_retention_days":90`) {
		t.Errorf("dry-run 이벤트 보고 누락: %s", out)
	}
	if len(f.recallPrunedAt) != 0 {
		t.Error("dry-run에서 PruneRecallEvents 위임 실행")
	}
	out, code = runMaintainArgs(t, f, "--prune-invalidated", "30", "--commit")
	if code != 0 {
		t.Fatalf("commit exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"deleted":2`) || !strings.Contains(out, `"pruned_recall_events":7`) {
		t.Errorf("commit 출력: %s", out)
	}
	if len(f.recallPrunedAt) != 1 {
		t.Fatalf("PruneRecallEvents 위임 %d회, want 1", len(f.recallPrunedAt))
	}
	if want := 90 * 24 * time.Hour; time.Since(f.recallPrunedAt[0]) < want-time.Hour || time.Since(f.recallPrunedAt[0]) > want+time.Hour {
		t.Errorf("이벤트 cutoff 경과 = %v, want 약 %v(보존기한 90일)", time.Since(f.recallPrunedAt[0]), want)
	}
	if !strings.Contains(strings.Join(f.commits, ";"), "prune-recall-events") {
		t.Errorf("감사 커밋 메시지에 이벤트 정리 귀속 없음: %v", f.commits)
	}
	// idempotent — after the targets vanish (0 listed, 0 expired), a re-run is no targets with no delegation.
	f2 := &fakeMaintStore{fakeStore: &fakeStore{}}
	f2.queryFn = func(sql string, args []any) ([][]any, error) { return nil, nil }
	out, code = runMaintainArgs(t, f2, "--prune-invalidated", "30", "--commit")
	if code != 0 {
		t.Fatalf("재실행 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"detail":"no targets"`) {
		t.Errorf("빈 재실행 출력: %s", out)
	}
	if len(f2.recallPrunedAt) != 0 || len(f2.prunedAt) != 0 {
		t.Errorf("빈 재실행 위임 발생 — 멱등 위반(prune=%d recall=%d)", len(f2.prunedAt), len(f2.recallPrunedAt))
	}
}

// TestMaintainSupersedeGuardian — the stewardship: an unreflected update (a race —
// Supersede hitting 0 rows) ends as an error, not a silent success (still unreflected
// after retries means a failed exit). The stewardship is built into db.Repo.Supersede
// via rows-affected — simulated here by the delegation returning a wrapped ErrNotFound error.
func TestMaintainSupersedeGuardian(t *testing.T) {
	shortRetry(t)
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	f.supersedeErr = fmt.Errorf("%w: supersede: id=5 갱신 미반영(경합 — 재시도)", db.ErrNotFound)
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status") {
			return [][]any{{"active", time.Now()}}, nil
		}
		return nil, nil
	}
	out, code := runMaintainArgs(t, f, "--supersede", "5", "--by", "9")
	if code == 0 {
		t.Fatalf("경합 미반영이 exit 0 — 후견사 부재; out=%s", out)
	}
	// exhausted Retry retries end the update path as an error (a 1-line error output — not a silent success).
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 출력 부재: %s", out)
	}
	if len(f.commits) != 0 {
		t.Error("미반영 경로에서 commit — 계약 위반")
	}
}

// TestMarkerFileUnderConfigDir — the marker path respects FLINT_CONFIG_DIR isolation.
func TestMarkerFileUnderConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if got := MarkerFile(); !strings.HasPrefix(got, filepath.Join(dir)) {
		if _, err := os.Stat(got); err != nil && got == "" {
			t.Skip("flintHome 폴백 환경")
		}
		t.Errorf("MarkerFile=%q, want %q 하위", got, dir)
	}
}
