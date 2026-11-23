package cli

import (
	"strings"
	"testing"
	"time"
)

func expireFixture(old, recent time.Time) *fakeMaint {
	return &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{int64(3), "backup-policy", "오래된 백업 정책 요약", old},
			{int64(4), "port-config", "최근 포트 정책", recent},
		}, nil
	}}}
}

func TestExpirePendingDryRun(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	f := expireFixture(old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}

	if !strings.Contains(out, `"id":3`) || !strings.Contains(out, `"age_days":7`) {
		t.Errorf("7일 행 미포함: %q", out)
	}
	if strings.Contains(out, `"id":4`) {
		t.Errorf("2일 행이 나열됨: %q", out)
	}
	if !strings.Contains(out, `"status":"expire-pending"`) {
		t.Errorf("status 불일치: %q", out)
	}

	if len(f.invalidateCalls) != 0 {
		t.Errorf("dry-run에서 Invalidate 실행: %v", f.invalidateCalls)
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에서 Commit 실행: %v", f.commits)
	}
}

func TestExpirePendingCommit(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	f := expireFixture(old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5", "--commit")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.invalidateCalls) != 1 {
		t.Fatalf("Invalidate 호출 수=%d, want 1", len(f.invalidateCalls))
	}

	if len(f.invalidateCalls[0]) != 1 || f.invalidateCalls[0][0] != 3 {
		t.Errorf("Invalidate 파라미터=%v, want [[3]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 1 || f.invalidateMsgs[0] != "expire-pending" {
		t.Errorf("Invalidate 사유=%v, want expire-pending", f.invalidateMsgs)
	}

	if !strings.Contains(out, `"status":"invalidated"`) || !strings.Contains(out, `"count":1`) {
		t.Errorf("전환 건수 출력 불일치: %q", out)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "expire-pending") {
		t.Errorf("커밋 메시지=%v, want expire-pending 1회", f.commits)
	}
}

func TestExpirePendingValidation(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--expire-pending")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("days 누락 거부 없음: exit=%d %q", code, out)
	}
	out, code = runMaintainArgs(t, f, "--expire-pending", "0")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("양수 아닌 days 거부 없음: exit=%d %q", code, out)
	}
	out, code = runMaintainArgs(t, f, "--commit")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("--commit 단독 거부 없음: exit=%d %q", code, out)
	}

	out, code = runMaintainArgs(t, f, "--expire-pending", "5", "--id", "7")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", code, out)
	}
}
