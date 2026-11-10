package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestTruncateRunesSingleSource — Review Focus(a): rune 절단 3중 복제
// (import 500룬·maintain 접두 40·pending oneLine)의 단일 구현 수렴.
// truncateRunes는 룬 단위 절단(멀티바이트 안전)이고 각 관례 래퍼는
// 결과 불변으로 위임한다.
func TestTruncateRunesSingleSource(t *testing.T) {
	long := strings.Repeat("한", 60) // 60룬(180바이트) — 바이트 절단이면 파손
	if got := truncateRunes(long, 40); len([]rune(got)) != 40 || got != strings.Repeat("한", 40) {
		t.Errorf("룬 절단 위반: %d룬", len([]rune(got)))
	}
	if got := truncateRunes("abc한", 3); got != "abc" {
		t.Errorf("혼합 문자열 절단=%q, want %q", got, "abc")
	}
	if got := truncateRunes("한글", 2); got != "한글" {
		t.Errorf("경계(상한==길이) 불변 위반: %q", got)
	}
	if got := truncateRunes("한글", 40); got != "한글" {
		t.Errorf("상한 이하 불변 위반: %q", got)
	}
	if got := truncateRunes("", 40); got != "" {
		t.Errorf("빈 문자열: %q", got)
	}
	// 관례 위임 동치 — maintain 접두 40자(공백·개행 없는 입력으로 비교).
	if conflictSummaryPrefix(long) != truncateRunes(long, 40) {
		t.Errorf("conflictSummaryPrefix 미위임")
	}
	// pending oneLine 상한(개행·공백 정리 후 위임 — 정제 불필요 입력으로 비교).
	if oneLine(long, 48) != truncateRunes(long, 48) {
		t.Errorf("oneLine 미위임")
	}
}

// TestUnknownFlagExitContract — Review Focus(c): maintain·import의 무효
// 플래그는 무효 인자 계약 exit 3(doctor·tuning·diff 정합).
func TestUnknownFlagExitContract(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--check", "--bogus")
	if code != 3 {
		t.Errorf("maintain 무효 플래그 exit=%d, want 3 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "unknown flag") {
		t.Errorf("maintain 에러 미포함: %q", out)
	}

	fi := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var b strings.Builder
	icode := runImport(context.Background(), fi, []string{"--verbose"}, strings.NewReader(""), &b)
	if icode != 3 {
		t.Errorf("import 무효 플래그 exit=%d, want 3 (출력 %q)", icode, b.String())
	}
	if !strings.Contains(b.String(), "unknown flag") {
		t.Errorf("import 에러 미포함: %q", b.String())
	}
}

// TestSupersedeZeroRowsFails — Review Focus(e): 사전 확인(active) 통과 후
// 경합 등으로 UPDATE가 0행 갱신되면 무음 성공이 아니라 오류(exit 1·커밋 없음)
// — supersedeAfterApprove 후견사 패턴 승계.
func TestSupersedeZeroRowsFails(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "SELECT (SELECT status"):
			return [][]any{{"active", time.Now().Add(-24 * time.Hour)}}, nil
		case strings.HasPrefix(strings.TrimSpace(sql), "UPDATE"):
			return nil, nil // no-op 갱신 — 0행 반영(경합 시뮬레이션)
		case strings.Contains(sql, "superseded_by FROM knowledge"):
			return nil, nil // 후검사: 미반영
		}
		return nil, errors.New("unexpected query: " + sql)
	}}}
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 1 {
		t.Errorf("0행 갱신 exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("에러 JSON 아님: %q", out)
	}
	if len(f.commits) != 0 {
		t.Errorf("무음 성공 커밋 발생: %v", f.commits)
	}
}
