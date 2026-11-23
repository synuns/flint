package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func init() {
	CurrentVersionFn = func() string { return "" }
	ValidVersionFn = func(string) bool { return false }
	FetchLatestVersionFn = func() (string, error) { return "", errors.New("offline") }
	CompareVersionsFn = func(a, b string) int { return strings.Compare(a, b) }
	KnownMetaKeyFn = func(string) bool { return false }
	AnyToTimeFn = func(v any) (time.Time, bool) {
		t, ok := v.(time.Time)
		return t, ok
	}
	IsConflictGroupFn = func(db.ConflictGroup, float64) bool { return false }
	ConflictDupThresholdFn = func() float64 { return store.DefaultThresholds().ConflictDupThreshold }
	BackupEncryptOnFn = func(string) bool { return false }
	BackupKeyPathFn = func() string { return "" }
	SkillSrcDirFn = func(string) string { return "" }
	CheckSkillFileFn = func(string, string) (DoctorCheck, bool) { return DoctorCheck{}, false }
}

type fakeStore struct {
	wire.HistoryStubs

	exists        bool
	existsErr     error
	existsProject map[string]bool
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)

	inserted []map[string]any
	commits  []string
	queries  []string
	qmu      sync.Mutex

	metaDeleted   []string
	metaDeleteErr error
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	f.inserted = append(f.inserted, recs...)
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	ids := make([]int, len(recs))
	for i := range ids {
		ids[i] = i + 1
	}
	return ids, nil
}
func (f *fakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return f.exists, f.existsErr
}
func (f *fakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return f.existsProject[name], nil
}
func (f *fakeStore) Commit(ctx context.Context, msg string) (string, error) {
	f.commits = append(f.commits, msg)
	if f.commitErr != nil {
		return "", f.commitErr
	}
	return "abc123", nil
}
func (f *fakeStore) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeStore) Close()                         {}

func (f *fakeStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	f.metaDeleted = append(f.metaDeleted, keys...)
	if f.metaDeleteErr != nil {
		return 0, f.metaDeleteErr
	}
	return int64(len(keys)), nil
}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {

	f.qmu.Lock()
	f.queries = append(f.queries, sql)
	f.qmu.Unlock()
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return f.rows, nil
}

func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := ComposePsFn
	ComposePsFn = fn
	t.Cleanup(func() { ComposePsFn = old })
}

func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := RunDoctor(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

func doctorQueryFn(lastWrite, lastHot, lastRead time.Time) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"4"}}, nil
		case strings.Contains(sql, "pg_indexes"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "last_write_at"):
			return [][]any{
				{"last_write_at", lastWrite.UTC().Format(time.RFC3339)},
				{"last_hot_at", lastHot.UTC().Format(time.RFC3339)},
				{"last_read_at", lastRead.UTC().Format(time.RFC3339)},
			}, nil
		case strings.Contains(sql, "max(recalled_at) FROM recall_events"):
			return [][]any{{lastRead}}, nil
		case strings.Contains(sql, "max(ts) FROM read_events"),
			strings.Contains(sql, "max(ts) FROM hot_events"),
			strings.Contains(sql, "max(ts) FROM knowledge_reads"),
			strings.Contains(sql, "max(ts) FROM session_starts"):
			return [][]any{{lastRead}}, nil
		case strings.Contains(sql, "last_threshold_snapshot_at"):
			return [][]any{
				{"last_threshold_snapshot_at", lastRead.UTC().Format(time.RFC3339)},
				{"last_rollup_at", lastRead.UTC().Format(time.RFC3339)},
			}, nil
		}
		return nil, nil
	}
}

func freshDoctorFn() func(string, []any) ([][]any, error) {
	h := time.Now().Add(-time.Hour)
	return doctorQueryFn(h, h, h)
}

func TestDoctorChecksContract(t *testing.T) {
	want := []string{"hook-file", "db", "schema", "integrity", "last-seen", "port"}
	got := DoctorChecks()
	if len(got) != len(want) {
		t.Fatalf("DoctorChecks() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DoctorChecks()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDoctorRegistered(t *testing.T) {
	if _, ok := wire.Lookup("doctor"); !ok {
		t.Fatal("doctor 미등록")
	}
}

func TestDoctorListChecks(t *testing.T) {
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--list-checks")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	var m struct {
		Checks []string `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if len(m.Checks) != 23 {
		t.Errorf("checks %d종, want 23종(코어6+tail17): %v", len(m.Checks), m.Checks)
	}
	if m.Checks[0] != "hook-file" || m.Checks[4] != "last-seen" || m.Checks[5] != "port" {
		t.Errorf("코어 6종 순서 회귀: %v", m.Checks)
	}
}

func TestDoctorMeteringFreshnessSpec(t *testing.T) {
	s, ok := doctorSpec("metering-freshness")
	if !ok {
		t.Fatal("등록표에 metering-freshness 부재 — A4 계약 위반")
	}
	if s.Chain != "tail" {
		t.Errorf("chain=%q, want tail", s.Chain)
	}
	if s.ThFn == nil {
		t.Error("ThFn nil — 임계 주입형(metering_freshness_warn_h)이어야 함")
	}
	if s.OnStoreNil != "warn" {
		t.Errorf("OnStoreNil=%q, want warn", s.OnStoreNil)
	}
}

func meteringFreshnessFn(overrides map[string]time.Time, errs []string) func(string, []any) ([][]any, error) {
	fresh := time.Now().Add(-time.Hour)
	times := map[string]time.Time{
		"read_events": fresh, "recall_events": fresh, "hot_events": fresh,
		"knowledge_reads": fresh, "session_starts": fresh,
		"last_threshold_snapshot_at": fresh, "last_rollup_at": fresh,
	}
	for k, v := range overrides {
		times[k] = v
	}
	failed := map[string]bool{}
	for _, k := range errs {
		failed[k] = true
	}
	return func(sql string, args []any) ([][]any, error) {
		src := ""
		switch {
		case strings.Contains(sql, "max(recalled_at) FROM recall_events"):
			src = "recall_events"
		case strings.Contains(sql, "max(ts) FROM read_events"):
			src = "read_events"
		case strings.Contains(sql, "max(ts) FROM hot_events"):
			src = "hot_events"
		case strings.Contains(sql, "max(ts) FROM knowledge_reads"):
			src = "knowledge_reads"
		case strings.Contains(sql, "max(ts) FROM session_starts"):
			src = "session_starts"
		case strings.Contains(sql, "last_threshold_snapshot_at"):

			if failed["last_threshold_snapshot_at"] || failed["last_rollup_at"] {
				return nil, errors.New("gate lookup boom")
			}
			return [][]any{
				{"last_threshold_snapshot_at", times["last_threshold_snapshot_at"].UTC().Format(time.RFC3339)},
				{"last_rollup_at", times["last_rollup_at"].UTC().Format(time.RFC3339)},
			}, nil
		default:
			return nil, nil
		}
		if failed[src] {
			return nil, errors.New("lookup boom: " + src)
		}
		if times[src].IsZero() {
			return [][]any{{nil}}, nil
		}
		return [][]any{{times[src]}}, nil
	}
}

func TestMeteringFreshnessVerdicts(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-400 * time.Hour)
	th := store.DefaultThresholds()

	run := func(fn func(string, []any) ([][]any, error), t store.Thresholds) DoctorCheck {
		return checkMeteringFreshnessTh(ctx, &fakeStore{queryFn: fn}, t)
	}

	t.Run("전 소스 신선 — ok", func(t *testing.T) {
		c := run(meteringFreshnessFn(nil, nil), th)
		if c.Status != "ok" {
			t.Fatalf("status=%q detail=%q, want ok", c.Status, c.Detail)
		}
	})
	t.Run("테이블 소스 경과 — 경고에 소스명", func(t *testing.T) {
		c := run(meteringFreshnessFn(map[string]time.Time{"hot_events": old}, nil), th)
		if c.Status != "warn" {
			t.Fatalf("status=%q, want warn", c.Status)
		}
		if !strings.Contains(c.Detail, "hot_events") {
			t.Fatalf("detail %q — 초과 소스명 미포함", c.Detail)
		}
	})
	t.Run("recall_events — recalled_at 열 사용", func(t *testing.T) {
		var sawRecalled bool
		fn := func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "max(recalled_at) FROM recall_events") {
				sawRecalled = true
				return [][]any{{time.Now().Add(-time.Hour)}}, nil
			}
			return meteringFreshnessFn(nil, nil)(sql, args)
		}
		c := run(fn, th)
		if !sawRecalled {
			t.Fatal("max(recalled_at) 조회 미관측 — ts 열 사용 의심(계약 위반)")
		}
		if c.Status != "ok" {
			t.Fatalf("status=%q detail=%q, want ok", c.Status, c.Detail)
		}
	})
	t.Run("게이트 meta 키 소스 포함", func(t *testing.T) {
		c := run(meteringFreshnessFn(map[string]time.Time{"last_rollup_at": old}, nil), th)
		if c.Status != "warn" || !strings.Contains(c.Detail, "last_rollup_at") {
			t.Fatalf("status=%q detail=%q, want warn+last_rollup_at", c.Status, c.Detail)
		}
	})
	t.Run("미기록(빈 테이블) — 경고", func(t *testing.T) {
		c := run(meteringFreshnessFn(map[string]time.Time{"read_events": {}}, nil), th)
		if c.Status != "warn" || !strings.Contains(c.Detail, "read_events") {
			t.Fatalf("status=%q detail=%q, want warn+read_events(never)", c.Status, c.Detail)
		}
	})
	t.Run("임계 오버라이드 — 판정 변화", func(t *testing.T) {

		over100h := time.Now().Add(-100 * time.Hour)
		overrides := map[string]time.Time{
			"read_events": over100h, "recall_events": over100h, "hot_events": over100h,
			"knowledge_reads": over100h, "session_starts": over100h,
			"last_threshold_snapshot_at": over100h, "last_rollup_at": over100h,
		}
		if c := run(meteringFreshnessFn(overrides, nil), th); c.Status != "ok" {
			t.Fatalf("336h 기본 임계에서 100h 경과가 warn(status=%q)", c.Status)
		}
		narrow := th
		narrow.MeteringFreshnessWarnH = 48
		if c := run(meteringFreshnessFn(overrides, nil), narrow); c.Status != "warn" {
			t.Fatalf("48h 임계에서 100h 경과가 ok — 임계 미주입 의심(status=%q)", c.Status)
		}
	})
	t.Run("일부 조회 실패 — 해당 소스만 스킵(조용히)", func(t *testing.T) {
		c := run(meteringFreshnessFn(nil, []string{"hot_events", "knowledge_reads"}), th)
		if c.Status != "ok" {
			t.Fatalf("status=%q detail=%q, want ok(실패 소스 스킵 — 원칙 3)", c.Status, c.Detail)
		}
	})
	t.Run("전체 조회 실패 — 판정 보류(ok)", func(t *testing.T) {
		c := run(meteringFreshnessFn(nil, []string{
			"read_events", "recall_events", "hot_events", "knowledge_reads",
			"session_starts", "last_rollup_at",
		}), th)
		if c.Status != "ok" {
			t.Fatalf("status=%q, want ok(전체 실패 — 판정 보류)", c.Status)
		}
	})
}

func TestDoctorCheckSubset(t *testing.T) {
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--check", "bogus")
	if code != 3 {
		t.Errorf("무효명 exit=%d, want 3 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}

	f2 := &fakeStore{queryFn: freshDoctorFn()}
	out2, code2 := runDoctorArgs(t, f2, "--check", "db")
	if code2 != 0 {
		t.Fatalf("db 서브셋 exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if strings.Count(out2, `"name"`) != 1 {
		t.Errorf("서브셋이 아님(검사 수 != 1): %q", out2)
	}
	if !strings.Contains(out2, `"db"`) {
		t.Errorf("db 검사 결과 없음: %q", out2)
	}
}

func TestDoctorHookFileFailWithFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	var m struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Fix    string `json:"fix"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m.Status != "fail" {
		t.Errorf("status=%q, want fail", m.Status)
	}
	var hookFail bool
	for _, c := range m.Checks {
		if c.Name == "hook-file" {
			if c.Status != "fail" {
				t.Errorf("hook-file status=%q, want fail", c.Status)
			}
			if c.Fix == "" {
				t.Errorf("hook-file fail에 fix 문구 없음: %q", out)
			}
			hookFail = true
		}
	}
	if !hookFail {
		t.Errorf("hook-file 검사 결과 없음: %q", out)
	}
}

func TestDoctorHookFileOkWhenRegistered(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정")
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write --source hook"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if strings.Contains(out, `"fail"`) {
		t.Errorf("fail 존재: %q", out)
	}
}

func TestDoctorWarnVsFailExit(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정")
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{queryFn: doctorQueryFn(
		time.Now().Add(-80*time.Hour),
		time.Now().Add(-time.Hour),
		time.Now().Add(-time.Hour)),
	}
	out, code := runDoctorArgs(t, f)
	if code != 0 {
		t.Fatalf("warn exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"warn"`) {
		t.Errorf("warn 없음: %q", out)
	}
	if strings.Contains(out, `"fail"`) {
		t.Errorf("fail 있음: %q", out)
	}
}

func TestDoctorAllOk(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정")
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"status":"ok"`) {
		t.Errorf("status ok 아님: %q", out)
	}
}

func TestDoctorDBFail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`flint`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{pingErr: context.DeadlineExceeded, queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--check", "db")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"fail"`) {
		t.Errorf("fail 없음: %q", out)
	}
}

func TestDoctorSchemaVersionGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`flint`), 0o644); err != nil {
		t.Fatal(err)
	}
	v3 := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "schema_version") {
			return [][]any{{"3"}}, nil
		}
		return freshDoctorFn()(sql, args)
	}
	f := &fakeStore{queryFn: v3}
	out, code := runDoctorArgs(t, f, "--check", "schema")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"fail"`) {
		t.Errorf("fail 없음: %q", out)
	}
}

func TestDoctorIntegrityRowGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`flint`), 0o644); err != nil {
		t.Fatal(err)
	}
	two := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "pg_indexes") {
			return [][]any{{int64(2)}}, nil
		}
		return freshDoctorFn()(sql, args)
	}
	f := &fakeStore{queryFn: two}
	out, code := runDoctorArgs(t, f, "--check", "integrity")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"fail"`) {
		t.Errorf("fail 없음: %q", out)
	}
}

func TestDoctorLastSeenNeverWarns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`flint`), 0o644); err != nil {
		t.Fatal(err)
	}
	none := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_write_at") {
			return nil, nil
		}
		return freshDoctorFn()(sql, args)
	}
	f := &fakeStore{queryFn: none}
	out, code := runDoctorArgs(t, f, "--check", "last-seen")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"warn"`) {
		t.Errorf("warn 없음: %q", out)
	}
}

func TestDoctorHotStallWarns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`flint`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{queryFn: doctorQueryFn(
		time.Now().Add(-time.Hour),
		time.Now().Add(-49*time.Hour),
		time.Now().Add(-time.Hour)),
	}
	out, code := runDoctorArgs(t, f, "--check", "last-seen")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"warn"`) {
		t.Errorf("warn 없음: %q", out)
	}
}

func TestDoctorUnknownFlag(t *testing.T) {
	f := &fakeStore{}
	out, code := runDoctorArgs(t, f, "--bogus")
	if code != 3 {
		t.Errorf("exit=%d, want 3", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}
}

func TestDoctorCheckRequiresNameExit3(t *testing.T) {
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--check")
	if code != 3 {
		t.Errorf("exit=%d, want 3", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}
}

func TestDoctorJSONFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--json")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	var m struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m.Status != "fail" {
		t.Errorf("status=%q, want fail", m.Status)
	}
	if len(m.Checks) == 0 {
		t.Fatalf("checks 비어 있음: %q", out)
	}
	if m.Checks[0].Name != "hook-file" || m.Checks[0].Status != "fail" {
		t.Errorf("checks[0]=%+v, want hook-file fail", m.Checks[0])
	}

	f2 := &fakeStore{queryFn: freshDoctorFn()}
	out2, code2 := runDoctorArgs(t, f2, "--json", "--check", "db")
	if code2 != 0 {
		t.Fatalf("subset exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out2)), &m); err != nil {
		t.Fatalf("subset JSON 아님: %q", out2)
	}
	if len(m.Checks) != 1 || m.Checks[0].Name != "db" {
		t.Errorf("subset checks=%+v, want db 1건", m.Checks)
	}
}

func TestDoctorJSONBytesMatchDefault(t *testing.T) {
	f := &fakeStore{queryFn: freshDoctorFn()}
	def, defCode := runDoctorArgs(t, f, "--check", "db")
	f2 := &fakeStore{queryFn: freshDoctorFn()}
	js, jsCode := runDoctorArgs(t, f2, "--check", "db", "--json")
	if defCode != jsCode {
		t.Fatalf("exit default=%d json=%d, want equal", defCode, jsCode)
	}
	if def != js {
		t.Errorf("--json not byte-identical to default:\ndefault: %q\njson:    %q", def, js)
	}
}

func TestDoctorSchemaFutureVersionOk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	v5 := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "schema_version") {
			return [][]any{{"5"}}, nil
		}
		return freshDoctorFn()(sql, args)
	}
	f := &fakeStore{queryFn: v5}
	out, code := runDoctorArgs(t, f, "--check", "schema")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"ok"`) {
		t.Errorf("schema v5가 fail로 오탐: %q", out)
	}
}

func TestDoctorLastSeenThresholdOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := doctorQueryFn(
		time.Now().Add(-30*time.Hour),
		time.Now().Add(-30*time.Hour),
		time.Now().Add(-time.Hour))
	over := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "stall_hot_h") {
			return [][]any{{"stall_hot_h", "24"}, {"stall_write_h", "72"}}, nil
		}
		return fn(sql, args)
	}
	f := &fakeStore{queryFn: over}
	out, code := runDoctorArgs(t, f, "--check", "last-seen")
	if code != 0 {
		t.Fatalf("warn exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"warn"`) {
		t.Errorf("오버라이드 24h 초과인데 warn 없음(하드코딩 48h 잔존 의심): %q", out)
	}
}

func TestDoctorHookFileNestedShapeOk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	body := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"flint hot --session x"}]}],` +
		`"Stop":[{"hooks":[{"type":"command","command":"flint done --session x"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--check", "hook-file")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"ok"`) {
		t.Errorf("실체 형태 훅 등록인데 fail: %q", out)
	}
}

func setHarnessEnvs(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"FLINT_CODEX_HOME", "FLINT_GEMINI_HOME", "FLINT_OPENCODE_HOME",
		"FLINT_COPILOT_HOME", "FLINT_CURSOR_DIR",
	} {
		t.Setenv(e, t.TempDir())
	}
}

func TestHarnessChecksSkipWhenAbsent(t *testing.T) {
	setHarnessEnvs(t)
	if got := harnessChecks(); len(got) != 0 {
		t.Fatalf("harnessChecks() = %v, want 빈 슬라이스", got)
	}
}

func TestHarnessChecksFailWhenUnregistered(t *testing.T) {
	setHarnessEnvs(t)
	home := t.TempDir()
	t.Setenv("FLINT_CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[mcp_servers.other]\ncommand = \"other\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := harnessChecks()
	if len(got) != 1 {
		t.Fatalf("harnessChecks() = %v, want 1건", got)
	}
	if got[0].Name != "harness-codex" {
		t.Errorf("Name = %q, want harness-codex", got[0].Name)
	}
	if got[0].Status != "fail" {
		t.Errorf("Status = %q, want fail", got[0].Status)
	}
	if !strings.Contains(got[0].Fix, "docs/adapters/codex.md") {
		t.Errorf("Fix = %q, want docs/adapters/codex.md 경로", got[0].Fix)
	}
}

func TestHarnessChecksOkWhenRegistered(t *testing.T) {
	setHarnessEnvs(t)
	home := t.TempDir()
	t.Setenv("FLINT_GEMINI_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"mcpServers":{"flint":{"command":"flint","args":["mcp"],"trust":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := harnessChecks()
	if len(got) != 1 {
		t.Fatalf("harnessChecks() = %v, want 1건", got)
	}
	if got[0].Name != "harness-gemini" || got[0].Status != "ok" {
		t.Errorf("결과 = %+v, want harness-gemini ok", got[0])
	}
}

func TestHarnessChecksCursorProjectScope(t *testing.T) {
	setHarnessEnvs(t)
	root := t.TempDir()
	t.Setenv("FLINT_CURSOR_DIR", root)
	if err := os.MkdirAll(filepath.Join(root, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".cursor", "mcp.json")
	if err := os.WriteFile(path,
		[]byte(`{"mcpServers":{"flint":{"command":"flint","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := harnessChecks()
	if len(got) != 1 {
		t.Fatalf("harnessChecks() = %v, want 1건", got)
	}
	if got[0].Name != "harness-cursor" || got[0].Status != "ok" {
		t.Errorf("결과 = %+v, want harness-cursor ok", got[0])
	}
	if !strings.Contains(got[0].Detail, path) {
		t.Errorf("Detail = %q, want 프로젝트 경로 %q", got[0].Detail, path)
	}
}

func TestDoctorHookFileFlintWordOnlyFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"statusLine":"flint 상태 표시"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--check", "hook-file")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"fail"`) {
		t.Errorf("flint 단어만으로 통과하면 안 됨: %q", out)
	}
	if !strings.Contains(out, "session-start(SessionStart)") {
		t.Errorf("fix 문구가 실제 등록 절차(hot/done 훅)를 안내하지 않음: %q", out)
	}
}

func TestDoctorCheckNameSpace(t *testing.T) {
	c := runDoctorCheck(context.Background(), &fakeStore{}, "skill-file")
	if c.Name != "skill-file" || (c.Status != "ok" && c.Status != "warn" && c.Status != "fail") {
		t.Errorf("skill-file 체크 = %+v", c)
	}
	h := runDoctorCheck(context.Background(), &fakeStore{}, "harness-codex")
	if h.Name != "harness-codex" {
		t.Errorf("harness-codex 체크 이름 = %q", h.Name)
	}
	unknown := runDoctorCheck(context.Background(), &fakeStore{}, "harness-nope")
	if unknown.Status != "fail" || unknown.Detail != "unknown check" {
		t.Errorf("미등록 하니스 = %+v, want unknown check", unknown)
	}
}

func TestDoctorEventVolume(t *testing.T) {
	base := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "count(*) FROM read_events"):
			return [][]any{{int64(60000)}}, nil
		}
		return nil, nil
	}}
	if c := checkEventVolume(context.Background(), base); c.Status != "warn" {
		t.Errorf("60000행 = %+v, want warn", c)
	}
	small := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "count(*) FROM read_events"):
			return [][]any{{int64(120)}}, nil
		}
		return nil, nil
	}}
	if c := checkEventVolume(context.Background(), small); c.Status != "ok" {
		t.Errorf("120행 = %+v, want ok", c)
	}
}

func TestDoctorSecretsScan(t *testing.T) {
	hit := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {

			return [][]any{{int64(7), "토픽", "토큰 ghp_abcdefghijklmnopqrst 포함", "", "", "", ""}}, nil
		}
		return nil, nil
	}}
	if c := checkSecrets(context.Background(), hit); c.Status != "warn" || !strings.Contains(c.Detail, "github-token#7") {
		t.Errorf("발견 케이스 = %+v", c)
	}
	clean := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		return nil, nil
	}}
	if c := checkSecrets(context.Background(), clean); c.Status != "ok" {
		t.Errorf("무해 케이스 = %+v", c)
	}
}

func TestDoctorSecretsScanSessions(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {

			return [][]any{{int64(3), "claude", "요약에 토큰 ghp_abcdefghijklmnopqrst 포함"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#session:3") {
		t.Errorf("세션 발견 케이스 = %+v", c)
	}
}

func TestDoctorSecretsScanRowLimitReached(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return [][]any{{"secrets_scan_row_limit", "2"}}, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {

			return [][]any{
				{int64(1), "토픽", "무해한 요약", "", "", "", ""},
				{int64(2), "토픽", "또 다른 무해한 요약", "", "", "", ""},
			}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "row limit") {
		t.Errorf("행 상한 케이스 = %+v", c)
	}
}

func TestDoctorSecretsScanSessionsError(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {
			return nil, errors.New("relation missing")
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "session query failed") {
		t.Errorf("세션 조회 실패 케이스 = %+v", c)
	}
}

func TestDoctorSecretsFindingsSurviveSessionError(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(7), "토픽", "토큰 ghp_abcdefghijklmnopqrst 포함", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {
			return nil, errors.New("relation missing")
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#7") {
		t.Errorf("지식 발견 유실 = %+v", c)
	}
	if !strings.Contains(c.Detail, "session query failed") {
		t.Errorf("격차 미주석 = %+v", c)
	}
}

func TestDoctorSecretsScanProjects(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{int64(3), "claude", "무해한 요약"}}, nil
		}
		if strings.Contains(sql, "FROM projects") {
			return [][]any{{int64(5), "토큰 ghp_abcdefghijklmnopqrst 담은 프로젝트명"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#project:5") {
		t.Errorf("프로젝트 발견 케이스 = %+v", c)
	}
}

func TestDoctorMetaHygieneUnknownKeySanitized(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"legacy[flint]key"}}, nil
		}
		return nil, nil
	}}
	c := checkMetaHygiene(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "1 unknown keys") {
		t.Fatalf("미지 키 경고 = %+v", c)
	}
	if strings.Contains(c.Detail, "[flint]") {
		t.Errorf("미지 키 원문 노출: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "legacy key") {
		t.Errorf("살균된 키 미출력: %s", c.Detail)
	}
}

func TestFlintHookCmdEvalWrapped(t *testing.T) {
	cmd := flintHookCmd("/x/flint", `session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`)["command"].(string)
	if !strings.HasPrefix(cmd, `eval "'/x/flint' `) {
		t.Errorf("eval 래핑·경로 인용 부재: %q", cmd)
	}
	if strings.Contains(cmd, `"$(jq`) {
		t.Errorf("이중따옴표 치환 잔존: %q", cmd)
	}
}

func TestFlintHookCmdQuotesBinPath(t *testing.T) {
	cmd := flintHookCmd("/My Tools/flint", `session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`)["command"].(string)
	if !strings.HasPrefix(cmd, `eval "'/My Tools/flint' session-start `) {
		t.Errorf("공백 경로 미인용: %q", cmd)
	}
	if !flintHookSubRe.MatchString(cmd) {
		t.Errorf("신형 인용 형식을 flintHookSubRe 미식별(멱등 파손): %q", cmd)
	}
	legacy := `eval "/x/flint session-start --session $(jq -r '.session_id // "" | @sh' 2>/dev/null)" 2>/dev/null || true`
	if !flintHookSubRe.MatchString(legacy) {
		t.Errorf("구형 무인용 형식을 flintHookSubRe 미식별(기존 설치 교체 파손): %q", legacy)
	}
}

func TestDoctorFullChainIncludesTailChecks(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{queryFn: doctorQueryFn(time.Now().Add(-time.Hour), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))}
	out, code := runDoctorArgs(t, f)
	if code != 0 && !strings.Contains(out, `"status":"warn"`) {
		t.Fatalf("doctor exit=%d: %s", code, out)
	}
	for _, name := range doctorTailChecks() {
		if !strings.Contains(out, `"name":"`+name+`"`) {
			t.Errorf("전체 체인에 %s 부재 — doctorTailChecks와 실행 체인 불일치", name)
		}
	}
}

func TestDoctorEngineChecksRegistered(t *testing.T) {
	found := map[string]bool{}
	for _, s := range doctorChecks() {
		found[s.Name] = true
	}
	if !found[doltLogDepthName] || !found["db_size"] {
		t.Fatalf("등록표에 엔진 경고 누락: %v", found)
	}
	f := &fakeStore{}
	out, code := runDoctorArgs(t, f, "--list-checks")
	if code != 0 {
		t.Fatalf("--list-checks exit=%d: %s", code, out)
	}
	for _, name := range []string{doltLogDepthName, "db_size"} {
		if !strings.Contains(out, name) {
			t.Errorf("--list-checks에 %q 누락: %s", name, out)
		}
	}

	for _, name := range []string{doltLogDepthName, "db_size"} {
		_, code := runDoctorArgs(t, &fakeStore{queryErr: errors.New("down")}, "--check", name)
		if code == 3 {
			t.Errorf("--check %q이 미등록 취급(exit 3)", name)
		}
	}
}

func TestDoctorLogDepthBoundary(t *testing.T) {
	th := store.DefaultThresholds()
	if th.DoltLogDepthWarn != 10000 {
		t.Fatalf("depth warn 기본값=%d, want 10000", th.DoltLogDepthWarn)
	}
	ok := checkDoltLogDepthTh(context.Background(),
		&fakeStore{rows: [][]any{{int64(9999)}}}, th)
	if ok.Status != "ok" {
		t.Errorf("깊이 9999 status=%s, want ok(%s)", ok.Status, ok.Detail)
	}
	warn := checkDoltLogDepthTh(context.Background(),
		&fakeStore{rows: [][]any{{int64(10000)}}}, th)
	if warn.Status != "warn" {
		t.Fatalf("깊이 10000 status=%s, want warn", warn.Status)
	}
	if !strings.Contains(warn.Detail, "maintain --gc --yes (disconnects)") {
		t.Errorf("경고 안내 문구 불일치: %s", warn.Detail)
	}

	var probed string
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		probed = sql
		return [][]any{{int64(3)}}, nil
	}}
	checkDoltLogDepthTh(context.Background(), f, th)
	if !strings.Contains(probed, "commit_order::int8 FROM "+db.DoltLogTable+" ORDER BY commit_order DESC LIMIT 1") {
		t.Errorf("O(1) 프로브 아님: %s", probed)
	}

	errw := checkDoltLogDepthTh(context.Background(),
		&fakeStore{queryErr: errors.New("no such table")}, th)
	if errw.Status != "warn" {
		t.Errorf("질의 실패 status=%s, want warn", errw.Status)
	}
}

func TestDoctorDBSizeBoundary(t *testing.T) {
	th := store.DefaultThresholds()
	if th.DbSizeWarnMB != 2048 {
		t.Fatalf("db_size_warn_mb 기본값=%d, want 2048", th.DbSizeWarnMB)
	}
	ok := checkDBSizeTh(context.Background(),
		&fakeStore{rows: [][]any{{int64(2047 << 20)}}}, th)
	if ok.Status != "ok" {
		t.Errorf("2047MB status=%s, want ok(%s)", ok.Status, ok.Detail)
	}
	warn := checkDBSizeTh(context.Background(),
		&fakeStore{rows: [][]any{{int64(2048 << 20)}}}, th)
	if warn.Status != "warn" {
		t.Fatalf("2048MB status=%s, want warn", warn.Status)
	}
	if !strings.Contains(warn.Fix, "review backup retention") {
		t.Errorf("경고 안내 문구 불일치: %s", warn.Fix)
	}

	skip := checkDBSizeTh(context.Background(),
		&fakeStore{queryErr: errors.New("function not found")}, th)
	if skip.Status != "skip" {
		t.Errorf("측정 오류 status=%s, want skip", skip.Status)
	}
}

func TestDoctorEngineChecksFullRun(t *testing.T) {
	base := doctorQueryFn(time.Now(), time.Now(), time.Now())
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, db.DoltLogTable):
			return [][]any{{int64(10000)}}, nil
		case strings.Contains(sql, "pg_database_size"):
			return nil, errors.New("unsupported")
		}
		return base(sql, args)
	}}

	out, code := runDoctorArgs(t, f, "--check", doltLogDepthName)
	if code != 0 {
		t.Fatalf("depth warn 실행 exit=%d, want 0: %s", code, out)
	}
	if !strings.Contains(out, `"name":"`+doltLogDepthName+`","status":"warn"`) {
		t.Errorf("depth warn 미표시: %s", out)
	}
	out2, code2 := runDoctorArgs(t, f, "--check", "db_size")
	if code2 != 0 {
		t.Fatalf("db_size skip 실행 exit=%d, want 0: %s", code2, out2)
	}
	if !strings.Contains(out2, `"name":"db_size","status":"skip"`) {
		t.Errorf("db_size skip 미표시: %s", out2)
	}
}

func withEngineStats(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := EngineStatsFn
	EngineStatsFn = fn
	t.Cleanup(func() { EngineStatsFn = old })
}

func TestDoctorEngineFootprintSpec(t *testing.T) {
	s, ok := doctorSpec("engine-footprint")
	if !ok {
		t.Fatal("등록표에 engine-footprint 부재 — M2 계약 위반")
	}
	if s.Chain != "tail" {
		t.Errorf("chain=%q, want tail", s.Chain)
	}
	if s.ThFn != nil {
		t.Errorf("ThFn 등록 — 표시 전용 체크는 임계 없음(Fn 관례)")
	}
	if s.OnStoreNil != "" {
		t.Errorf("onStoreNil=%q, want 빈값(st 미사용 — docker stats 전용)", s.OnStoreNil)
	}
}

func TestDoctorEngineFootprintSkip(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	cases := map[string]func(context.Context) ([]byte, error){
		"docker absent": func(context.Context) ([]byte, error) { return nil, errors.New("docker not installed") },
		"no flint row": func(context.Context) ([]byte, error) {
			return []byte("{\"Name\":\"other-db-1\",\"MemUsage\":\"1GiB / 4GiB\"}\n"), nil
		},
		"unparsable body": func(context.Context) ([]byte, error) { return []byte("not-json"), nil },
	}
	for name, fn := range cases {
		withEngineStats(t, fn)
		c := checkEngineFootprint(context.Background(), nil)
		if c.Name != "engine-footprint" || c.Status != "skip" {
			t.Errorf("%s: status=%q, want skip: %+v", name, c.Status, c)
		}
	}
}

func TestDoctorEngineFootprintSkipWhenSelectionFails(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("docker compose ps failed")
	})
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return []byte("{\"Name\":\"flint-doltgres-1\",\"MemUsage\":\"50.5MiB / 3.84GiB\"}\n"), nil
	})
	c := checkEngineFootprint(context.Background(), nil)
	if c.Status != "skip" {
		t.Fatalf("status=%q, want skip(엔진 선택 실패): %+v", c.Status, c)
	}
}

func TestDoctorEngineFootprintOK(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-test-doltgres-1","State":"running","Publishers":[{"PublishedPort":5499}]},` +
			`{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return []byte("{\"Name\":\"flint-test-doltgres-1\",\"MemUsage\":\"900MiB / 4GiB\"}\n" +
			"{\"Name\":\"flint-doltgres-1\",\"MemUsage\":\"50.5MiB / 3.84GiB\"}\n"), nil
	})
	c := checkEngineFootprint(context.Background(), nil)
	if c.Status != "ok" {
		t.Fatalf("status=%q, want ok: %+v", c.Status, c)
	}
	if !strings.Contains(c.Detail, "50MB") {
		t.Fatalf("detail=%q, want 50MB — 전용 엔진(5499) 900MB가 아니라 DSN 포트 컨테이너 선택", c.Detail)
	}
}

func TestEnginePortResolvesDSNPort(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@127.0.0.1:5499/flint_test")
	if got := enginePort(); got != 5499 {
		t.Fatalf("enginePort=%d, want 5499(해소 DSN 포트)", got)
	}
	t.Setenv("FLINT_DSN", "postgresql://flint@localhost/flint")
	if got := enginePort(); got != doctorPort {
		t.Fatalf("enginePort=%d, want 폴백 %d(포트 없는 DSN)", got, doctorPort)
	}
}

func TestEngineContainerNameMatchesDSNPort(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-test-doltgres-1","State":"running","Publishers":[{"PublishedPort":5499}]},` +
			`{"Name":"flint-doltgres-2","State":"exited","Publishers":[{"PublishedPort":5434}]},` +
			`{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	name, err := engineContainerName(context.Background())
	if err != nil {
		t.Fatalf("engineContainerName: %v", err)
	}
	if name != "flint-doltgres-1" {
		t.Fatalf("name=%q, want flint-doltgres-1 — 접두 first-match는 전용 엔진(5499)·exited 컨테이너를 잘못 고른다", name)
	}
}

func withVersionSeams(t *testing.T, cur string, cacheFn func() (string, string), regFn func() (string, error)) {
	t.Helper()
	prevCur, prevVal, prevCache, prevReg := CurrentVersionFn, ValidVersionFn, UpdateCacheInfoFn, FetchRegistryVersionFn
	CurrentVersionFn = func() string { return cur }
	ValidVersionFn = func(v string) bool { return v == cur && validSemverish(v) }
	UpdateCacheInfoFn = cacheFn
	FetchRegistryVersionFn = regFn
	t.Cleanup(func() {
		CurrentVersionFn, ValidVersionFn, UpdateCacheInfoFn, FetchRegistryVersionFn = prevCur, prevVal, prevCache, prevReg
	})
}

func validSemverish(v string) bool {
	n := strings.Split(v, ".")
	if len(n) != 3 {
		return false
	}
	for _, p := range n {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func TestDoctorVersionsSpec(t *testing.T) {
	s, ok := doctorSpec("versions")
	if !ok {
		t.Fatal("등록표에 versions 부재 — M3 계약 위반")
	}
	if s.Chain != "tail" {
		t.Errorf("chain=%q, want tail", s.Chain)
	}
	if s.ThFn == nil || s.Fn != nil {
		t.Errorf("ThFn=%v Fn=%v — ThFn 등록이어야 함(F6 공유 로드)", s.ThFn != nil, s.Fn != nil)
	}
	if s.OnStoreNil != "" {
		t.Errorf("onStoreNil=%q, want 빈값(st 선택적)", s.OnStoreNil)
	}
}

func TestDoctorVersionsSideBySide(t *testing.T) {

	binDir := t.TempDir()
	flint := filepath.Join(binDir, "flint")
	if err := os.WriteFile(flint, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	checked := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	withVersionSeams(t, "1.26.0",
		func() (string, string) { return "1.26.0", checked },
		func() (string, error) { return "1.26.0", nil })
	c := checkVersionsTh(context.Background(), &fakeStore{}, store.DefaultThresholds())
	if c.Status != "ok" {
		t.Fatalf("status=%q, want ok: %+v", c.Status, c)
	}
	for _, want := range []string{"self v1.26.0", "cache latest v1.26.0", flint, "registry v1.26.0"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail=%q, want %q 포함", c.Detail, want)
		}
	}
	if !strings.Contains(c.Detail, "2h old") {
		t.Errorf("detail=%q, want 캐시 나이 표시", c.Detail)
	}
}

func TestDoctorVersionsOfflineRegistry(t *testing.T) {
	withVersionSeams(t, "1.26.0",
		func() (string, string) { return "1.26.0", time.Now().UTC().Format(time.RFC3339) },
		func() (string, error) { return "", errors.New("offline") })
	c := checkVersionsTh(context.Background(), &fakeStore{}, store.DefaultThresholds())
	if c.Status != "ok" {
		t.Fatalf("오프라인 status=%q, want ok(경고 아님): %+v", c.Status, c)
	}
	if !strings.Contains(c.Detail, "unverified (offline)") {
		t.Errorf("detail=%q, want unverified (offline)", c.Detail)
	}
}

func TestDoctorVersionsOfflineRegistryCached(t *testing.T) {
	withVersionSeams(t, "1.26.0",
		func() (string, string) { return "1.26.0", time.Now().UTC().Format(time.RFC3339) },
		func() (string, error) { return "", errors.New("offline") })
	prevInfo := RegistryCacheInfoFn
	RegistryCacheInfoFn = func() (string, string) {
		return "1.25.0", time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339)
	}
	t.Cleanup(func() { RegistryCacheInfoFn = prevInfo })
	c := checkVersionsTh(context.Background(), &fakeStore{}, store.DefaultThresholds())
	if c.Status != "ok" {
		t.Fatalf("오프라인(캐시) status=%q, want ok(경고 아님): %+v", c.Status, c)
	}
	for _, want := range []string{"registry v1.25.0 (cached, unverified — offline", "5h old"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail=%q, want %q 포함", c.Detail, want)
		}
	}
}

func TestDoctorVersionsStaleCacheWarn(t *testing.T) {
	stale := time.Now().UTC().Add(-100 * time.Hour).Format(time.RFC3339)
	withVersionSeams(t, "1.26.0",
		func() (string, string) { return "1.26.0", stale },
		func() (string, error) { return "1.26.0", nil })
	c := checkVersionsTh(context.Background(), &fakeStore{}, store.DefaultThresholds())
	if c.Status != "warn" {
		t.Fatalf("캐시 100h status=%q, want warn(기본 72h 초과): %+v", c.Status, c)
	}
	if !strings.Contains(c.Detail, "update_cache_age_h") {
		t.Errorf("detail=%q, want update_cache_age_h 근거 표시", c.Detail)
	}

	th, err := store.ParseThresholds([][2]any{{"update_cache_age_h", "200"}})
	if err != nil {
		t.Fatal(err)
	}
	c2 := checkVersionsTh(context.Background(), &fakeStore{}, wire.ApplyThresholdsFallback(th))
	if c2.Status != "ok" {
		t.Fatalf("오버라이드 200h 상태=%q, want ok: %+v", c2.Status, c2)
	}
}

func TestUpdateCacheAgeHThreshold(t *testing.T) {
	if def := store.DefaultThresholds(); def.UpdateCacheAgeH != 72 {
		t.Errorf("UpdateCacheAgeH 기본값=%d, want 72", def.UpdateCacheAgeH)
	}
	th, err := store.ParseThresholds([][2]any{{"update_cache_age_h", "10"}})
	if err != nil || th.UpdateCacheAgeH != 10 {
		t.Errorf("오버라이드 파싱=%d err=%v, want 10", th.UpdateCacheAgeH, err)
	}
	found := false
	for _, k := range wire.ThresholdsKeys() {
		if k == "update_cache_age_h" {
			found = true
		}
	}
	if !found {
		t.Error("ThresholdsKeys에 update_cache_age_h 부재")
	}
}

func TestReportTextUpdateCheckedAt(t *testing.T) {
	ts := "2026-11-23T00:00:00Z"
	prev := UpdateCacheInfoFn
	UpdateCacheInfoFn = func() (string, string) { return "1.26.0", ts }
	t.Cleanup(func() { UpdateCacheInfoFn = prev })
	var b bytes.Buffer
	printReportText(&b, ReportOutput{}, 7)
	if !strings.Contains(b.String(), "update check: "+ts) {
		t.Errorf("checked_at 미표시: %q", b.String())
	}

	UpdateCacheInfoFn = nil
	b.Reset()
	printReportText(&b, ReportOutput{}, 7)
	if strings.Contains(b.String(), "update check:") {
		t.Errorf("캐시 부재 시 라인 잔존: %q", b.String())
	}
}

func TestDoctorEngineStatsTimeout5s(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	var budget time.Duration
	withEngineStats(t, func(ctx context.Context) ([]byte, error) {
		if d, ok := ctx.Deadline(); ok {
			budget = time.Until(d)
		}
		return nil, errors.New("stop")
	})
	if _, ok := EngineMemMB(context.Background()); ok {
		t.Fatal("seam 오류 경로가 ok로 나오면 안 됨")
	}
	if budget < 4950*time.Millisecond {
		t.Errorf("docker stats 시간 예산=%v, want ≥4.95s (r109-5 — 2s는 샘플링 창보다 짧다)", budget)
	}
}

func withStrayDSNV1290(t *testing.T, dsn string) {
	t.Helper()
	old := PurgeDSNFn
	PurgeDSNFn = func() string { return dsn }
	t.Cleanup(func() { PurgeDSNFn = old })
}

func TestEngineDBWarnThresholdsSixSitesV1290(t *testing.T) {
	def := store.DefaultThresholds()
	if def.DroppedDBsWarn != 1 || def.StrayDBsWarn != 0 {
		t.Fatalf("기본값: dropped=%d stray=%d, want 1·0", def.DroppedDBsWarn, def.StrayDBsWarn)
	}
	th, err := store.ParseThresholds([][2]any{
		{"dropped_dbs_warn", "4"},
		{"stray_dbs_warn", "2"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DroppedDBsWarn != 4 || th.StrayDBsWarn != 2 {
		t.Errorf("오버라이드: dropped=%d stray=%d, want 4·2", th.DroppedDBsWarn, th.StrayDBsWarn)
	}
	fb := wire.ApplyThresholdsFallback(store.Thresholds{})
	if fb.DroppedDBsWarn != 1 || fb.StrayDBsWarn != 0 {
		t.Errorf("폴백: dropped=%d stray=%d, want 1·0", fb.DroppedDBsWarn, fb.StrayDBsWarn)
	}
	keys := map[string]bool{}
	for _, k := range wire.ThresholdsKeys() {
		keys[k] = true
	}
	if !keys["dropped_dbs_warn"] || !keys["stray_dbs_warn"] {
		t.Errorf("ThresholdsKeys에 신규 키 부재: %v", wire.ThresholdsKeys())
	}
	metaDefaults := map[string]any{}
	for _, m := range wire.ThresholdMetas() {
		metaDefaults[m.Key] = m.Default
	}
	if metaDefaults["dropped_dbs_warn"] != 1 || metaDefaults["stray_dbs_warn"] != 0 {
		t.Errorf("등록표 기본값 불일치: dropped=%v stray=%v",
			metaDefaults["dropped_dbs_warn"], metaDefaults["stray_dbs_warn"])
	}
	for _, name := range []string{"dropped_dbs", "stray_dbs"} {
		s, ok := doctorSpec(name)
		if !ok {
			t.Fatalf("등록표에 %s 부재", name)
		}
		if s.Chain != "tail" || s.ThFn == nil {
			t.Errorf("%s: chain=%q thFn nil=%t, want tail·ThFn", name, s.Chain, s.ThFn == nil)
		}
	}
	if !KnownMetaKey("stray_dbs_allow") {
		t.Error("stray_dbs_allow 미등록(metaKnownKeys)")
	}
}

func TestDoctorDroppedDBsCheckV1290(t *testing.T) {
	ctx := context.Background()
	listErr := errors.New("ERROR: no database name specified. available databases that can be undropped: demo_a, demo_b (SQLSTATE XX000)")
	emptyErr := errors.New("ERROR: no database name specified. there are no databases currently available to be undropped (SQLSTATE XX000)")

	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, listErr
	}}
	c := checkDroppedDBsTh(ctx, f, store.DefaultThresholds())
	if c.Status != "warn" {
		t.Fatalf("status=%q detail=%q, want warn", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "2") || !strings.Contains(c.Detail, "dropped_dbs_warn") {
		t.Errorf("detail=%q", c.Detail)
	}
	if !strings.Contains(c.Fix, "maintain --purge-dropped --yes") {
		t.Errorf("fix=%q, want purge 안내", c.Fix)
	}

	c = checkDroppedDBsTh(ctx, f, store.Thresholds{DroppedDBsWarn: 3})
	if c.Status != "ok" {
		t.Fatalf("임계 상회 ok: status=%q detail=%q", c.Status, c.Detail)
	}

	empty := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, emptyErr
	}}
	if c = checkDroppedDBsTh(ctx, empty, store.DefaultThresholds()); c.Status != "ok" {
		t.Fatalf("빈 보존 영역: status=%q, want ok", c.Status)
	}

	unsupported := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	if c = checkDroppedDBsTh(ctx, unsupported, store.DefaultThresholds()); c.Status != "skip" {
		t.Fatalf("미지원: status=%q, want skip", c.Status)
	}
	if c = checkDroppedDBsTh(ctx, nil, store.DefaultThresholds()); c.Status != "skip" {
		t.Fatalf("st nil: status=%q, want skip", c.Status)
	}
}

func doctorStrayFnV1290(dbs [][]any, allow string, allowErr error) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "pg_database") {
			return dbs, nil
		}
		if len(args) == 1 && args[0] == strayDBsAllowKey {
			if allowErr != nil {
				return nil, allowErr
			}
			if allow == "" {
				return nil, nil
			}
			return [][]any{{allow}}, nil
		}
		return nil, nil
	}
}

func TestDoctorStrayDBsCheckV1290(t *testing.T) {
	ctx := context.Background()
	withStrayDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")

	dbs := [][]any{{"flint"}, {"postgres"}, {"template0"}, {"template1"}, {"flint_test"}, {"extra_demo"}}

	f := &fakeStore{queryFn: doctorStrayFnV1290(dbs, "", nil)}
	c := checkStrayDBsTh(ctx, f, store.DefaultThresholds())
	if c.Status != "warn" {
		t.Fatalf("status=%q detail=%q, want warn", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "flint_test") || !strings.Contains(c.Detail, "legacy shared test DB") {
		t.Errorf("레거시 안내 부재: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "extra_demo") {
		t.Errorf("미면제 DB 미표시: %q", c.Detail)
	}
	if !strings.Contains(c.Fix, "maintain --purge-dropped --yes") {
		t.Errorf("fix=%q, want purge 안내", c.Fix)
	}

	allowed := &fakeStore{queryFn: doctorStrayFnV1290(dbs, "extra_demo , flint_test", nil)}
	if c = checkStrayDBsTh(ctx, allowed, store.DefaultThresholds()); c.Status != "ok" {
		t.Fatalf("면제 후: status=%q detail=%q, want ok", c.Status, c.Detail)
	}

	capped := &fakeStore{queryFn: doctorStrayFnV1290(dbs, "", nil)}
	if c = checkStrayDBsTh(ctx, capped, store.Thresholds{StrayDBsWarn: 2}); c.Status != "ok" {
		t.Fatalf("임계 상회: status=%q detail=%q, want ok", c.Status, c.Detail)
	}

	metaErr := &fakeStore{queryFn: doctorStrayFnV1290(dbs, "", errors.New("meta down"))}
	if c = checkStrayDBsTh(ctx, metaErr, store.DefaultThresholds()); c.Status != "warn" {
		t.Fatalf("meta 오류는 skip 아님(면제 목록만 무시): status=%q", c.Status)
	}

	pgErr := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, errors.New("pg_database unavailable")
	}}
	if c = checkStrayDBsTh(ctx, pgErr, store.DefaultThresholds()); c.Status != "skip" {
		t.Fatalf("조회 실패: status=%q, want skip", c.Status)
	}
	if c = checkStrayDBsTh(ctx, nil, store.DefaultThresholds()); c.Status != "skip" {
		t.Fatalf("st nil: status=%q, want skip", c.Status)
	}
}

func withRestartCountV1290(t *testing.T, fn func() (int, error)) {
	t.Helper()
	old := RestartCountFn
	RestartCountFn = fn
	t.Cleanup(func() { RestartCountFn = old })
}

func TestDoctorEngineFootprintRestartsV1290(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return []byte("{\"Name\":\"flint-doltgres-1\",\"MemUsage\":\"50.5MiB / 3.84GiB\"}\n"), nil
	})
	withRestartCountV1290(t, func() (int, error) { return 3, nil })
	c := checkEngineFootprint(context.Background(), nil)
	if c.Status != "ok" {
		t.Fatalf("status=%q, want ok", c.Status)
	}
	if !strings.Contains(c.Detail, "restarts 3") {
		t.Fatalf("detail=%q, want restarts 3 표시", c.Detail)
	}

	withRestartCountV1290(t, func() (int, error) { return 0, errors.New("docker inspect failed") })
	c = checkEngineFootprint(context.Background(), nil)
	if c.Status != "ok" {
		t.Fatalf("에러 시에도 ok: status=%q", c.Status)
	}
	if strings.Contains(c.Detail, "restarts") {
		t.Fatalf("에러 시 restarts 미표시 아님: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "50MB") {
		t.Fatalf("에러 시에도 메모리 표시 유지: %q", c.Detail)
	}
}
