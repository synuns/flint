// maintain_v1221_test.go — the v1.22.1 r96 regressions: the
// --recheck --conditions backfill (delegation, commit, output, guards,
// round-trip through the report) and the reaffirm precheck's
// invalid_at IS NULL naming (r96-4 diagnostic accuracy). Synthetic demo
// rows only.
package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
)

// recheckCondStore — fakeMaintStore + the RecheckConditionsStore write
// extension capture (the UPDATE literal lives in the store layer — the
// ops flow asserts the delegation, commit, and output).
type recheckCondStore struct {
	*fakeMaintStore
	conditions   string // the row's current conditions (the round-trip state)
	condCalls    [][2]any
	condErr      error
	notActiveIDs map[int]bool // ids the write precheck must refuse
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

// runRecheckCondArgs — runMaintainArgs is fixed to *fakeMaintStore, so a
// local helper for the extension wrapper (the runReaffirmArgs convention).
func runRecheckCondArgs(t *testing.T, f *recheckCondStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// newRecheckCondStore — the default fake: id 7 exists and is active
// (both the write precheck and the report row answer from the shared
// conditions state — the round-trip axis).
func newRecheckCondStore() *recheckCondStore {
	validAt := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	return &recheckCondStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "topic, tags, conditions, valid_at"):
			// the report row (4 columns) — serves the round-trip state.
			return [][]any{{"r96-재점검-데모", ":adr::vol:immutable:", currentConditions, validAt}}, nil
		case strings.Contains(sql, "SELECT id FROM knowledge"):
			// the write precheck (the active target); 99 is absent.
			if len(args) > 0 && args[0] == 99 {
				return nil, nil
			}
			return [][]any{{int32(7)}}, nil
		}
		return nil, nil
	}}}}
}

// currentConditions — the round-trip state the report fake serves
// (closure capture is impossible in the struct literal, so a package
// var swapped per test — the decayNow convention).
var currentConditions = ""

// TestMaintainRecheckConditionsBackfill — the v1.22.1 r96 write form:
// `--recheck N --conditions S --yes` delegates the single UPDATE
// (captured id+value), records the audit commit, answers the
// recheck_conditions JSON, and the value round-trips into the next
// read-only report (previously the report's only guidance was "set the
// write conditions field" — no write path existed).
func TestMaintainRecheckConditionsBackfill(t *testing.T) {
	shortRetry(t)
	currentConditions = ""
	t.Cleanup(func() { currentConditions = "" })

	f := newRecheckCondStore()
	// before: the empty-conditions report carries the new guidance.
	out, code := runRecheckCondArgs(t, f, "--recheck", "7")
	if code != 0 || !strings.Contains(out, "--recheck --conditions") {
		t.Fatalf("보고 exit=%d out=%s, want 0 + 새 안내문", code, out)
	}

	// the write form: delegation + commit + output.
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

	// round-trip: the report now serves the written value.
	currentConditions = "when demo holds (r96)"
	out, code = runRecheckCondArgs(t, f, "--recheck", "7")
	if code != 0 {
		t.Fatalf("갱신 후 보고 exit=%d: %s", code, out)
	}
	if !strings.Contains(out, "when demo holds (r96)") || strings.Contains(out, `"conditions_empty":true`) {
		t.Fatalf("왕복 실패: %s", out)
	}
}

// TestMaintainRecheckConditionsGuards — the confirmation and shape
// guards: without --yes a guidance error (no write); an over-cap value
// (the write-path 500-rune cap, one definition) is a schema error exit 3;
// an empty value is legal (clearing the tag); --conditions under a
// foreign mode is ownership-rejected; --yes without --conditions under
// --recheck stays rejected (the read-only form earns no confirmation);
// a missing target is ErrNotFound exit 1 before any write.
func TestMaintainRecheckConditionsGuards(t *testing.T) {
	shortRetry(t)
	currentConditions = ""
	t.Cleanup(func() { currentConditions = "" })

	// missing --yes — the confirmation guidance, no delegation, no commit.
	f := newRecheckCondStore()
	out, code := runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", "demo")
	if code != 1 || !strings.Contains(out, "--yes") {
		t.Fatalf("--yes 부재 exit=%d out=%s, want 1+안내", code, out)
	}
	if len(f.condCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("가드 전 쓰기: %v %v", f.condCalls, f.commits)
	}

	// over-cap value — the shared MaxConditionsRunes cap (exit 3, no write).
	f = newRecheckCondStore()
	over := strings.Repeat("른", knowledge.MaxConditionsRunes+1)
	out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", over, "--yes")
	if code != 3 || !strings.Contains(out, "conditions") {
		t.Fatalf("길이 초과 exit=%d out=%s, want 3+conditions 필드", code, out)
	}
	if len(f.condCalls) != 0 {
		t.Fatalf("길이 초과 쓰기: %v", f.condCalls)
	}

	// empty value is legal — clearing the tag (the optional-field convention).
	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--conditions", "", "--yes"); code != 0 {
		t.Fatalf("빈 값 exit=%d out=%s, want 0(허용)", code, out)
	}
	if len(f.condCalls) != 1 || f.condCalls[0][1] != "" {
		t.Fatalf("빈 값 위임 = %v, want 조건 빈 문자열", f.condCalls)
	}

	// --conditions under a foreign mode — ownership rejection (exit 1).
	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--stale", "--days", "5", "--conditions", "demo"); code != 1 {
		t.Fatalf("무관 모드 exit=%d out=%s, want 1", code, out)
	}

	// --yes without --conditions under --recheck — still rejected.
	f = newRecheckCondStore()
	if out, code = runRecheckCondArgs(t, f, "--recheck", "7", "--yes"); code != 1 {
		t.Fatalf("--recheck --yes(쓰기 아님) exit=%d out=%s, want 1", code, out)
	}

	// missing target — ErrNotFound exit 1 before any write.
	f = newRecheckCondStore()
	out, code = runRecheckCondArgs(t, f, "--recheck", "99", "--conditions", "demo", "--yes")
	if code != 1 || !strings.Contains(out, "not found") {
		t.Fatalf("부재 exit=%d out=%s, want 1+not found", code, out)
	}
	if len(f.condCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("부재 경로 쓰기: %v %v", f.condCalls, f.commits)
	}
}

// TestMaintainIDParseGuards — the v1.22.1 r98 parse guards:
// parseIDList rejects DUPLICATE tokens (a duplicate degenerated the
// write paths into a rows-affected race — SQL ANY() dedups, the Go
// slice does not — re-applying and re-recording through every retry)
// and over-int4 values; the --id/--recheck parses carry the same int4
// ceiling (a MaxInt64-scale id broke pgx encoding as an internal
// error); the int4 ceiling value itself parses fine.
func TestMaintainIDParseGuards(t *testing.T) {
	// parseIDList: duplicates rejected.
	if _, err := parseIDList("5,5"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("--ids 5,5: err = %v, want duplicate rejection", err)
	}
	if _, err := parseIDList("5,6,5"); err == nil {
		t.Fatalf("--ids 5,6,5: 중복 미거부")
	}
	// unique lists still parse (order preserved).
	ids, err := parseIDList(" 7, 8 ")
	if err != nil || len(ids) != 2 || ids[0] != 7 || ids[1] != 8 {
		t.Fatalf("정상 목록 파싱 = %v %v", ids, err)
	}
	// the int4 ceiling: MaxInt64 rejected, MaxInt32 passes.
	if _, err := parseIDList("2147483648"); err == nil || !strings.Contains(err.Error(), "int4") {
		t.Fatalf("int4 초과: err = %v, want int4 ceiling 거부", err)
	}
	if ids, err = parseIDList("2147483647"); err != nil || ids[0] != 2147483647 {
		t.Fatalf("int4 상한값 파싱 = %v %v, want 통과", ids, err)
	}

	// RunMaintain level: --ids 5,5 is a usage error (exit 1), no write.
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
	// the single-id form still applies once (the pre-fix symptom was the
	// 2-token retry storm; 1 token must keep working).
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
	// the ceiling at the RunMaintain parse level: --recheck and --id.
	for _, args := range [][]string{
		{"--recheck", "2147483648"},
		{"--recheck", "9223372036854775807"},
		{"--reaffirm", "--id", "2147483648", "--yes"},
	} {
		if out, code = runReaffirmArgs(t, f, args...); code != 1 || !strings.Contains(out, "int4") {
			t.Fatalf("%v exit=%d out=%s, want 1+int4 ceiling", args, code, out)
		}
	}
	// the ceiling value itself passes the parse (id found → normal flow).
	f3 := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // not found → ErrNotFound, past the parse guard
	}}}
	if out, code = runMaintainArgs(t, f3, "--recheck", "2147483647"); code != 1 || !strings.Contains(out, "not found") {
		t.Fatalf("int4 상한 id exit=%d out=%s, want 1+not found(파싱 통과)", code, out)
	}
}

// TestMaintainReaffirmPrecheckInvalidAt — the r96-4 diagnostic fix: the
// reaffirm precheck SELECT carries invalid_at IS NULL, so an
// active-status row WITH invalid_at set (the single definition of
// active excludes it) is named "nonexistent or not active" up front
// instead of slipping past the precheck and surfacing as a
// rows-affected race. The fake honors the SQL shape: it returns the
// tainted row only when the guard is absent (pre-fix behavior).
func TestMaintainReaffirmPrecheckInvalidAt(t *testing.T) {
	shortRetry(t)
	f := &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "status") {
			if strings.Contains(sql, "invalid_at IS NULL") {
				// the guarded precheck sees only the truly-active row.
				return [][]any{{int32(7), "active"}}, nil
			}
			// the unguarded precheck (pre-fix) saw the tainted row too.
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
