// doctor_v0132_test.go — v0.13.2 심층 리뷰 회귀: N7 --check conflicts
// 단독 실행(tail-chain 서브셋 일반화)·N8 조회 상한 도달 문구.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"flint/lib/db"
)

// TestDoctorCheckConflictsSubset — N7: `doctor --check conflicts`는
// unknown check exit 3이 아니라 검사 1건(conflicts)만 실행해 exit 0.
// fakeStore는 ConflictGroups seam(conflicts_v0131_test.go)을 제공하므로
// 그룹 0개 → ok 경로로 단독 실행 계약을 확인한다.
func TestDoctorCheckConflictsSubset(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 모순 후보 그룹 0개
	}}
	var out bytes.Buffer
	code := runDoctor(context.Background(), st, []string{"--check", "conflicts"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out.String())
	}
	if !strings.Contains(out.String(), `"name":"conflicts"`) {
		t.Errorf("출력에 conflicts 검사 없음: %s", out.String())
	}
}

// TestConflictsLimitReachedNotesOverflow — N8: 그룹 수가 doctor 상한
// (doctorConflictLimit)에 도달하면 detail에 상한 초과 가능 문구가 붙는다.
func TestConflictsLimitReachedNotesOverflow(t *testing.T) {
	groups := make([]db.ConflictGroup, doctorConflictLimit)
	for i := range groups {
		groups[i] = db.ConflictGroup{
			Topic: fmt.Sprintf("topic-%d", i),
			IDs:   []int{2*i + 1, 2*i + 2},
			// 요약이 완전히 달라 isConflictGroup 참 — 모순 후보 50그룹.
			Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"},
		}
	}
	inner := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	s := &seamConflictStore{fakeStore: inner, groups: groups}
	c := checkConflicts(context.Background(), s)
	if c.Status != "warn" {
		t.Fatalf("status = %s, want warn", c.Status)
	}
	if !strings.Contains(c.Detail, "상한 초과 가능") {
		t.Errorf("상한 도달 문구 없음: %q", c.Detail)
	}
	if len(s.calls) != 1 || s.calls[0] != doctorConflictLimit {
		t.Errorf("조회 limit = %v, want [%d]", s.calls, doctorConflictLimit)
	}
}
