package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
)

// fakeMaint — a fakeStore with the admin operations (Counts/Invalidate/Supersede/Merge) layered on.
// Transition updates (invalidate, supersede) are captured via the Repo delegation methods
// (UPDATE literals are owned by the db layer).
type fakeMaint struct {
	*fakeStore

	counts    db.Counts
	countsErr error

	dupGroups      []db.DupGroup
	dupGroupsErr   error
	dupGroupsCalls []int

	invalErr     error
	invalSeqErrs []error // pre-set errors per call ordinal (retry simulation)
	supersedeErr error
	mergeErr     error
	pruneErr     error // PruneInvalidated pre-set error

	invalidateCalls  [][]int
	invalidateMsgs   []string
	supersedeCalls   []supersedeCall
	mergeCalls       [][2]int
	pruneCalls       []time.Time // PruneInvalidated delegation capture
	recallPruneCalls []time.Time // PruneRecallEvents delegation capture
}

// supersedeCall — Supersede delegation capture (old, new, and invalid_at values).
type supersedeCall struct {
	oldID, newID int
	at           time.Time
}

func (f *fakeMaint) Counts(ctx context.Context) (db.Counts, error) {
	return f.counts, f.countsErr
}

func (f *fakeMaint) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	f.dupGroupsCalls = append(f.dupGroupsCalls, limit)
	return f.dupGroups, f.dupGroupsErr
}

func (f *fakeMaint) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	call := len(f.invalidateCalls)
	f.invalidateCalls = append(f.invalidateCalls, ids)
	f.invalidateMsgs = append(f.invalidateMsgs, reason)
	if call < len(f.invalSeqErrs) {
		return 0, f.invalSeqErrs[call]
	}
	if f.invalErr != nil {
		return 0, f.invalErr
	}
	return int64(len(ids)), nil
}

func (f *fakeMaint) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	f.supersedeCalls = append(f.supersedeCalls, supersedeCall{oldID, newID, at})
	if f.supersedeErr != nil {
		return 0, f.supersedeErr
	}
	return 1, nil
}

func (f *fakeMaint) Merge(ctx context.Context, fromID, toID int) error {
	f.mergeCalls = append(f.mergeCalls, [2]int{fromID, toID})
	return f.mergeErr
}

// PruneInvalidated — Repo delegation-method capture (return = the 2-target-row
// convention — same as the maintain_v0330 prune contract).
func (f *fakeMaint) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	f.pruneCalls = append(f.pruneCalls, before)
	if f.pruneErr != nil {
		return 0, f.pruneErr
	}
	return 2, nil
}

// PruneRecallEvents — Repo delegation-method capture (return = the 0-row convention —
// expired-event counts are set up via queryFn count queries).
func (f *fakeMaint) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	f.recallPruneCalls = append(f.recallPruneCalls, before)
	return 0, nil
}

func runMaintainArgs(t *testing.T, f *fakeMaint, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runMaintain(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// boundary — pending 49/50 is ok; 51 onward is review-needed.
// The threshold is read from meta (default pending_max=50).
func TestMaintainCheckBoundary(t *testing.T) {
	cases := []struct {
		name    string
		pending int
		want    string
	}{
		{"49 ok", 49, "ok"},
		{"50 경계 ok", 50, "ok"},
		{"51 review-needed", 51, "review-needed"},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{Pending: c.pending}}
		out, code := runMaintainArgs(t, f, "--check")
		if code != 0 {
			t.Errorf("%s: exit=%d, want 0", c.name, code)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
			t.Fatalf("%s: JSON 아님: %q", c.name, out)
		}
		if m["status"] != c.want {
			t.Errorf("%s: status=%v, want %q", c.name, m["status"], c.want)
		}
		if c.want == "review-needed" {
			if got, _ := m["pending"].(float64); int(got) != c.pending {
				t.Errorf("%s: pending 필드=%v, want %d", c.name, m["pending"], c.pending)
			}
		}
	}
}

// dup_suspect boundary (default 20) — 20 ok, 21 review-needed.
func TestMaintainCheckDupBoundary(t *testing.T) {
	for _, c := range []struct {
		dup  int
		want string
	}{{20, "ok"}, {21, "review-needed"}} {
		f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{DupSuspect: c.dup}}
		out, code := runMaintainArgs(t, f, "--check")
		if code != 0 {
			t.Errorf("dup=%d: exit=%d, want 0", c.dup, code)
		}
		if !strings.Contains(out, `"status":"`+c.want+`"`) {
			t.Errorf("dup=%d: 출력 %q, want %q", c.dup, out, c.want)
		}
	}
}

// thresholds are read from meta — with pending_max=5, pending 6 is review-needed.
func TestMaintainCheckThresholdsFromMeta(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			return [][]any{{"pending_max", "5"}}, nil
		},
	}, counts: db.Counts{Pending: 6}}
	out, code := runMaintainArgs(t, f, "--check")
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"review-needed"`) {
		t.Errorf("meta 임계값 미반영: %q", out)
	}
}

// the review-needed report includes both counts and exits 0.
func TestMaintainCheckReportFields(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{Pending: 60, DupSuspect: 25}}
	out, code := runMaintainArgs(t, f, "--check")
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m["pending"] != float64(60) || m["dup_suspect"] != float64(25) {
		t.Errorf("필드 누락: %q", out)
	}
}

// --invalidate --id N --reason — update-path call + exit 0.
func TestMaintainInvalidateOK(t *testing.T) {
	// the pre-check (existence and invalid_at lookup) goes through Query, so
	// seed 1 active row (the first-invalidation path, distinct from the idempotent no-op).
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT invalid_at FROM knowledge") {
			return [][]any{{nil}}, nil // id 7: active(invalid_at NULL)
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--invalidate", "--id", "7", "--reason", "중복")
	if code != 0 {
		t.Errorf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.invalidateCalls) != 1 || len(f.invalidateCalls[0]) != 1 || f.invalidateCalls[0][0] != 7 {
		t.Errorf("Invalidate 호출 = %v, want [[7]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 1 || f.invalidateMsgs[0] != "중복" {
		t.Errorf("reason 전달 누락: %v", f.invalidateMsgs)
	}
	if !strings.Contains(out, `"status":"invalidated"`) {
		t.Errorf("출력 %q", out)
	}
}

// invalidating a missing id — ErrNotFound -> exit 1 one-line JSON.
func TestMaintainInvalidateNotFound(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, invalErr: db.ErrNotFound}
	out, code := runMaintainArgs(t, f, "--invalidate", "--id", "99", "--reason", "x")
	if code != 1 {
		t.Errorf("exit=%d, want 1", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}
}

// --merge --from N --to M — merge-path call + exit 0.
func TestMaintainMergeOK(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--merge", "--from", "3", "--to", "9")
	if code != 0 {
		t.Errorf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.mergeCalls) != 1 || f.mergeCalls[0] != [2]int{3, 9} {
		t.Errorf("Merge 호출 = %v, want [[3 9]]", f.mergeCalls)
	}
	if !strings.Contains(out, `"status":"merged"`) {
		t.Errorf("출력 %q", out)
	}
}

// missing merge target — ErrNotFound -> exit 1.
func TestMaintainMergeNotFound(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, mergeErr: db.ErrNotFound}
	_, code := runMaintainArgs(t, f, "--merge", "--from", "1", "--to", "2")
	if code != 1 {
		t.Errorf("exit=%d, want 1", code)
	}
}

// no mode given (bare usage) is invalid-arg exit 3; a missing argument within a mode
// remains a usage error exit 1.
func TestMaintainUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{}, 3},                         // bare usage 3 (previously 1)
		{[]string{"--id", "5"}, 3},              // no mode -> bare usage 3
		{[]string{"--invalidate"}, 1},           // no --id
		{[]string{"--merge", "--from", "3"}, 1}, // no --to
		{[]string{"--check", "--id", "abc"}, 1}, // not a number — the id is ignored by check but parsing must still fail
	}
	// invalid flag (--bogus) is exit 3 (the invalid-arg contract) —
	// covered by TestUnknownFlagExitContract (textutil_test.go).
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		out, code := runMaintainArgs(t, f, c.args...)
		if code != c.want {
			t.Errorf("%v: exit=%d, want %d (출력 %q)", c.args, code, c.want, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%v: 에러 JSON 아님: %q", c.args, out)
		}
	}
}

// Counts failure — errLine propagation, MapExit code.
func TestMaintainCheckCountError(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, countsErr: errors.New("boom")}
	out, code := runMaintainArgs(t, f, "--check")
	if code == 0 {
		t.Errorf("exit=0, want != 0 (출력 %q)", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("에러 미전파: %q", out)
	}
}

// registration check — maintain must be in the registry for Run to dispatch it.
func TestMaintainRegistered(t *testing.T) {
	if _, ok := Lookup("maintain"); !ok {
		t.Fatal("maintain 미등록")
	}
}

// schema-version gate — realStore must satisfy MaintainStore (compile-time guarantee).
var _ MaintStore = (*realStore)(nil)

var _ = store.DefaultThresholds // keep the import (for future extension)

// TestMaintainSupersedeCycleErrorExit — the cycle precheck rejection
// surfaces as exit 1 with the diagnostic wording, and the write is
// never attempted (the guard itself — db.SupersedeCycleSQL — is
// verified against a real DB in the slow test below; this pins the cli
// wiring: precheck answer → rejection before the Retry-wrapped write).
func TestMaintainSupersedeCycleErrorExit(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "WITH RECURSIVE") {
			return [][]any{{true}}, nil // cycle detected by the lineage walk
		}
		if strings.Contains(sql, "SELECT status FROM knowledge") {
			// precheck: OLD active, NEW exists (valid_at non-nil).
			return [][]any{{"active", time.Now()}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 1 {
		t.Errorf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "would create a supersede cycle") {
		t.Errorf("진단 문구 미전파: %q", out)
	}
	if len(f.supersedeCalls) != 0 {
		t.Errorf("사이클 거부 후 Supersede 호출됨: %v", f.supersedeCalls)
	}
}

// TestMaintainSupersedeCycleRealSlow — real-DB round trip of the guard
// at the cli boundary: A→B succeeds, the reverse B→A and the reverse
// merge are rejected (exit 1 + diagnostic), self keeps its invalid-arg
// exit 3. Only when SUPERLITE_REAL=1 + FLINT_TEST_DSN are set.
func TestMaintainSupersedeCycleRealSlow(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	st, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(st.Close)
	const marker = "demo-supersede-cycle-slow"
	cleanup := func() {
		if _, err := st.Query(ctx, `DELETE FROM knowledge WHERE origin = $1`, marker); err != nil {
			t.Fatalf("정리 실패: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	rows := []knowledge.ExportRow{
		{ID: 910101, Topic: "demo cycle alpha", Content: "합성 데모 사이클 alpha — 실DB 가드 검증용.",
			Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
			Domain: "main", Source: marker, CreatedAt: t0, UpdatedAt: t0},
		{ID: 910102, Topic: "demo cycle beta", Content: "합성 데모 사이클 beta — 실DB 가드 검증용.",
			Kind: "knowledge", Status: "active", Confidence: 0.8, Tags: []string{"demo"},
			Domain: "main", Source: marker, CreatedAt: t0, UpdatedAt: t0},
		{ID: 910103, Topic: "demo cycle gamma", Content: "합성 데모 사이클 gamma — 실DB 가드 검증용.",
			Kind: "knowledge", Status: "active", Confidence: 0.7, Tags: []string{"demo"},
			Domain: "main", Source: marker, CreatedAt: t0, UpdatedAt: t0},
	}
	data, err := knowledge.FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	var seed bytes.Buffer
	if code := RunImportMigrate(st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &seed); code != 0 {
		t.Fatalf("seed exit=%d; out=%s", code, seed.String())
	}

	run := func(args ...string) (string, int) {
		t.Helper()
		var out bytes.Buffer
		code := runMaintain(ctx, st, args, strings.NewReader(""), &out)
		return out.String(), code
	}
	// A→B→C — a normal 3-hop chain extends (NEW's status unrestricted:
	// B is mid-chain when C supersedes it).
	for _, step := range [][2]string{
		{"910101", "910102"}, {"910102", "910103"},
	} {
		if out, code := run("--supersede", step[0], "--by", step[1]); code != 0 {
			t.Fatalf("체인 %s→%s exit=%d, want 0; out=%s", step[0], step[1], code, out)
		}
	}
	// Closing the loop C→A is rejected: exit 1 + diagnostic.
	out, code := run("--supersede", "910103", "--by", "910101")
	if code != 1 {
		t.Fatalf("C→A exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "would create a supersede cycle") {
		t.Fatalf("C→A 진단 문구 없음: %s", out)
	}
	// The reverse merge hits the same guard inside db.Repo.Merge.
	out, code = run("--merge", "--from", "910103", "--to", "910101")
	if code != 1 {
		t.Fatalf("역방향 merge exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "would create a supersede cycle") {
		t.Fatalf("역방향 merge 진단 문구 없음: %s", out)
	}
	// self keeps the preexisting invalid-arg exit 3 (checked before any
	// guard query).
	if out, code = run("--supersede", "910101", "--by", "910101"); code != 3 {
		t.Fatalf("self exit=%d, want 3; out=%s", code, out)
	}
}

// TestMaintainCheckDrillReport — last_drill elapsed reporting: no record -> never,
// over 30 days -> stale, within -> field absent (a fast regression pending the table-commit decision).
func TestMaintainCheckDrillReport(t *testing.T) {
	stale := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	cases := []struct {
		name string
		last string
		want string // "" = no drill field
	}{
		{"기록 없음", "", `"drill":"never"`},
		{"40일 경과", stale, `"drill":"stale"`},
		{"1일 전", fresh, ""},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{Pending: 1}}
		f.queryFn = func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "last_drill") {
				if c.last == "" {
					return nil, nil
				}
				return [][]any{{"last_drill", c.last}}, nil
			}
			return nil, nil // thresholds unset -> defaults
		}
		out, code := runMaintainArgs(t, f, "--check")
		if code != 0 {
			t.Fatalf("%s: exit=%d, want 0; out=%s", c.name, code, out)
		}
		if c.want == "" {
			if strings.Contains(out, "drill") {
				t.Fatalf("%s: 이내 경과인데 drill 필드 있음: %q", c.name, out)
			}
			continue
		}
		if !strings.Contains(out, c.want) {
			t.Fatalf("%s: 출력 %q, want %q 포함", c.name, out, c.want)
		}
	}
}
