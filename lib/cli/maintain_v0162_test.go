// maintain_v0162_test.go — T3 `maintain --expire-pending`(스펙 R4):
// valid_at 경과 N일 초과 pending 행 나열(dry-run 기본)·--commit 시
// 기존 무효화 경로(status='invalidated'·invalid_at=now) 전환.
package cli

import (
	"strings"
	"testing"
	"time"
)

// expireFixture — pending 2건(경과 7일·2일)을 반환하는 queryFn. UPDATE는
// updSQL·updArgs에 캡처한다(승계 fakeStore 패턴 — fakeMaint 내장).
func expireFixture(updSQL *[]string, updArgs *[][]any, old, recent time.Time) *fakeMaint {
	return &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "UPDATE") {
			*updSQL = append(*updSQL, sql)
			*updArgs = append(*updArgs, args)
			return [][]any{{int64(1)}}, nil
		}
		return [][]any{
			{int64(3), "backup-policy", "오래된 백업 정책 요약", old},
			{int64(4), "port-config", "최근 포트 정책", recent},
		}, nil
	}}}
}

// TestExpirePendingDryRun — Review Focus: pending 2건(경과 7일·2일)에서
// days=5 나열 시 7일 행 1건만 출력·UPDATE·Commit 미호출.
func TestExpirePendingDryRun(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	var updSQL []string
	var updArgs [][]any
	f := expireFixture(&updSQL, &updArgs, old, recent)

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
	// dry-run — UPDATE·Commit 무변경.
	if len(updSQL) != 0 {
		t.Errorf("dry-run에서 UPDATE 실행: %v", updSQL)
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에서 Commit 실행: %v", f.commits)
	}
}

// TestExpirePendingCommit — Review Focus: UPDATE 캡처 — status='invalidated'·
// invalid_at=now() 설정·컷오프가 대상 id(7일 행)만 포함·건수 출력.
func TestExpirePendingCommit(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	var updSQL []string
	var updArgs [][]any
	f := expireFixture(&updSQL, &updArgs, old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5", "--commit")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(updSQL) != 1 {
		t.Fatalf("UPDATE 호출 수=%d, want 1", len(updSQL))
	}
	if !strings.Contains(updSQL[0], "status = 'invalidated'") {
		t.Errorf("UPDATE가 status 전환 아님: %q", updSQL[0])
	}
	if !strings.Contains(updSQL[0], "invalid_at = now()") {
		t.Errorf("UPDATE가 invalid_at 설정 아님: %q", updSQL[0])
	}
	// 대상 id 일치 — 동일 조건 파라미터(컷오프)가 7일 행만 포함하는지.
	if len(updArgs[0]) != 1 {
		t.Fatalf("UPDATE 파라미터=%v, want 컷오프 1개", updArgs[0])
	}
	cutoff, ok := updArgs[0][0].(time.Time)
	if !ok {
		t.Fatalf("컷오프 파라미터 타입=%T, want time.Time", updArgs[0][0])
	}
	if !cutoff.After(old) || !cutoff.Before(recent) {
		t.Errorf("컷오프 %v가 7일 행만 포함하지 않음(old=%v recent=%v)", cutoff, old, recent)
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
