// care_test.go — v0.40.0 T2(ops 2탄) 이동분 행위 검증: maintain·nudge·
// session-start·session-end 등록(ops.go init)·WriteTypedMarker 종류 분리
// (knowledge seam 호환)·expire-pending dry-run·supersede 후견사(경합
// 미반영 감지). fakeStore는 doctor_test.go 관례(동반 이동분)를 재사용한다.
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

// fakeMaintStore — fakeStore + MaintStore 갱신 메서드(조회는 queryFn 경유).
// v0.41.0 T2: 전이 갱신은 Repo 위임 메서드(Invalidate 배치·Supersede).
type fakeMaintStore struct {
	*fakeStore
	invalidatedIDs [][]int
	merged         [][2]int
	supersedeErr   error
	superseded     [][2]int
	prunedAt       []time.Time // v0.41.0 T3: PruneInvalidated 위임 캡처
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

// PruneInvalidated — v0.41.0 T3: Repo 위임 메서드 캡처(반환 = 대상 2행
// 관례 — maintain_v0330 prune 계약과 동일).
func (f *fakeMaintStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	f.prunedAt = append(f.prunedAt, before)
	return 2, nil
}
func (f *fakeMaintStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	return nil, nil
}

// shortRetry — Retry 재시도 즉시 실행(maintain_v08_test 관례 승계).
func shortRetry(t *testing.T) {
	t.Helper()
	prev := MaintainRetryDelays
	MaintainRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { MaintainRetryDelays = prev })
}

// runMaintainArgs — fake store으로 RunMaintain 실행. 출력·exit 반환.
func runMaintainArgs(t *testing.T, f *fakeMaintStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestCareCommandsRegistered — 2탄 커맨드 4종 등록(ops.go init — wire.Register).
func TestCareCommandsRegistered(t *testing.T) {
	for _, name := range []string{"maintain", "nudge", "session-start", "session-end"} {
		if _, ok := wire.Lookup(name); !ok {
			t.Errorf("%s 미등록 — ops.go init 누락", name)
		}
	}
}

// TestWriteTypedMarkerKindSeparation — 종류별 파일 분리(distill·recall 동시
// 존재·독립 소멸). knowledge WriteTypedMarkerFn seam 주입 계약과 동일 표면.
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

// TestMaintainExpirePendingDryRun — dry-run 기본: 대상 나열만·exit 0·갱신 없음.
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

// TestMaintainSupersedeGuardian — 후견사: 갱신 미반영(경합 — Supersede 0행)은
// 무음 성공이 아니라 에러로 종결(재시도 후에도 미반영이면 실패 exit).
// v0.41.0 T2: 후견사는 rows-affected로 db.Repo.Supersede에 내장 — 여기선
// 위임이 ErrNotFound 랩 오류를 돌려주는 것으로 시뮬레이션한다.
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
	// Retry 재시도 소진은 갱신 경로 에러로 종결(에러 1줄 출력 — 무음 성공 아님).
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 출력 부재: %s", out)
	}
	if len(f.commits) != 0 {
		t.Error("미반영 경로에서 commit — 계약 위반")
	}
}

// TestMarkerFileUnderConfigDir — 마커 경로는 FLINT_CONFIG_DIR 격리 준수.
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
