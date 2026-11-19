// maintain_v0130_test.go — `maintain --conflict-suspects` (spec R2):
// classify same-topic active 2+ groups by summary similarity and list only contradiction candidates.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

func TestClassifyConflictBoundary(t *testing.T) {
	if classifyConflict("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다") {
		t.Error("동일 요약 = 모순 아님")
	}
	if !classifyConflict("포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다") {
		t.Error("이질 요약 = 모순 후보")
	}
}

// fakeConflict — a fake layering ConflictGroups lookup onto fakeMaint (stub-configurable).
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

// TestConflictSuspectsFlag — of the 2 fake groups (identical summaries, dissimilar
// summaries), only the 1 dissimilar group is output. The output inherits dup-suspects + the first 40 chars of the summary.
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
	// inherits the first-40-chars summary — the truncated summary is in the output (not the full text).
	if !strings.Contains(out, "포트 기본값은 5433이다") {
		t.Errorf("요약 미포함: %q", out)
	}
	if len(f.conflictCalls) != 1 || f.conflictCalls[0] != 10 {
		t.Errorf("ConflictGroups 호출 limit = %v, want [10]", f.conflictCalls)
	}

	// --limit passed through.
	f2 := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	_, code2 := runConflictArgs(t, f2, "--conflict-suspects", "--limit", "5")
	if code2 != 0 {
		t.Fatalf("--limit: exit=%d, want 0", code2)
	}
	if len(f2.conflictCalls) != 1 || f2.conflictCalls[0] != 5 {
		t.Errorf("--limit 전달 = %v, want [5]", f2.conflictCalls)
	}

	// no contradiction candidates — empty groups.
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

	// mode ownership — --id cannot be used with --conflict-suspects.
	m := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	mout, mcode := runConflictArgs(t, m, "--conflict-suspects", "--id", "7", "--reason", "x")
	if mcode != 1 || !strings.Contains(mout, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", mcode, mout)
	}

	// usage includes --conflict-suspects (bare usage is 3).
	u := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	uout, ucode := runConflictArgs(t, u)
	if ucode != 3 || !strings.Contains(uout, "--conflict-suspects") {
		t.Errorf("usage 갱신 누락: exit=%d %q", ucode, uout)
	}
}

// TestConflictDiceMeasures — boundary observations: identical strings Dice=1; dissimilar pairs
// are below the threshold; the constant is the ruled value 0.6.
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

// TestConflictSummaryTruncation — truncation at the first 40 chars (rune-based) of the summary.
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
