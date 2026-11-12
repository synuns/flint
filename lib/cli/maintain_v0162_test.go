// maintain_v0162_test.go — T3 `maintain --expire-pending`(스펙 R4):
// valid_at 경과 N일 초과 pending 행 나열(dry-run 기본)·--commit 시
// 기존 무효화 경로(status='invalidated'·invalid_at=now) 전환.
package cli

import (
	"strings"
	"testing"
	"time"
)

// expireFixture — pending 2건(경과 7일·2일)을 반환하는 queryFn. 전이
// 갱신은 Repo.Invalidate 위임이라 queryFn은 조회(SELECT)만 담당한다
// (v0.41.0 T2 — UPDATE 캡처는 fakeMaint Invalidate가 대신한다).
func expireFixture(old, recent time.Time) *fakeMaint {
	return &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{int64(3), "backup-policy", "오래된 백업 정책 요약", old},
			{int64(4), "port-config", "최근 포트 정책", recent},
		}, nil
	}}}
}

// TestExpirePendingDryRun — Review Focus: pending 2건(경과 7일·2일)에서
// days=5 나열 시 7일 행 1건만 출력·전이·Commit 미호출.
func TestExpirePendingDryRun(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	f := expireFixture(old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	// 7일 행만 나열 — id·topic·경과일 노출.
	if !strings.Contains(out, `"id":3`) || !strings.Contains(out, `"age_days":7`) {
		t.Errorf("7일 행 미포함: %q", out)
	}
	if strings.Contains(out, `"id":4`) {
		t.Errorf("2일 행이 나열됨: %q", out)
	}
	if !strings.Contains(out, `"status":"expire-pending"`) {
		t.Errorf("status 불일치: %q", out)
	}
	// dry-run — 전이·Commit 무변경.
	if len(f.invalidateCalls) != 0 {
		t.Errorf("dry-run에서 Invalidate 실행: %v", f.invalidateCalls)
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에서 Commit 실행: %v", f.commits)
	}
}

// TestExpirePendingCommit — Review Focus: 전이 위임 캡처 — 대상 id(7일 행)
// 스냅숏만 Invalidate에 전달·건수 출력·커밋 메시지.
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
	// 나열된 7일 행(id 3)만 — dry-run 대상과 확정 대상 일치.
	if len(f.invalidateCalls[0]) != 1 || f.invalidateCalls[0][0] != 3 {
		t.Errorf("Invalidate 파라미터=%v, want [[3]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 1 || f.invalidateMsgs[0] != "expire-pending" {
		t.Errorf("Invalidate 사유=%v, want expire-pending", f.invalidateMsgs)
	}
	// 전환 건수 출력·커밋 메시지.
	if !strings.Contains(out, `"status":"invalidated"`) || !strings.Contains(out, `"count":1`) {
		t.Errorf("전환 건수 출력 불일치: %q", out)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "expire-pending") {
		t.Errorf("커밋 메시지=%v, want expire-pending 1회", f.commits)
	}
}

// TestExpirePendingValidation — days 필수·양수(오삭제 방지 — 기본값 없음),
// --commit은 --expire-pending 전용.
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
	// 무관 값 플래그 거부 — 모드 귀속 승계.
	out, code = runMaintainArgs(t, f, "--expire-pending", "5", "--id", "7")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", code, out)
	}
}
