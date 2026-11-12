// doctor_test.go — T64: flint doctor 검사 5종·exit 계약(fail만 1·warn 0)·
// FLINT_CLAUDE_HOME 오버라이드(--check 무효명 exit 3).
// v0.40.0 T1: lib/cli에서 이동(행위명 유지 — v0.39 관례). fakeStore·
// withComposePs 헬퍼도 동반 이동한다(cli_test.go·doctor_v07_test.go 관례).
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// init — ops 단독 테스트 바이너리에서는 cli init(ops_alias_1.go)의 seam
// 주입이 없다 — 진단 체인이 참조하는 seam에 기본 구현을 둔다(cli 실함수와
// 동일 행동: dev 빌드 버전 미확정·스킬 소스 미탐지·조용한 실패).
func init() {
	CurrentVersionFn = func() string { return "" } // dev 빌드 — 판정 보류 경로
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
	SkillSrcDirFn = func(string) string { return "" } // 소스 미탐지 — 검사 생략
	CheckSkillFileFn = func(string, string) (DoctorCheck, bool) { return DoctorCheck{}, false }
}

// fakeStore — wire.Store 인터페이스 fake(cli_test.go 관례 이동분). stub
// 필드(exists·*Err)는 지정 시 우선되고, rows·queryFn 미지정 시 영(零)
// 동작을 반환한다.
type fakeStore struct {
	// stub 조건
	exists        bool
	existsErr     error
	existsProject map[string]bool // name별 존재 판정(nil이면 전부 false)
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)

	// 기록
	inserted []map[string]any
	commits  []string
	queries  []string

	// v0.43.0 T2 fake 동질: metaDeleteStore canned(호출 기록·오류 주입).
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
	return f.existsProject[name], nil // nil map 조회는 false(영 동작)
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

// MetaDelete — v0.43.0 T2 fake 동질: metaDeleteStore capability canned
// 구현. tuning reset 경로가 열화 폴백이 아니라 위임 경로로 검증되게
// 한다(호출 기록 슬라이스·선택적 오류 필드 — fakeStore 관례).
func (f *fakeStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	f.metaDeleted = append(f.metaDeleted, keys...)
	if f.metaDeleteErr != nil {
		return 0, f.metaDeleteErr
	}
	return int64(len(keys)), nil
}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return f.rows, nil
}

// withComposePs — ComposePsFn seam 교체(테스트 후 원복).
// doctor_v07_test.go 관례 이동분(cli 측 withComposePs는 cli var 교체로
// ops_alias_1.go 래퍼를 경유해 같은 효과를 낸다).
func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := ComposePsFn
	ComposePsFn = fn
	t.Cleanup(func() { ComposePsFn = old })
}

// runDoctorArgs — fake store으로 RunDoctor 실행. 출력·exit 반환.
func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := RunDoctor(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// doctorQueryFn — 검사별 meta/pg_indexes 응답을 주입하는 공용 queryFn.
// schema=4·인덱스 3행·last-seen 3키(경과 조절)를 반환한다.
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
		}
		return nil, nil
	}
}

// freshDoctorFn — 전 검사 통과 기준 응답(1시간 전 last-seen).
func freshDoctorFn() func(string, []any) ([][]any, error) {
	h := time.Now().Add(-time.Hour)
	return doctorQueryFn(h, h, h)
}

// DoctorChecks — 고정 순서 계약(v0.7부터 6종 — port 추가).
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

// registry 등록 — Run이 doctor를 디스패치해야 한다.
func TestDoctorRegistered(t *testing.T) {
	if _, ok := wire.Lookup("doctor"); !ok {
		t.Fatal("doctor 미등록")
	}
}

// --list-checks — 한 줄 JSON에 등록표 전량 16종(v0.34.3 R11 — 구 6종만
// 내던 미노출 결함 회수). 코어 6종은 기존 순서(hook-file…port) 선두 유지.
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
	if len(m.Checks) != 16 {
		t.Errorf("checks %d종, want 16종(코어6+tail10): %v", len(m.Checks), m.Checks)
	}
	if m.Checks[0] != "hook-file" || m.Checks[4] != "last-seen" || m.Checks[5] != "port" {
		t.Errorf("코어 6종 순서 회귀: %v", m.Checks)
	}
}

// protocol — --check 무효명 exit 3·유효명은 서브셋만 실행.
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

// file — FLINT_CLAUDE_HOME 오버라이드: 훅 없는 임시 홈 → hook-file fail.
// fail은 exit 1 + fix 문구 한 줄.
func TestDoctorHookFileFailWithFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home) // settings.json 부재

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

// file — 훅 등록된 임시 환경(settings.json에 flint 포함) → hook-file ok.
func TestDoctorHookFileOkWhenRegistered(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — 환경 의존 제거
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // 하니스 5종 격리 — 실홈 설정 파일이 전체 실행에 끼어들지 않게
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

// state — grace 경과 last-seen은 warn·exit 0. write 72h·hot 48h 초과로 warn.
func TestDoctorWarnVsFailExit(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — 환경 의존 제거
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // 하니스 5종 격리 — 실홈 설정 파일이 전체 실행에 끼어들지 않게
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// write 80시간 전(>72h)·hot 1시간 전 → warn만 → exit 0.
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

// state — grace 이내면 전부 ok·exit 0.
func TestDoctorAllOk(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — 환경 의존 제거
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // 하니스 5종 격리 — 실홈 설정 파일이 전체 실행에 끼어들지 않게
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

// db — ping 실패 → fail·exit 1.
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

// schema — 버전 4가 아니면 fail(기존 3 DB).
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

// integrity — flint_knowledge_* 인덱스 3행 미만이면 fail.
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

// last-seen — 기록 부재(never)는 warn(nodata 패턴)·exit 0.
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

// hot 49시간 전(>48h)도 warn.
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

// 알 수 없는 플래그 — 무효 인자 exit 3(전역 계약, 무효 --check명과 일치).
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

// --check 값 누락 — 무효 인자 exit 3(unknown flag·무효 --check명과 통일).
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

// schema — 상위 버전(예: 향후 v5)은 정상. 문자열 동등 "4" 검사였을 때
// v5 DB가 fail로 오탐됐다(수치 비교 n>=4 계약).
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

// last-seen — stall_hot_h 메타 오버라이드(24h)를 loadThresholds가 반영:
// 30h 경과 last_hot_at은 하드코딩 48h였다면 ok, 오버라이드 24h면 warn
// (maintain --check와 결론 일치).
func TestDoctorLastSeenThresholdOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := doctorQueryFn(
		time.Now().Add(-30*time.Hour), // last_write_at — 기본 72h 이내
		time.Now().Add(-30*time.Hour), // last_hot_at — 기본 48h 이내, 오버라이드 24h 초과
		time.Now().Add(-time.Hour))
	over := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "stall_hot_h") { // loadThresholds 질의
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

// hook-file — hooks.json 실체 형태(이벤트 배열 → hooks[].command 중첩)로
// 등록된 경우 ok.
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

// setHarnessEnvs — 하니스 5종 env를 전부 빈 임시 디렉터리로 격리.
// harnessChecks 단위 테스트의 전제(실홈 설정 파일 영향 차단).
func setHarnessEnvs(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"FLINT_CODEX_HOME", "FLINT_GEMINI_HOME", "FLINT_OPENCODE_HOME",
		"FLINT_COPILOT_HOME", "FLINT_CURSOR_DIR",
	} {
		t.Setenv(e, t.TempDir())
	}
}

// state — 설정 파일 전부 부재 → 하니스 검사 생성 없음(빈 슬라이스).
// 미설치 하니스는 경고가 아니다(§4 a).
func TestHarnessChecksSkipWhenAbsent(t *testing.T) {
	setHarnessEnvs(t)
	if got := harnessChecks(); len(got) != 0 {
		t.Fatalf("harnessChecks() = %v, want 빈 슬라이스", got)
	}
}

// file — codex config만 있고 flint 미등록 → 1건 fail. Name "harness-codex"·
// Fix에 문서 경로(§4 b).
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

// file — 본문에 "flint" 포함 → ok(등록 판정은 문자열 매칭, 포맷 무관 — C6).
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

// file — cursor는 프로젝트 스코프(.cursor/mcp.json) 우선: FLINT_CURSOR_DIR
// 루트의 프로젝트 파일을 검사 대상으로 삼는다(C5).
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

// hook-file — 본문에 'flint' 문자열만 있고 hooks.command 없으면 fail
// (전문 포함 검사였을 때 통과하던 우연 일치).
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

// TestDoctorCheckNameSpace — v0.20.2(패치 루프 1차 O2): 전체 리포트가
// 보여주는 체크명(skill-file·harness-*)이 --check 단독 실행으로 가능하다.
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

// TestDoctorEventVolume — v0.20.4(마일스톤 C①): 계측 3테이블 합계가
// events_volume_warn 임계를 넘으면 warn, 미만이면 ok(성장은 fail 아님).
func TestDoctorEventVolume(t *testing.T) {
	base := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 기본값(50000)
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

// TestDoctorSecretsScan — v0.26.0 R1: 보유 지식에서 비밀 후보 발견 시
// warn(패턴명+id만 — 본문 미출력).
func TestDoctorSecretsScan(t *testing.T) {
	hit := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil // thresholds 미설정 — 기본 상한
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			// v0.26.0 리뷰 ②: 전열 스캔(7열)·무효화 제외.
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

// TestDoctorSecretsScanSessions — v0.27.4(이월 T4): sessions.summary
// 발견 경로 — PII 최다 필드라는 도입 취지의 발견 케이스가 무테스트였다.
func TestDoctorSecretsScanSessions(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM sessions") {
			// 세션 요약에 실제 패턴 — 발견되어야 warn.
			return [][]any{{int64(3), "claude", "요약에 토큰 ghp_abcdefghijklmnopqrst 포함"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#session:3") {
		t.Errorf("세션 발견 케이스 = %+v", c)
	}
}

// TestDoctorSecretsScanRowLimitReached — v0.27.4(이월 T5): 행 상한 도달
// 분기 — 부분 스캔을 '미발견 ok'로 거짓 보고하지 않는다(LIMIT 경계 >=).
func TestDoctorSecretsScanRowLimitReached(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return [][]any{{"secrets_scan_row_limit", "2"}}, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			// 상한 2와 동일 수 반환 → 상한 도달(LIMIT 잘리면 더 있음).
			return [][]any{
				{int64(1), "토픽", "무해한 요약", "", "", "", ""},
				{int64(2), "토픽", "또 다른 무해한 요약", "", "", "", ""},
			}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "행 상한") {
		t.Errorf("행 상한 케이스 = %+v", c)
	}
}

// TestDoctorSecretsScanSessionsError — v0.27.4: 세션 조회 실패는 거짓
// ok가 아니라 warn(지식 경로 '지식 조회 실패'와 대칭).
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
	if c.Status != "warn" || !strings.Contains(c.Detail, "세션 조회 실패") {
		t.Errorf("세션 조회 실패 케이스 = %+v", c)
	}
}

// TestDoctorSecretsFindingsSurviveSessionError — v0.27.4 리뷰 3: 세션
// 조회 실패가 이미 잡은 지식 발견을 버리지 않는다(격차는 주석으로).
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
	if !strings.Contains(c.Detail, "세션 조회 실패") {
		t.Errorf("격차 미주석 = %+v", c)
	}
}

// TestDoctorSecretsScanProjects — v1.0.1 감사(F4): projects.name 사후
// 스캔 — restore·레거시 유입은 쓰기 시점 스캔을 우회한다(세션 경로와 같은
// 사각 폐쇄). 발견은 패턴명#project:id로만 보고(본문 미출력).
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

// TestDoctorMetaHygieneUnknownKeySanitized — v1.0.1 감사(F4): meta-hygiene
// 미지 키 샘플은 detail에 그대로 흐르는 출력이라 살균 출구를 통과한다.
func TestDoctorMetaHygieneUnknownKeySanitized(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"legacy[flint]key"}}, nil
		}
		return nil, nil
	}}
	c := checkMetaHygiene(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "미지 키 1건") {
		t.Fatalf("미지 키 경고 = %+v", c)
	}
	if strings.Contains(c.Detail, "[flint]") {
		t.Errorf("미지 키 원문 노출: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "legacy key") {
		t.Errorf("살균된 키 미출력: %s", c.Detail)
	}
}

// TestFlintHookCmdEvalWrapped — v1.0.1 감사(F4): doctor가 설치하는 훅
// 명령도 jq @sh+eval 패턴 — 이중따옴표 치환("$(jq …)") 재발 방지.
// v1.0.2 R2-A1: binPath 홑따옴표 인용 — 공백 경로의 무음 소실 방지.
func TestFlintHookCmdEvalWrapped(t *testing.T) {
	cmd := flintHookCmd("/x/flint", `session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`)["command"].(string)
	if !strings.HasPrefix(cmd, `eval "'/x/flint' `) {
		t.Errorf("eval 래핑·경로 인용 부재: %q", cmd)
	}
	if strings.Contains(cmd, `"$(jq`) {
		t.Errorf("이중따옴표 치환 잔존: %q", cmd)
	}
}

// TestFlintHookCmdQuotesBinPath — v1.0.2 감사(R2-A1): 공백 포함 설치 경로도
// 홑따옴표 1개 인자로 조립(무인용 시 eval 단어 분할로 훅 무음 소실).
// 조립 문자열 단언 + 멱등 식별 정합(flintHookSubRe가 신형 인용 형식과
// 구형 무인용 형식 모두 잡아야 재설치 중복 방지 유지).
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

// TestDoctorFullChainIncludesTailChecks — v0.28.1: 전체 실행 체인과
// doctorTailChecks(--check 허용 목록)의 정합 — v0.28.0에서 backup-key를
// 목록에만 추가하고 체인 append를 누락해 전체 doctor가 체크를 실행하지
// 않던 배선 누락의 재발 방지(이름 소스 단일화 검증).
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
