// maintain_v0130_test.go — T2 `maintain --conflict-suspects`(스펙 R2):
// 동일 topic active 2+ 그룹을 요약 유사도로 분류해 모순 후보만 리스팅.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"flint/lib/db"
)

func TestClassifyConflictBoundary(t *testing.T) {
	if classifyConflict("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다") {
		t.Error("동일 요약 = 모순 아님")
	}
	if !classifyConflict("포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다") {
		t.Error("이질 요약 = 모순 후보")
	}
}

// fakeConflict — fakeMaint에 ConflictGroups 조회를 얹은 fake(슬러블 가능).
type fakeConflict struct {
	*fakeMaint

	conflictGroups []db.ConflictGroup
	conflictErr    error
	conflictCalls  []int
}

func (f *fakeConflict) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	f.conflictCalls = append(f.conflictCalls, limit)
	return f.conflictGroups, f.conflictErr
}

func runConflictArgs(t *testing.T, f *fakeConflict, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runMaintain(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// TestConflictSuspectsFlag — Review Focus: fake 그룹(동일 요약·이질 요약)
// 2개에서 이질 1그룹만 출력. 출력은 dup-suspects 승계 + 요약 앞 40자.
func TestConflictSuspectsFlag(t *testing.T) {
	same := "flint 백업은 매일 자동이다"
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{same, same}},
			{Topic: "port-config", IDs: []int{3, 4},
				Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}}
	out, code := runConflictArgs(t, f, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"topic":"port-config"`) {
		t.Errorf("이질 그룹 미포함: %q", out)
	}
	if strings.Contains(out, "backup-policy") {
		t.Errorf("동일 요약 그룹이 모순 후보로 리스팅됨: %q", out)
	}
	if !strings.Contains(out, `"status":"conflict-suspects"`) {
		t.Errorf("status 불일치: %q", out)
	}
	// 요약 앞 40자 승계 — 잘린 요약이 출력에 있다(원문 전체 아님).
	if !strings.Contains(out, "포트 기본값은 5433이다") {
		t.Errorf("요약 미포함: %q", out)
	}
	if len(f.conflictCalls) != 1 || f.conflictCalls[0] != 10 {
		t.Errorf("ConflictGroups 호출 limit = %v, want [10]", f.conflictCalls)
	}

	// --limit 전달.
	f2 := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	_, code2 := runConflictArgs(t, f2, "--conflict-suspects", "--limit", "5")
	if code2 != 0 {
		t.Fatalf("--limit: exit=%d, want 0", code2)
	}
	if len(f2.conflictCalls) != 1 || f2.conflictCalls[0] != 5 {
		t.Errorf("--limit 전달 = %v, want [5]", f2.conflictCalls)
	}

	// 모순 후보 없음 — 빈 groups.
	f3 := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{same, same}}}}
	out3, code3 := runConflictArgs(t, f3, "--conflict-suspects")
	if code3 != 0 {
		t.Fatalf("빈 결과: exit=%d, want 0 (출력 %q)", code3, out3)
	}
	if !strings.Contains(out3, `"groups":[]`) {
		t.Errorf("빈 groups 아님: %q", out3)
	}

	// 모드 귀속 — --id는 --conflict-suspects와 함께 쓸 수 없다.
	m := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	mout, mcode := runConflictArgs(t, m, "--conflict-suspects", "--id", "7", "--reason", "x")
	if mcode != 1 || !strings.Contains(mout, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", mcode, mout)
	}

	// usage 안내에 --conflict-suspects 포함.
	u := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	uout, ucode := runConflictArgs(t, u)
	if ucode != 1 || !strings.Contains(uout, "--conflict-suspects") {
		t.Errorf("usage 갱신 누락: exit=%d %q", ucode, uout)
	}
}

// TestConflictDiceMeasures — 경계 실측: 동일 문자열 Dice=1, 이질 쌍은
// 임계값 미만, 상수는 룰링 값 0.6.
func TestConflictDiceMeasures(t *testing.T) {
	if got := conflictDice("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다"); got != 1 {
		t.Errorf("동일 문자열 Dice = %v, want 1", got)
	}
	got := conflictDice("포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다")
	if got >= conflictDupThreshold {
		t.Errorf("이질 쌍 Dice = %v, want <%v", got, conflictDupThreshold)
	}
	if conflictDupThreshold != 0.6 {
		t.Errorf("conflictDupThreshold = %v, want 0.6(룰링)", conflictDupThreshold)
	}
}

// TestConflictSummaryTruncation — 요약 앞 40자(rune 기준) 절단.
func TestConflictSummaryTruncation(t *testing.T) {
	long := strings.Repeat("가", 50)
	if got := conflictSummaryPrefix(long); len([]rune(got)) != 40 {
		t.Errorf("절단 길이 = %d, want 40", len([]rune(got)))
	}
	short := "짧은 요약"
	if got := conflictSummaryPrefix(short); got != short {
		t.Errorf("짧은 요약 변형 = %q, want 원문", got)
	}
}
