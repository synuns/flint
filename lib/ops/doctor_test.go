// doctor_test.go — flint doctor's 5 checks and exit contracts (only fail exits 1, warn 0) and the
// FLINT_CLAUDE_HOME override (--check invalid name exit 3).
// fakeStore·
// the withComposePs helper is owned here (moved per the cli_test.go / doctor_v07_test.go convention).
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

// init — in the standalone ops test binary there is no cli init (ops_alias_1.go) seam
// injection — so the diagnostic chain's referenced seams get default implementations (same
// behavior as the cli real functions: dev build version indeterminate, skill source undetected, silent failure).
func init() {
	CurrentVersionFn = func() string { return "" } // dev build — the deferred-verdict path
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
	SkillSrcDirFn = func(string) string { return "" } // source undetected — check skipped
	CheckSkillFileFn = func(string, string) (DoctorCheck, bool) { return DoctorCheck{}, false }
}

// fakeStore — a wire.Store interface fake (moved per the cli_test.go convention). Stub
// fields (exists, *Err) take precedence when set; without rows or queryFn it returns
// zero behavior.
type fakeStore struct {
	wire.HistoryStubs

	// stub conditions
	exists        bool
	existsErr     error
	existsProject map[string]bool // existence per name (nil means all false)
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)

	// recording
	inserted []map[string]any
	commits  []string
	queries  []string
	qmu      sync.Mutex // protects the queries append (parallel collection — v1.9.3 T2)

	// fake-equivalent: metaDeleteStore canned (call recording, error injection).
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
	return f.existsProject[name], nil // a nil map lookup is false (zero behavior)
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

// MetaDelete — fake-equivalent: the metaDeleteStore capability canned
// implementation. So the tuning reset path is verified as a delegation path, not a
// degraded fallback (call-recording slice and an optional error field — the fakeStore convention).
func (f *fakeStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	f.metaDeleted = append(f.metaDeleted, keys...)
	if f.metaDeleteErr != nil {
		return 0, f.metaDeleteErr
	}
	return int64(len(keys)), nil
}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	// v1.9.3 T2: with report collection parallelized, queryFn is also called from multiple
	// goroutines — only the recording-slice append is mutex-protected (reads happen only on the
	// test thread after the command returns).
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

// withComposePs — swaps the ComposePsFn seam (restored after the test).
// Moved per the doctor_v07_test.go convention (the cli-side withComposePs swaps the cli var via
// the ops_alias_1.go wrapper for the same effect).
func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := ComposePsFn
	ComposePsFn = fn
	t.Cleanup(func() { ComposePsFn = old })
}

// runDoctorArgs — runs RunDoctor with a fake store. Returns output and exit.
func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := RunDoctor(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// doctorQueryFn — a shared queryFn injecting per-check meta/pg_indexes responses.
// Returns schema=4, 3 index rows, and 3 last-seen keys (with adjustable elapsed).
// The metering-freshness sources (5 table maxes + the 2 gate keys — schema v13
// A4) all report lastRead (fresh by construction in freshDoctorFn).
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

// freshDoctorFn — the all-checks-pass baseline response (last-seen 1h ago).
func freshDoctorFn() func(string, []any) ([][]any, error) {
	h := time.Now().Add(-time.Hour)
	return doctorQueryFn(h, h, h)
}

// DoctorChecks — the fixed order contract (6 kinds — port added).
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

// registry registration — Run must dispatch doctor.
func TestDoctorRegistered(t *testing.T) {
	if _, ok := wire.Lookup("doctor"); !ok {
		t.Fatal("doctor 미등록")
	}
}

// --list-checks — one-line JSON with all 20 registry kinds.
// The core 6 keep the existing order (hook-file... port) at the front.
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
	if len(m.Checks) != 20 {
		t.Errorf("checks %d종, want 20종(코어6+tail14): %v", len(m.Checks), m.Checks)
	}
	if m.Checks[0] != "hook-file" || m.Checks[4] != "last-seen" || m.Checks[5] != "port" {
		t.Errorf("코어 6종 순서 회귀: %v", m.Checks)
	}
}

// TestDoctorMeteringFreshnessSpec — the schema v13 A4 registration guard:
// exactly 1 registry row "metering-freshness" in the tail chain, ThFn
// (threshold-injected — metering_freshness_warn_h), warn on st==nil.
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

// meteringFreshnessFn — the 7-source freshness fixture: every source at
// 1h ago unless overridden (a zero time = NULL max — never recorded).
// Errors lists source labels whose lookup fails (the quiet-skip path).
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
			// the 2 gate keys travel in one IN query — a per-key failure is
			// simulated by erroring the whole query for either label.
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
			return [][]any{{nil}}, nil // NULL max — the table is empty
		}
		return [][]any{{times[src]}}, nil
	}
}

// TestMeteringFreshnessVerdicts — the freshness verdicts: all sources
// within the threshold → ok; one over → warn naming it; the threshold is
// read from the injected th (protocol: an override changes the verdict);
// never-recorded sources warn; a per-source lookup failure is skipped
// quietly and a total lookup failure defers (ok); recall_events uses
// recalled_at (not ts); the gate meta keys are judgment sources.
func TestMeteringFreshnessVerdicts(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-400 * time.Hour) // over the 336h default
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
		// All sources 100h old: within the 336h default (ok), over a 48h
		// override (warn) — the verdict follows the injected threshold.
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

// protocol — --check with an invalid name exits 3; a valid name runs only the subset.
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

// file — FLINT_CLAUDE_HOME override: a temp home without hooks -> hook-file fail.
// fail means exit 1 + a one-line fix wording.
func TestDoctorHookFileFailWithFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home) // no settings.json

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

// file — a temp environment with the hook registered (settings.json contains flint) -> hook-file ok.
func TestDoctorHookFileOkWhenRegistered(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — environment dependency removed
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // isolate the 5 harnesses — so real-home settings files cannot interfere with the full run
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

// state — last-seen past grace is warn, exit 0. write over 72h and hot over 48h warn.
func TestDoctorWarnVsFailExit(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — environment dependency removed
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // isolate the 5 harnesses — so real-home settings files cannot interfere with the full run
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// write 80h ago (>72h) and hot 1h ago -> warn only -> exit 0.
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

// state — within grace everything is ok, exit 0.
func TestDoctorAllOk(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — environment dependency removed
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // isolate the 5 harnesses — so real-home settings files cannot interfere with the full run
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

// db — ping failure -> fail, exit 1.
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

// schema — any version other than 4 fails (a legacy v3 DB).
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

// integrity — fewer than 3 flint_knowledge_* index rows fails.
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

// last-seen — no record (never) is warn (the nodata pattern), exit 0.
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

// hot 49h ago (>48h) is also warn.
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

// an unknown flag — invalid-arg exit 3 (the global contract, matching an invalid --check name).
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

// a missing --check value — invalid-arg exit 3 (unified with unknown flag and invalid --check name).
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

// protocol — doctor --json — the explicit machine-readable channel. The output is
// a one-line JSON of the DoctorCheck field structure (status, checks[].name/status), and
// the exit contract matches the default path. The existing text (the default one-line JSON) is unchanged —
// default-path regression is covered by the existing full-run tests.
func TestDoctorJSONFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home) // pin hook-file fail (temp home)

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--json")
	if code != 1 { // a fail exists — the same exit as the default full run
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

	// --json --check NAME — the subset uses the same structure.
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

// protocol — the doctor --json output is byte-identical to the default output (pinning the explicit
// machine channel — not a second format). Verified on the path without --fix
// (byte comparison with a deterministic db subset).
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

// schema — a higher version (e.g. a future v5) is normal. Under the old string-equality "4" check,
// a v5 DB was a false fail (the numeric n>=4 contract).
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

// last-seen — the stall_hot_h meta override (24h) is reflected by loadThresholds:
// a 30h-elapsed last_hot_at is ok under the hardcoded 48h but warn under the 24h override
// (conclusions match maintain --check).
func TestDoctorLastSeenThresholdOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := doctorQueryFn(
		time.Now().Add(-30*time.Hour), // last_write_at — within the default 72h
		time.Now().Add(-30*time.Hour), // last_hot_at — within the default 48h, over the 24h override
		time.Now().Add(-time.Hour))
	over := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "stall_hot_h") { // the loadThresholds query
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

// hook-file — ok when registered in the real hooks.json shape (event arrays -> nested hooks[].command).
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

// setHarnessEnvs — isolates all 5 harness envs to empty temp directories.
// A precondition of the harnessChecks unit tests (blocking real-home settings files).
func setHarnessEnvs(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"FLINT_CODEX_HOME", "FLINT_GEMINI_HOME", "FLINT_OPENCODE_HOME",
		"FLINT_COPILOT_HOME", "FLINT_CURSOR_DIR",
	} {
		t.Setenv(e, t.TempDir())
	}
}

// state — all settings files absent -> no harness checks produced (an empty slice).
// An uninstalled harness is not a warning (§4 a).
func TestHarnessChecksSkipWhenAbsent(t *testing.T) {
	setHarnessEnvs(t)
	if got := harnessChecks(); len(got) != 0 {
		t.Fatalf("harnessChecks() = %v, want 빈 슬라이스", got)
	}
}

// file — only the codex config present, flint unregistered -> 1 fail. Name "harness-codex" and
// the Fix carries the doc path (§4 b).
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

// file — the body contains "flint" -> ok (the registration check is string matching, format-agnostic — C6).
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

// file — cursor prefers the project scope (.cursor/mcp.json): the FLINT_CURSOR_DIR
// root's project file becomes the check target (C5).
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

// hook-file — a body with only the 'flint' string and no hooks.command fails
// (an accidental match that passed under the old full-text containment check).
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

// TestDoctorCheckNameSpace — every check name the full report
// shows (skill-file, harness-*) is runnable via --check standalone.
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

// TestDoctorEventVolume — when the 3 metering tables' total exceeds
// the events_volume_warn threshold it warns; below it, ok (growth is not a fail).
func TestDoctorEventVolume(t *testing.T) {
	base := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds defaults (50000)
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

// TestDoctorSecretsScan — a secret candidate found in held knowledge
// warns (pattern name + id only — no body output).
func TestDoctorSecretsScan(t *testing.T) {
	hit := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil // thresholds unset — the default cap
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			// full-column scan (7 columns), invalidation excluded.
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

// TestDoctorSecretsScanSessions — sessions.summary
// the findings path — the founding motive (the most PII-dense field) had no test case.
func TestDoctorSecretsScanSessions(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {
			// a real pattern in the session summary — must be found and warn
			return [][]any{{int64(3), "claude", "요약에 토큰 ghp_abcdefghijklmnopqrst 포함"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#session:3") {
		t.Errorf("세션 발견 케이스 = %+v", c)
	}
}

// TestDoctorSecretsScanRowLimitReached — the row-cap-reached branch —
// never falsely reports a partial scan as a clean ok (the LIMIT boundary is >=).
func TestDoctorSecretsScanRowLimitReached(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return [][]any{{"secrets_scan_row_limit", "2"}}, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			// returns the same count as the cap of 2 -> cap reached (a truncated LIMIT means more exist).
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

// TestDoctorSecretsScanSessionsError — a session lookup failure is warn,
// not a false ok (symmetric with the knowledge path's 'knowledge lookup failure').
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

// TestDoctorSecretsFindingsSurviveSessionError — a session lookup
// failure does not discard already-caught knowledge findings (the gap is annotated).
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

// TestDoctorSecretsScanProjects — the post-hoc scan of projects.name —
// restore and legacy intake bypass the write-time scan (closing the same blind spot as the
// session path). Findings are reported only as pattern#project:id (no body output).
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

// TestDoctorMetaHygieneUnknownKeySanitized — meta-hygiene
// unknown-key samples flow verbatim into the detail, so they pass through the sanitization outlet.
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

// TestFlintHookCmdEvalWrapped — the hook command doctor installs also uses the
// jq @sh + eval pattern — preventing a recurrence of the double-quoted substitution ("$(jq ...)").
// binPath single-quoted — preventing silent loss for paths with spaces.
func TestFlintHookCmdEvalWrapped(t *testing.T) {
	cmd := flintHookCmd("/x/flint", `session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`)["command"].(string)
	if !strings.HasPrefix(cmd, `eval "'/x/flint' `) {
		t.Errorf("eval 래핑·경로 인용 부재: %q", cmd)
	}
	if strings.Contains(cmd, `"$(jq`) {
		t.Errorf("이중따옴표 치환 잔존: %q", cmd)
	}
}

// TestFlintHookCmdQuotesBinPath — an install path containing spaces is also
// assembled as 1 single-quoted argument (unquoted, eval word-splitting silently drops the hook).
// Asserts the assembled string + idempotence-identification consistency (flintHookSubRe must catch
// both the new quoted form and the old unquoted form to keep preventing reinstall duplicates).
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

// TestDoctorFullChainIncludesTailChecks — consistency between the full run chain and
// doctorTailChecks (the --check allow list) — preventing a recurrence of the wiring omission
// where backup-key was added to the list but the chain append was missed, so the full doctor
// never ran the check (verifying the single source of names).
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

// ── v1.25.0 T1: the engine-warning checks (dolt_log_depth · db_size) ──

// TestDoctorEngineChecksRegistered — the 2 engine warnings join the registry
// tail (19 registry kinds) and the --list-checks surface.
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
	// standalone --check execution is allowed for both.
	for _, name := range []string{doltLogDepthName, "db_size"} {
		_, code := runDoctorArgs(t, &fakeStore{queryErr: errors.New("down")}, "--check", name)
		if code == 3 {
			t.Errorf("--check %q이 미등록 취급(exit 3)", name)
		}
	}
}

// TestDoctorLogDepthBoundary — the depth verdict is at-or-above the
// dolt_log_depth_warn threshold (9999 ok, 10000 warn) off the O(1) probe
// (commit_order DESC LIMIT 1).
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
	// the O(1) probe shape — no count(*).
	var probed string
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		probed = sql
		return [][]any{{int64(3)}}, nil
	}}
	checkDoltLogDepthTh(context.Background(), f, th)
	if !strings.Contains(probed, "commit_order::int8 FROM "+db.DoltLogTable+" ORDER BY commit_order DESC LIMIT 1") {
		t.Errorf("O(1) 프로브 아님: %s", probed)
	}
	// a query failure is a warn finding (not disguised as clean).
	errw := checkDoltLogDepthTh(context.Background(),
		&fakeStore{queryErr: errors.New("no such table")}, th)
	if errw.Status != "warn" {
		t.Errorf("질의 실패 status=%s, want warn", errw.Status)
	}
}

// TestDoctorDBSizeBoundary — the size verdict is at-or-above the
// db_size_warn_mb threshold (2047MB ok, 2048MB warn); a pg_database_size
// failure is a quiet skip.
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
	// an unsupported pg_database_size is a quiet skip (no warn, no fail).
	skip := checkDBSizeTh(context.Background(),
		&fakeStore{queryErr: errors.New("function not found")}, th)
	if skip.Status != "skip" {
		t.Errorf("측정 오류 status=%s, want skip", skip.Status)
	}
}

// TestDoctorEngineChecksFullRun — the full run chain wires the 2 checks with
// the once-per-run fallback-applied thresholds (unset meta → defaults), a
// warn verdict keeps exit 0, and the skip does not surface as a warn.
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
	// Only run the 2 engine checks — the full chain includes environment-
	// dependent checks (hook-file, port) that fail on CI runners.
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

// withEngineStats — swaps the EngineStatsFn seam (restored after the test;
// the withComposePs convention for exec seams).
func withEngineStats(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := EngineStatsFn
	EngineStatsFn = fn
	t.Cleanup(func() { EngineStatsFn = old })
}

// TestDoctorEngineFootprintSpec — the v1.25.0 M2 registration guard:
// exactly 1 registry row "engine-footprint" in the tail chain, the Fn form
// (display only — no threshold, so no ThFn per the CheckSpec convention),
// and st unused (OnStoreNil "" — docker stats only).
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

// TestDoctorEngineFootprintSkip — docker unavailable (not installed, no
// flint container row, or unparsable output): a quiet skip, never
// fail/warn (non-interference — the checkPort convention).
func TestDoctorEngineFootprintSkip(t *testing.T) {
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

// TestDoctorEngineFootprintOK — a flint container row round-trips into the
// ok detail as working-set MB ("50.5MiB / 3.84GiB" → 50MB), and non-flint
// rows are skipped over.
func TestDoctorEngineFootprintOK(t *testing.T) {
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return []byte("{\"Name\":\"other-db-1\",\"MemUsage\":\"900MiB / 4GiB\"}\n" +
			"{\"Name\":\"flint-doltgres-1\",\"MemUsage\":\"50.5MiB / 3.84GiB\"}\n"), nil
	})
	c := checkEngineFootprint(context.Background(), nil)
	if c.Status != "ok" {
		t.Fatalf("status=%q, want ok: %+v", c.Status, c)
	}
	if !strings.Contains(c.Detail, "50MB") {
		t.Fatalf("detail=%q, want 50MB 포함", c.Detail)
	}
}

// TestDoctorEngineStatsTimeout5s — the r109-5 regression: EngineMemMB hands
// the docker stats seam a ≥5s budget. The old 2s cap was shorter than the
// sampling window docker stats --no-stream itself takes, so a normal read on
// a loaded daemon timed out and the footprint was a standing skip. The seam
// captures the deadline it was given; the checkUpdate convention is 5s.
func TestDoctorEngineStatsTimeout5s(t *testing.T) {
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
