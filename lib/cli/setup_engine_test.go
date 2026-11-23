package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withEngineConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	return dir
}

func engineConfigPath(dir string) string {
	return filepath.Join(dir, "config.json")
}

func readEngineConfig(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(engineConfigPath(dir))
	if err != nil {
		t.Fatalf("config.json 읽기 실패: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("config.json 파싱 실패: %v", err)
	}
	return m
}

func withEngineHandshake(t *testing.T, fn func(ctx context.Context, dsn string) error) {
	t.Helper()
	prev := engineHandshakeFn
	engineHandshakeFn = fn
	t.Cleanup(func() { engineHandshakeFn = prev })
}

func withEngineHandshakeOK(t *testing.T) {
	t.Helper()
	withEngineHandshake(t, func(ctx context.Context, dsn string) error { return nil })
}

func TestEngineURLLoopbackAllowed(t *testing.T) {
	dsns := []string{
		"postgresql://flint@localhost:5434/flint",
		"postgresql://flint@127.0.0.1:5434/flint",
		"postgresql://flint@[::1]:5434/flint",
	}
	for _, dsn := range dsns {
		dir := withEngineConfigDir(t)
		withEngineHandshakeOK(t)
		t.Setenv("FLINT_ENGINE_URL", dsn)
		out, code := mustRun(t, []string{"setup"}, "")
		if code != 0 {
			t.Fatalf("%s: exit=%d, want 0; out=%s", dsn, code, out)
		}
		m := decode(t, out)
		if m["status"] != "ok" {
			t.Fatalf("%s: status=%v, want ok; out=%s", dsn, m["status"], out)
		}
		cfg := readEngineConfig(t, dir)
		if cfg["engine_url"] != dsn {
			t.Fatalf("%s: config 저장값=%v, want 원문 DSN", dsn, cfg["engine_url"])
		}
		info, err := os.Stat(engineConfigPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: 권한=%v, want 0600", dsn, info.Mode().Perm())
		}
	}
}

func TestEngineModeRecordedExternal(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshakeOK(t)
	t.Setenv("FLINT_ENGINE_URL", "postgresql://flint@127.0.0.1:5599/flint")
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	cfg := readEngineConfig(t, dir)
	eng, ok := cfg["engine"].(map[string]any)
	if !ok || eng["mode"] != "external" {
		t.Fatalf("engine.mode=%v, want external; cfg=%v", cfg["engine"], cfg)
	}
	if m := decode(t, out); m["mode"] != "external" {
		t.Fatalf("출력 mode=%v, want external; out=%s", m["mode"], out)
	}
}

func TestEngineURLMaskedInOutputOnly(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshakeOK(t)
	dsn := "postgresql://flint:s3cr3tpw@127.0.0.1:5434/flint"
	t.Setenv("FLINT_ENGINE_URL", dsn)
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.Contains(out, "s3cr3tpw") {
		t.Fatalf("출력에 비밀번호 노출: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("마스킹 표식 없음: %s", out)
	}
	cfg := readEngineConfig(t, dir)
	if cfg["engine_url"] != dsn {
		t.Fatalf("config 저장값=%v, want 원문(0600 저장은 마스킹 없음)", cfg["engine_url"])
	}
}

func TestEngineURLRejectedNonLoopback(t *testing.T) {
	cases := []string{
		"postgresql://flint@10.0.0.5:5434/flint",
		"postgresql://flint@dbhost:5434/flint",
		"postgresql:///var/run/postgresql",
		"http://127.0.0.1:5434/flint",
	}
	for _, dsn := range cases {
		dir := withEngineConfigDir(t)
		withEngineHandshakeOK(t)
		t.Setenv("FLINT_ENGINE_URL", dsn)
		out, code := mustRun(t, []string{"setup"}, "")
		if code != 3 {
			t.Fatalf("%s: exit=%d, want 3; out=%s", dsn, code, out)
		}
		if !strings.Contains(out, "loopback") {
			t.Fatalf("%s: 루프백 안내 문구 없음: %s", dsn, out)
		}
		if _, err := os.Stat(engineConfigPath(dir)); !os.IsNotExist(err) {
			t.Fatalf("%s: 거부 시 config.json이 기록됨", dsn)
		}
	}
}

func TestEngineURLRequiresValue(t *testing.T) {
	withEngineConfigDir(t)
	out, code := mustRun(t, []string{"setup", "--engine-url"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "--engine-url") {
		t.Fatalf("값 누락 안내 없음: %s", out)
	}
}

func TestEngineURLPreservesConfigKeys(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshakeOK(t)
	prev := []byte("{\n  \"port\": 5599,\n  \"dsn\": \"postgresql://flint:x@127.0.0.1:5434/flint\"\n}\n")
	if err := os.WriteFile(engineConfigPath(dir), prev, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLINT_ENGINE_URL", "postgresql://flint@localhost:5435/flint")
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	cfg := readEngineConfig(t, dir)
	if cfg["port"] != float64(5599) {
		t.Fatalf("기존 port 키 유실: %v", cfg["port"])
	}
	if cfg["dsn"] != "postgresql://flint:x@127.0.0.1:5434/flint" {
		t.Fatalf("기존 dsn 키 유실: %v", cfg["dsn"])
	}
	if cfg["engine_url"] != "postgresql://flint@localhost:5435/flint" {
		t.Fatalf("engine_url 미기록: %v", cfg["engine_url"])
	}
}

func TestEngineURLHandshakeSeam(t *testing.T) {
	withEngineConfigDir(t)
	called := ""
	withEngineHandshake(t, func(ctx context.Context, dsn string) error {
		called = dsn
		return errors.New("not doltgres")
	})
	dsn := "postgresql://flint:pw@127.0.0.1:5434/flint"
	t.Setenv("FLINT_ENGINE_URL", dsn)
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1(악수 실패); out=%s", code, out)
	}
	if called != dsn {
		t.Fatalf("악수 DSN=%q", called)
	}
	withEngineHandshake(t, func(ctx context.Context, dsn string) error { return nil })
	out, code = mustRun(t, []string{"setup"}, "")
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("악수 성공 exit=%d out=%s", code, out)
	}
}

func TestEngineURLPassthroughOtherSetup(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--dry"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(래퍼 미간섭); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "dry" {
		t.Fatalf("status=%v, want dry; out=%s", m["status"], out)
	}
	if rec.upCalls != 0 {
		t.Fatalf("dry에서 compose 실행: %d", rec.upCalls)
	}
}

func TestMaskEngineURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgresql://flint:pw@127.0.0.1:5434/flint", "postgresql://flint:***@127.0.0.1:5434/flint"},
		{"postgresql://flint:p@ss@127.0.0.1:5434/flint", "postgresql://flint:***@127.0.0.1:5434/flint"},
		{"postgresql://flint@127.0.0.1:5434/flint", "postgresql://flint@127.0.0.1:5434/flint"},
		{"postgresql://127.0.0.1:5434/flint", "postgresql://127.0.0.1:5434/flint"},
		{"postgresql://flint:pw@[::1]:5434/flint?sslmode=disable", "postgresql://flint:***@[::1]:5434/flint?sslmode=disable"},
	}
	for _, tc := range cases {
		if got := MaskEngineURL(tc.in); got != tc.want {
			t.Fatalf("MaskEngineURL(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEngineURLInlinePasswordRejectedV1300(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "")
	withEngineConfigDir(t)
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://u:secret@127.0.0.1:5434/flint"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "FLINT_ENGINE_URL") {
		t.Fatalf("안내 부재: %s", out)
	}
}

func TestEngineURLEnvFullDSNAcceptedV1300(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "postgresql://u:secret@127.0.0.1:5434/flint")
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	withEngineHandshakeOK(t)
	var b strings.Builder
	code := runSetupEngineURL(context.Background(), &b, "postgresql://u:secret@127.0.0.1:5434/flint")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(env DSN); out=%s", code, b.String())
	}
	if !strings.Contains(b.String(), "***") {
		t.Fatalf("마스킹 부재: %s", b.String())
	}
}

func TestEngineURLInlinePasswordRejected(t *testing.T) {
	dir := withEngineConfigDir(t)
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint:s3cr3tpw@127.0.0.1:5434/flint"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(인라인 비밀 거부); out=%s", code, out)
	}
	if !strings.Contains(out, "FLINT_ENGINE_URL") {
		t.Fatalf("FLINT_ENGINE_URL 안내 없음: %s", out)
	}
	if _, err := os.Stat(engineConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("거부 시 config.json이 기록됨")
	}
}

func TestEngineURLInlineValueRejectedV1301(t *testing.T) {
	dir := withEngineConfigDir(t)
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@127.0.0.1:5434/flint"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(인라인 값 전면 거부); out=%s", code, out)
	}
	if !strings.Contains(out, "FLINT_ENGINE_URL") || !strings.Contains(out, "--engine-url-file") {
		t.Fatalf("대체 경로 안내 없음: %s", out)
	}
	if _, err := os.Stat(engineConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("거부 시 config.json이 기록됨")
	}
}

func TestEngineURLEnvVarFullDSNAccepted(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshakeOK(t)
	t.Setenv("FLINT_ENGINE_URL", "postgresql://flint:s3cr3tpw@127.0.0.1:5434/flint")
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(환경변수는 전체 DSN 허용); out=%s", code, out)
	}
	if strings.Contains(out, "s3cr3tpw") {
		t.Fatalf("출력에 비밀번호 노출: %s", out)
	}
	cfg := readEngineConfig(t, dir)
	if cfg["engine_url"] != "postgresql://flint:s3cr3tpw@127.0.0.1:5434/flint" {
		t.Fatalf("config에 원문 DSN 미기록: %v", cfg["engine_url"])
	}
	eng, _ := cfg["engine"].(map[string]any)
	if eng["mode"] != "external" {
		t.Fatalf("engine.mode=%v, want external", eng["mode"])
	}
}

func TestEngineURLFlagRejectedEvenWithEnvV1301(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshakeOK(t)
	t.Setenv("FLINT_ENGINE_URL", "postgresql://envuser:pw@127.0.0.1:5999/flint")
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@127.0.0.1:5434/flint"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(플래그 인라인은 환경변수가 있어도 거부); out=%s", code, out)
	}
	if _, err := os.Stat(engineConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("거부 시 config.json이 기록됨")
	}
}

func TestEngineURLFileFullDSNAcceptedV1300(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "")
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	withEngineHandshakeOK(t)
	f := filepath.Join(dir, "engine-url")
	if err := os.WriteFile(f, []byte("postgresql://u:secret@127.0.0.1:5434/flint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	code := runSetup(context.Background(), nil, []string{"--engine-url-file", f}, strings.NewReader(""), &b)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, b.String())
	}
	if !strings.Contains(b.String(), "***") || strings.Contains(b.String(), "secret") {
		t.Fatalf("마스킹 위반: %s", b.String())
	}
}

func TestEngineURLFilePermWarnsV1301(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "")
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	withEngineHandshakeOK(t)
	f := filepath.Join(dir, "engine-url")
	if err := os.WriteFile(f, []byte("postgresql://u:secret@127.0.0.1:5434/flint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errs strings.Builder
	prevStderr := setupStderr
	setupStderr = &errs
	t.Cleanup(func() { setupStderr = prevStderr })
	var b strings.Builder
	code := runSetup(context.Background(), nil, []string{"--engine-url-file", f}, strings.NewReader(""), &b)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고는 진행 방해 없음); out=%s", code, b.String())
	}
	warned := errs.String()
	if !strings.Contains(warned, "0600") {
		t.Fatalf("0600 권고 경고 없음: %q", warned)
	}
	if lines := strings.Split(strings.TrimRight(warned, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("경고는 1행이어야 한다: %q", warned)
	}
	cfg, rerr := os.ReadFile(filepath.Join(dir, "config.json"))
	if rerr != nil || !strings.Contains(string(cfg), "engine_url") {
		t.Fatalf("진행 후 config 기록: %v %s", rerr, cfg)
	}
}

func TestEngineURLHandshakeBeforeWriteV1301(t *testing.T) {
	dir := withEngineConfigDir(t)
	withEngineHandshake(t, func(ctx context.Context, dsn string) error {
		return errors.New("engine not doltgres")
	})
	t.Setenv("FLINT_ENGINE_URL", "postgresql://flint@127.0.0.1:5434/flint")
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1(악수 실패); out=%s", code, out)
	}
	if _, err := os.Stat(engineConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("악수 실패 전 config.json이 먼저 기록됨(순서 위반)")
	}
}

func TestEngineHandshakeDefaultRequiresDoltgres(t *testing.T) {
	postgresOnly := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "version()") {
			return [][]any{{"PostgreSQL 15.5"}}, nil
		}
		return nil, errors.New("no dolt log")
	}}
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return postgresOnly, nil }
	t.Cleanup(func() { newStore = prev })
	err := engineHandshakeDefault(context.Background(), "postgresql://flint@127.0.0.1:5434/flint")
	if err == nil || !strings.Contains(err.Error(), "doltgres") {
		t.Fatalf("postgres 엔진 거부 없음: %v", err)
	}

	dolt := &fakeStore{queryFn: engineHandshakeQueryFn}
	newStore = func(ctx context.Context, dsn string) (Store, error) { return dolt, nil }
	if err := engineHandshakeDefault(context.Background(), "postgresql://flint@127.0.0.1:5434/flint"); err != nil {
		t.Fatalf("doltgres 엔진 오류: %v", err)
	}
}
