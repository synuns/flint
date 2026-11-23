package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
)

type recheckCondStore struct {
	*fakeMaintStore
	conditions   string
	condCalls    [][2]any
	condErr      error
	notActiveIDs map[int]bool
}

func (f *recheckCondStore) SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error) {
	f.condCalls = append(f.condCalls, [2]any{id, conditions})
	if f.condErr != nil {
		return 0, f.condErr
	}
	if len(f.notActiveIDs) == 0 || !f.notActiveIDs[id] {
		f.conditions = conditions
		return 1, nil
	}
	return 0, nil
}

func runRecheckCondArgs(t *testing.T, f *recheckCondStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

func newRecheckCondStore() *recheckCondStore {
	validAt := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	return &recheckCondStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "topic, tags, conditions, valid_at"):

			return [][]any{{"r96-재점검-데모", ":adr::vol:immutable:", currentConditions, validAt}}, nil
		case strings.Contains(sql, "SELECT id FROM knowledge"):

			if len(args) > 0 && args[0] == 99 {
				return nil, nil
			}
			return [][]any{{int32(7)}}, nil
		}
		return nil, nil
	}}}}
}

var currentConditions = ""

func TestMaintainRecheckConditionsBackfill(t *testing.T) {
	shortRetry(t)
	currentConditions = ""
	t.Cleanup(func() { currentConditions = "" })

	f := newRecheckCondStore()

	out, code := runRecheckCondArgs(t, f, "--recheck", "7")
	if code != 0 || !strings.Contains(out, "--recheck --conditions") {
		t.Fatalf("보고 exit=%d out=%s, want 0 + 새 안내문", code, out)
	}

	out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", "when demo holds (r96)", "--yes")
	if code != 0 {
		t.Fatalf("갱신 exit=%d out=%s, want 0", code, out)
	}
	if len(f.condCalls) != 1 || f.condCalls[0][0] != 7 || f.condCalls[0][1] != "when demo holds (r96)" {
		t.Fatalf("SetRecheckConditions 호출 = %v, want [[7 when demo holds (r96)]]", f.condCalls)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "recheck") {
		t.Fatalf("감사 커밋 부재: %v", f.commits)
	}
	if !strings.Contains(out, `"status":"recheck_conditions"`) || !strings.Contains(out, `"id":7`) {
		t.Fatalf("출력 = %s", out)
	}
	if len(f.invalidatedIDs) != 0 {
		t.Fatalf("무효화 경로 오염: %v", f.invalidatedIDs)
	}

	currentConditions = "when demo holds (r96)"
	out, code = runRecheckCondArgs(t, f, "--recheck", "7")
	if code != 0 {
		t.Fatalf("갱신 후 보고 exit=%d: %s", code, out)
	}
	if !strings.Contains(out, "when demo holds (r96)") || strings.Contains(out, `"conditions_empty":true`) {
		t.Fatalf("왕복 실패: %s", out)
	}
}

func TestMaintainRecheckConditionsGuards(t *testing.T) {
	shortRetry(t)
	currentConditions = ""
	t.Cleanup(func() { currentConditions = "" })

	f := newRecheckCondStore()
	out, code := runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", "demo")
	if code != 1 || !strings.Contains(out, "--yes") {
		t.Fatalf("--yes 부재 exit=%d out=%s, want 1+안내", code, out)
	}
	if len(f.condCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("가드 전 쓰기: %v %v", f.condCalls, f.commits)
	}

	f = newRecheckCondStore()
	over := strings.Repeat("른", knowledge.MaxConditionsRunes+1)
	out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", over, "--yes")
	if code != 3 || !strings.Contains(out, "conditions") {
		t.Fatalf("길이 초과 exit=%d out=%s, want 3+conditions 필드", code, out)
	}
	if len(f.condCalls) != 0 {
		t.Fatalf("길이 초과 쓰기: %v", f.condCalls)
	}

	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", "", "--yes"); code != 0 {
		t.Fatalf("빈 값 exit=%d out=%s, want 0(허용)", code, out)
	}
	if len(f.condCalls) != 1 || f.condCalls[0][1] != "" {
		t.Fatalf("빈 값 위임 = %v, want 조건 빈 문자열", f.condCalls)
	}

	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--stale", "--days", "5", "--conditions", "demo"); code != 1 {
		t.Fatalf("무관 모드 exit=%d out=%s, want 1", code, out)
	}

	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--yes"); code != 1 {
		t.Fatalf("--recheck --yes(쓰기 아님) exit=%d out=%s, want 1", code, out)
	}

	f = newRecheckCondStore()
	out, code = runRecheckCondArgs(t, f, "--recheck", "99", "--conditions", "demo", "--yes")
	if code != 1 || !strings.Contains(out, "not found") {
		t.Fatalf("부재 exit=%d out=%s, want 1+not found", code, out)
	}
	if len(f.condCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("부재 경로 쓰기: %v %v", f.condCalls, f.commits)
	}
}

func TestMaintainIDParseGuards(t *testing.T) {

	if _, err := parseIDList("5,5"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("--ids 5,5: err = %v, want duplicate rejection", err)
	}
	if _, err := parseIDList("5,6,5"); err == nil {
		t.Fatalf("--ids 5,6,5: 중복 미거부")
	}

	ids, err := parseIDList(" 7, 8 ")
	if err != nil || len(ids) != 2 || ids[0] != 7 || ids[1] != 8 {
		t.Fatalf("정상 목록 파싱 = %v %v", ids, err)
	}

	if _, err := parseIDList("2147483648"); err == nil || !strings.Contains(err.Error(), "int4") {
		t.Fatalf("int4 초과: err = %v, want int4 ceiling 거부", err)
	}
	if ids, err = parseIDList("2147483647"); err != nil || ids[0] != 2147483647 {
		t.Fatalf("int4 상한값 파싱 = %v %v, want 통과", ids, err)
	}

	f := &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}}
	out, code := runReaffirmArgs(t, f, "--reaffirm", "--ids", "5,5", "--yes")
	if code != 1 || !strings.Contains(out, "duplicate") {
		t.Fatalf("--ids 5,5 exit=%d out=%s, want 1+duplicate", code, out)
	}
	if len(f.reaffirmCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("중복 토큰 경로 쓰기: %v %v", f.reaffirmCalls, f.commits)
	}

	f2 := &recheckCondStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id FROM knowledge") {
			return [][]any{{int32(5)}}, nil
		}
		return nil, nil
	}}}}
	currentConditions = ""
	t.Cleanup(func() { currentConditions = "" })
	if out, code = runRecheckCondArgs(t, f2, "--recheck", "5", "--conditions", "demo", "--yes"); code != 0 {
		t.Fatalf("단일 적용 exit=%d out=%s, want 0", code, out)
	}
	if len(f2.condCalls) != 1 || f2.condCalls[0][0] != 5 {
		t.Fatalf("단일 적용 위임 = %v, want [[5 ...]]", f2.condCalls)
	}

	for _, args := range [][]string{
		{"--recheck", "2147483648"},
		{"--recheck", "9223372036854775807"},
		{"--reaffirm", "--id", "2147483648", "--yes"},
	} {
		if out, code = runReaffirmArgs(t, f, args...); code != 1 || !strings.Contains(out, "int4") {
			t.Fatalf("%v exit=%d out=%s, want 1+int4 ceiling", args, code, out)
		}
	}

	f3 := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}
	if out, code = runMaintainArgs(t, f3, "--recheck", "2147483647"); code != 1 || !strings.Contains(out, "not found") {
		t.Fatalf("int4 상한 id exit=%d out=%s, want 1+not found(파싱 통과)", code, out)
	}
}

func TestMaintainReaffirmPrecheckInvalidAt(t *testing.T) {
	shortRetry(t)
	f := &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "status") {
			if strings.Contains(sql, "invalid_at IS NULL") {

				return [][]any{{int32(7), "active"}}, nil
			}

			return [][]any{{int32(7), "active"}, {int32(8), "active"}}, nil
		}
		return nil, nil
	}}}}
	out, code := runReaffirmArgs(t, f, "--reaffirm", "--ids", "7,8", "--yes")
	if code != 1 {
		t.Fatalf("exit=%d out=%s, want 1(invalid_at 행 거부)", code, out)
	}
	if !strings.Contains(out, "8") {
		t.Fatalf("진단에 invalid_at 행 미포함: %s", out)
	}
	if len(f.reaffirmCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("거부 경로에서 쓰기: %v %v", f.reaffirmCalls, f.commits)
	}
}
