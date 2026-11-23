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

func TestEngineURLLoopbackAllowed(t *testing.T) {
	dsns := []string{
		"postgresql://flint@localhost:5434/flint",
		"postgresql://flint@127.0.0.1:5434/flint",
		"postgresql://flint@[::1]:5434/flint",
	}
	for _, dsn := range dsns {
		dir := withEngineConfigDir(t)
		out, code := mustRun(t, []string{"setup", "--engine-url", dsn}, "")
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
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@127.0.0.1:5599/flint"}, "")
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
		out, code := mustRun(t, []string{"setup", "--engine-url", dsn}, "")
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
	prev := []byte("{\n  \"port\": 5599,\n  \"dsn\": \"postgresql://flint:x@127.0.0.1:5434/flint\"\n}\n")
	if err := os.WriteFile(engineConfigPath(dir), prev, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@localhost:5435/flint"}, "")
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

func TestEngineURLInlineNoPasswordAllowed(t *testing.T) {
	dir := withEngineConfigDir(t)
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@127.0.0.1:5434/flint"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(비밀 없는 인라인 허용); out=%s", code, out)
	}
	cfg := readEngineConfig(t, dir)
	if cfg["engine_url"] != "postgresql://flint@127.0.0.1:5434/flint" {
		t.Fatalf("config engine_url=%v", cfg["engine_url"])
	}
}

func TestEngineURLEnvVarFullDSNAccepted(t *testing.T) {
	dir := withEngineConfigDir(t)
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

func TestEngineURLEnvVarFlagTakesPrecedence(t *testing.T) {
	dir := withEngineConfigDir(t)
	t.Setenv("FLINT_ENGINE_URL", "postgresql://envuser:pw@127.0.0.1:5999/flint")
	out, code := mustRun(t, []string{"setup", "--engine-url", "postgresql://flint@127.0.0.1:5434/flint"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(비밀 없는 인라인 인자 우선); out=%s", code, out)
	}
	cfg := readEngineConfig(t, dir)
	if cfg["engine_url"] != "postgresql://flint@127.0.0.1:5434/flint" {
		t.Fatalf("config engine_url=%v, want 인자 DSN 우선", cfg["engine_url"])
	}
}

func TestEngineURLFileFullDSNAcceptedV1300(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "")
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
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
