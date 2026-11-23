package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
)

type fakeMaint struct {
	*fakeStore

	counts    db.Counts
	countsErr error

	dupGroups      []db.DupGroup
	dupGroupsErr   error
	dupGroupsCalls []int

	invalErr     error
	invalSeqErrs []error
	supersedeErr error
	mergeErr     error
	pruneErr     error

	invalidateCalls  [][]int
	invalidateMsgs   []string
	supersedeCalls   []supersedeCall
	mergeCalls       [][2]int
	pruneCalls       []time.Time
	recallPruneCalls []time.Time
	reaffirmCalls    [][]int
}

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

func (f *fakeMaint) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	f.pruneCalls = append(f.pruneCalls, before)
	if f.pruneErr != nil {
		return 0, f.pruneErr
	}
	return 2, nil
}

func (f *fakeMaint) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	f.recallPruneCalls = append(f.recallPruneCalls, before)
	return 0, nil
}

func (f *fakeMaint) Reaffirm(ctx context.Context, ids []int) (int64, error) {
	f.reaffirmCalls = append(f.reaffirmCalls, ids)
	return int64(len(ids)), nil
}

func runMaintainArgs(t *testing.T, f *fakeMaint, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runMaintain(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

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

func TestMaintainInvalidateOK(t *testing.T) {

	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT invalid_at FROM knowledge") {
			return [][]any{{nil}}, nil
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

func TestMaintainMergeNotFound(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, mergeErr: db.ErrNotFound}
	_, code := runMaintainArgs(t, f, "--merge", "--from", "1", "--to", "2")
	if code != 1 {
		t.Errorf("exit=%d, want 1", code)
	}
}

func TestMaintainUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{}, 3},
		{[]string{"--id", "5"}, 3},
		{[]string{"--invalidate"}, 1},
		{[]string{"--merge", "--from", "3"}, 1},
		{[]string{"--check", "--id", "abc"}, 1},
	}

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

func TestMaintainRegistered(t *testing.T) {
	if _, ok := Lookup("maintain"); !ok {
		t.Fatal("maintain 미등록")
	}
}

var _ MaintStore = (*realStore)(nil)

var _ = store.DefaultThresholds

func TestMaintainSupersedeCycleErrorExit(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "WITH RECURSIVE") {
			return [][]any{{true}}, nil
		}
		if strings.Contains(sql, "SELECT status FROM knowledge") {

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

	for _, step := range [][2]string{
		{"910101", "910102"}, {"910102", "910103"},
	} {
		if out, code := run("--supersede", step[0], "--by", step[1]); code != 0 {
			t.Fatalf("체인 %s→%s exit=%d, want 0; out=%s", step[0], step[1], code, out)
		}
	}

	out, code := run("--supersede", "910103", "--by", "910101")
	if code != 1 {
		t.Fatalf("C→A exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "would create a supersede cycle") {
		t.Fatalf("C→A 진단 문구 없음: %s", out)
	}

	out, code = run("--merge", "--from", "910103", "--to", "910101")
	if code != 1 {
		t.Fatalf("역방향 merge exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "would create a supersede cycle") {
		t.Fatalf("역방향 merge 진단 문구 없음: %s", out)
	}

	if out, code = run("--supersede", "910101", "--by", "910101"); code != 3 {
		t.Fatalf("self exit=%d, want 3; out=%s", code, out)
	}
}

func TestMaintainReaffirmDelegation(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{{int32(5), "active"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--reaffirm", "--id", "5", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.reaffirmCalls) != 1 || len(f.reaffirmCalls[0]) != 1 || f.reaffirmCalls[0][0] != 5 {
		t.Errorf("Reaffirm 호출 = %v, want [[5]]", f.reaffirmCalls)
	}
	if len(f.invalidateCalls) != 0 {
		t.Errorf("무효화 경로 오염: %v", f.invalidateCalls)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "reaffirm") {
		t.Errorf("감사 커밋 = %v", f.commits)
	}
	if !strings.Contains(out, `"status":"reaffirmed"`) {
		t.Errorf("출력 %q", out)
	}

	f2 := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		return nil, nil
	}}}
	out, code = runMaintainArgs(t, f2, "--reaffirm", "--id", "5")
	if code != 1 || !strings.Contains(out, "--yes") {
		t.Fatalf("--yes 부재 exit=%d out=%q, want 1+안내", code, out)
	}
	if len(f2.reaffirmCalls) != 0 || len(f2.commits) != 0 {
		t.Errorf("가드 전 쓰기: %v %v", f2.reaffirmCalls, f2.commits)
	}
}

func TestMaintainReaffirmRealSlow(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	name := fmt.Sprintf("flint_t3raf_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	base.Close()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	st, err := newRealStore(ctx, u.String())
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(st.Close)

	seed := func(hash string) map[string]any {
		return map[string]any{
			"kind":         "knowledge",
			"topic":        "t3-reaffirm-demo-" + hash,
			"summary":      "합성 데모 재확신 행-" + hash,
			"tags":         ":adr:",
			"confidence":   0.9,
			"content_hash": hash,
			"status":       "active",
		}
	}
	ids, err := st.InsertBatch(ctx, []map[string]any{seed("t3-raf-a"), seed("t3-raf-b")})
	if err != nil {
		t.Fatalf("시드: %v", err)
	}

	for _, id := range ids {
		if _, err := st.Query(ctx,
			`UPDATE knowledge SET valid_at = now() - interval '100 days' WHERE id = $1`, id); err != nil {
			t.Fatalf("valid_at 시드 id=%d: %v", id, err)
		}
	}
	run := func(args ...string) (string, int) {
		t.Helper()
		var out bytes.Buffer
		code := runMaintain(ctx, st, args, strings.NewReader(""), &out)
		return out.String(), code
	}

	out, code := run("--reaffirm", "--id", fmt.Sprint(ids[0]), "--yes")
	if code != 0 {
		t.Fatalf("reaffirm exit=%d out=%s", code, out)
	}
	rows, err := st.Query(ctx, `SELECT valid_at FROM knowledge WHERE id = $1`, ids[0])
	if err != nil || len(rows) == 0 {
		t.Fatalf("valid_at 조회: %v", err)
	}
	va, ok2 := rows[0][0].(time.Time)
	if !ok2 || time.Since(va) > 2*time.Minute {
		t.Fatalf("valid_at 리셋 실패: %v", rows[0][0])
	}
	var runs int
	if err := scanCount(ctx, st, `SELECT count(*) FROM maintenance_runs WHERE kind = 'reaffirm'`, &runs); err != nil {
		t.Fatalf("maintenance_runs 조회: %v", err)
	}
	if runs != 1 {
		t.Fatalf("maintenance_runs(reaffirm) = %d, want 1", runs)
	}

	out, code = run("--reaffirm", "--ids", fmt.Sprintf("%d,%d", ids[0], ids[1]), "--yes")
	if code != 0 || !strings.Contains(out, `"count":2`) {
		t.Fatalf("--ids reaffirm exit=%d out=%s", code, out)
	}
	if err := scanCount(ctx, st, `SELECT count(*) FROM maintenance_runs WHERE kind = 'reaffirm'`, &runs); err != nil || runs != 2 {
		t.Fatalf("maintenance_runs 누적 = %d, want 2 (err %v)", runs, err)
	}

	out, code = run("--reaffirm", "--id", "999999", "--yes")
	if code != 1 {
		t.Fatalf("부재 exit=%d, want 1 — %s", code, out)
	}
	if err := scanCount(ctx, st, `SELECT count(*) FROM maintenance_runs WHERE kind = 'reaffirm'`, &runs); err != nil || runs != 2 {
		t.Fatalf("부재 후 maintenance_runs = %d, want 2 (err %v)", runs, err)
	}

	out, code = run("--recheck", fmt.Sprint(ids[1]))
	if code != 0 || !strings.Contains(out, `"conditions_empty":true`) {
		t.Fatalf("recheck exit=%d out=%s", code, out)
	}
}

func scanCount(ctx context.Context, st Store, sql string, dst *int) error {
	rows, err := st.Query(ctx, sql)
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return fmt.Errorf("count 행 부재: %s", sql)
	}
	switch v := rows[0][0].(type) {
	case int64:
		*dst = int(v)
	case int32:
		*dst = int(v)
	default:
		return fmt.Errorf("count 파싱 실패: %T %v", rows[0][0], rows[0][0])
	}
	return nil
}

func TestMaintainCheckDrillReport(t *testing.T) {
	stale := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	cases := []struct {
		name string
		last string
		want string
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
			return nil, nil
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

func TestMaintainMergeAuditDelegation(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "invalid_at IS NULL AND status = 'active' LIMIT") {
			return [][]any{
				{int32(7), "데모 배포 절차", "매주 화요일 배포 프리즈와 롤백 점검을 함께 기록한다"},
				{int32(8), "데모 배포 절차", "매주 화요일 배포 프리즈와 롤백 점검을 함께 기록한다"},
			}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--merge-audit")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"status":"merge-audit"`) || !strings.Contains(out, `"a":7,"b":8`) {
		t.Errorf("출력 %q", out)
	}
	if !strings.Contains(out, "dispose via maintain --merge/--supersede (approval required)") {
		t.Errorf("처분 안내 부재: %q", out)
	}
	if len(f.invalidateCalls) != 0 || len(f.mergeCalls) != 0 || len(f.supersedeCalls) != 0 || len(f.commits) != 0 {
		t.Errorf("읽기 전용 위반: invalidate=%v merge=%v supersede=%v commits=%v",
			f.invalidateCalls, f.mergeCalls, f.supersedeCalls, f.commits)
	}

	out, code = runMaintainArgs(t, f, "--merge-audit", "--limit", "5")
	if code != 0 || !strings.Contains(out, `"status":"merge-audit"`) {
		t.Fatalf("--limit 결합 exit=%d out=%q", code, out)
	}
}

func TestMaintainRecallAuditDelegation(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, _ []any) ([][]any, error) {
		if strings.Contains(sql, "AS bad_recall") {
			return [][]any{
				{int32(9), "t4-recall-audit 데모 배포 절차", int64(2)},
			}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"status":"recall-audit"`) || !strings.Contains(out, `"id":9`) {
		t.Errorf("출력 %q", out)
	}
	if !strings.Contains(out, "dispose via maintain --invalidate/--supersede/--reaffirm (approval required)") {
		t.Errorf("처분 안내 부재: %q", out)
	}
	if len(f.invalidateCalls) != 0 || len(f.mergeCalls) != 0 || len(f.supersedeCalls) != 0 || len(f.commits) != 0 {
		t.Errorf("읽기 전용 위반: invalidate=%v merge=%v supersede=%v commits=%v",
			f.invalidateCalls, f.mergeCalls, f.supersedeCalls, f.commits)
	}

	out, code = runMaintainArgs(t, f, "--recall-audit", "--limit", "5")
	if code != 0 || !strings.Contains(out, `"status":"recall-audit"`) {
		t.Fatalf("--limit 결합 exit=%d out=%q", code, out)
	}
}

func TestMaintainGCFlagModeGuardCLI(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil },
	}}
	out, code := runMaintainArgs(t, f, "--gc")
	if code != 1 {
		t.Fatalf("--gc(무 --yes) exit=%d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "--gc requires --yes") {
		t.Errorf("--yes 가이드 문구 없음: %s", out)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_gc") {
			t.Errorf("가드 실패 전 엔진 호출 관통: %s", q)
		}
	}
}

func TestMaintainGCRealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(base.Close)
	name := fmt.Sprintf("flint_v1250_gc_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
	}
	u.Path = "/" + name
	st, err := newRealStore(ctx, u.String())
	if err != nil {
		t.Fatalf("newRealStore: %v", err)
	}
	t.Cleanup(st.Close)

	var out strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--gc", "--yes"}, nil, &out); code != 0 {
		t.Fatalf("maintain --gc --yes exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "disconnect") || !strings.Contains(out.String(), `"status":"gc"`) {
		t.Fatalf("GC 출력 계약 불일치: %s", out.String())
	}

	rows, err := st.Query(ctx, `SELECT key, value FROM meta WHERE key IN ('last_gc_at','last_gc_mode')`)
	if err != nil || len(rows) != 2 {
		t.Fatalf("meta 2키 왕복 실패: %v rows=%d", err, len(rows))
	}
	vals := map[string]string{}
	for _, r := range rows {
		vals[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
	}
	if vals["last_gc_mode"] != "full" {
		t.Errorf("last_gc_mode=%q, want full", vals["last_gc_mode"])
	}
	if _, perr := time.Parse(time.RFC3339, vals["last_gc_at"]); perr != nil {
		t.Errorf("last_gc_at 비RFC3339: %q", vals["last_gc_at"])
	}

	var sOut strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--gc", "--shallow", "--yes"}, nil, &sOut); code != 0 {
		t.Fatalf("maintain --gc --shallow --yes exit=%d: %s", code, sOut.String())
	}
	if !strings.Contains(sOut.String(), `"mode":"shallow"`) {
		t.Fatalf("shallow 결과 JSON 불일치: %s", sOut.String())
	}
	sRows, err := st.Query(ctx, `SELECT value FROM meta WHERE key = 'last_gc_mode'`)
	if err != nil || len(sRows) != 1 || fmt.Sprint(sRows[0][0]) != "shallow" {
		t.Fatalf("last_gc_mode shallow 왕복 실패: %v %v", sRows, err)
	}

	for _, check := range []string{"dolt_" + "log_depth", "db_size"} {
		var dOut strings.Builder
		if code := ops.RunDoctor(ctx, st, []string{"--check", check}, nil, &dOut); code != 0 {
			t.Errorf("doctor --check %s exit=%d: %s", check, code, dOut.String())
		}
		if !strings.Contains(dOut.String(), `"name":"`+check+`"`) {
			t.Errorf("doctor --check %s 결과 미표시: %s", check, dOut.String())
		}
	}
}
