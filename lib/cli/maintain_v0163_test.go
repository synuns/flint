// maintain_v0163_test.go — v0.16.3: conflict_dup_threshold meta 오버라이드가
// --conflict-suspects 분류에 반영되는지(Thresholds 레지스트리 우선).
package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// conflict_dup_threshold 오버라이드 — 임계를 요약 쌍 실측 유사도 초과로
// 올리면 기본 0.6에서 중복(제외)이던 그룹이 모순 후보로 리스팅된다.
// 임계값은 실측 Dice와 1의 중점 — 픽스처 문구에 의존하지 않는 결정적 검증.
func TestConflictThresholdOverride(t *testing.T) {
	summA, summB := "flint 백업은 매일 자동이다", "flint 백업은 매주 수동 점검이다"
	dice := conflictDice(summA, summB)
	if dice < conflictDupThreshold || dice >= 1 {
		t.Fatalf("픽스처 유사도 %v는 [기본 임계, 1) 구간 밖 — 검증 전제 위반", dice)
	}
	over := strconv.FormatFloat((dice+1)/2, 'f', -1, 64)

	// 기본(빈 meta) — 중복 후보: 리스팅되지 않는다.
	base := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{summA, summB}}}}
	out, code := runConflictArgs(t, base, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("기본 exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"groups":[]`) {
		t.Errorf("기본 0.6에서 중복 그룹이 리스팅됨: %q", out)
	}

	// 오버라이드 — 임계 상향으로 같은 그룹이 모순 후보로 리스팅된다.
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "conflict_dup_threshold") {
				return [][]any{{"conflict_dup_threshold", over}}, nil
			}
			return nil, nil
		},
	}}, conflictGroups: []db.ConflictGroup{
		{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{summA, summB}},
	}}
	out2, code2 := runConflictArgs(t, f, "--conflict-suspects")
	if code2 != 0 {
		t.Fatalf("오버라이드 exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if !strings.Contains(out2, `"topic":"backup-policy"`) {
		t.Errorf("오버라이드 %s에서 모순 후보 미리스팅(기본 0.6 잔존 의심): %q", over, out2)
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
