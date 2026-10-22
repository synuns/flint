// doctor_test.go — T64: flint doctor 검사 5종·exit 계약(fail만 1·warn 0)·
// FLINT_CLAUDE_HOME 오버라이드(--check 무효명 exit 3).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runDoctorArgs — fake store으로 runDoctor 실행. 출력·exit 반환.
func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runDoctor(context.Background(), f, args, strings.NewReader(""), &out)
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

// DoctorChecks — 고정 5종 순서 계약.
func TestDoctorChecksContract(t *testing.T) {
	want := []string{"hook-file", "db", "schema", "integrity", "last-seen"}
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
	if _, ok := Lookup("doctor"); !ok {
		t.Fatal("doctor 미등록")
	}
}

// --list-checks — 한 줄 JSON에 검사 5종.
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
	if len(m.Checks) != 5 || m.Checks[0] != "hook-file" || m.Checks[4] != "last-seen" {
		t.Errorf("checks=%v, want 5종", m.Checks)
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
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
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
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
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
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
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
		t.Errorf("exit=%d, want 3 (출력 %q)", code, out)
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
		t.Errorf("exit=%d, want 3 (출력 %q)", code, out)
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
	if !strings.Contains(out, "hot(SessionStart)") {
		t.Errorf("fix 문구가 실제 등록 절차(hot/done 훅)를 안내하지 않음: %q", out)
	}
}
