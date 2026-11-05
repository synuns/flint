// maintain_v0163_test.go — v0.16.3: conflict_dup_threshold meta 오버라이드가
// --conflict-suspects 분류에 반영되는지(Thresholds 레지스트리 우선).
package cli

import (
	"strings"
	"testing"

	"flint/lib/db"
)

// conflict_dup_threshold=0.01 오버라이드 — 기본 0.6에서 모순 후보였던
// 이질 요약 그룹(Dice는 0.01 이상)이 중복으로 재분류돼 출력에서 빠진다.
func TestConflictThresholdOverride(t *testing.T) {
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "conflict_dup_threshold") {
				return [][]any{{"conflict_dup_threshold", "0.01"}}, nil
			}
			return nil, nil
		},
	}}, conflictGroups: []db.ConflictGroup{
		{Topic: "port-config", IDs: []int{3, 4},
			Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
	}}
	out, code := runConflictArgs(t, f, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"groups":[]`) {
		t.Errorf("오버라이드 0.01에서 이질 그룹이 모순 후보로 남음(기본 0.6 잔존 의심): %q", out)
	}
}

// 대조 — 오버라이드 없음(빈 meta)이면 기본 0.6으로 같은 그룹이 모순 후보.
func TestConflictThresholdDefaultKept(t *testing.T) {
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "port-config", IDs: []int{3, 4},
				Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}}
	out, code := runConflictArgs(t, f, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"topic":"port-config"`) {
		t.Errorf("기본 0.6 분류 회귀 — 이질 그룹 미포함: %q", out)
	}
}
