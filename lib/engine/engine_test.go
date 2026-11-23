package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withConfigDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	saved := configDir
	configDir = func() string { return dir }
	t.Cleanup(func() { configDir = saved })
	t.Setenv("FLINT_DSN", "")
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readJSON(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCurrentModeStored(t *testing.T) {
	for _, mode := range []string{"managed-container", "native", "external"} {
		withConfigDir(t, `{"engine":{"mode":"`+mode+`"}}`)
		if got := CurrentMode(); got != mode {
			t.Fatalf("CurrentMode=%q want %q", got, mode)
		}
	}
}

func TestCurrentModeInferContainer5434(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}`)
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("CurrentMode=%q want %q", got, ModeManagedContainer)
	}
}

func TestCurrentModeInferContainerPortOverride(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@127.0.0.1:5432/flint","port":5434}`)
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("CurrentMode=%q want %q", got, ModeManagedContainer)
	}
}

func TestCurrentModeInferTest5499(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5499/flint"}`)
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("CurrentMode=%q want %q", got, ModeManagedContainer)
	}
}

func TestCurrentModeInferExternal(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine_url":"postgresql://flint:x@localhost:5555/flint"}`)
	if got := CurrentMode(); got != ModeExternal {
		t.Fatalf("CurrentMode=%q want %q", got, ModeExternal)
	}
}

func TestCurrentModeInferUnknown(t *testing.T) {
	withConfigDir(t, "")
	if got := CurrentMode(); got != ModeUnknown {
		t.Fatalf("CurrentMode=%q want %q", got, ModeUnknown)
	}
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@dbhost:5434/flint"}`)
	if got := CurrentMode(); got != ModeUnknown {
		t.Fatalf("CurrentMode=%q want %q", got, ModeUnknown)
	}
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5555/flint"}`)
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("CurrentMode=%q want %q (비표준 루프백 포트는 관리형 추론 — v1.31.1)", got, ModeManagedContainer)
	}
}

func TestCurrentModeDoesNotWriteInference(t *testing.T) {
	dir := withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}`)
	CurrentMode()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}` {
		t.Fatalf("config mutated: %s", b)
	}
}

func TestSetModeValid(t *testing.T) {
	dir := withConfigDir(t, "")
	if err := SetMode("native"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentMode(); got != "native" {
		t.Fatalf("CurrentMode=%q want native", got)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm=%o want 600", perm)
	}
}

func TestSetModeInvalid(t *testing.T) {
	dir := withConfigDir(t, `{"engine":{"mode":"native"}}`)
	if err := SetMode("bad"); err == nil {
		t.Fatal("SetMode(bad) want error")
	}
	if err := SetMode(""); err == nil {
		t.Fatal("SetMode(empty) want error")
	}
	m := readJSON(t, dir)
	eng := m["engine"].(map[string]any)
	if eng["mode"] != "native" {
		t.Fatalf("mode mutated: %v", eng["mode"])
	}
}

func TestSetModePreservesKeys(t *testing.T) {
	body := `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine_url":"","port":5435,"engine":{"mode":"external","log_level":"info"},"extra":true}`
	dir := withConfigDir(t, body)
	if err := SetMode("managed-container"); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, dir)
	if m["dsn"] != "postgresql://flint:flint-dev@localhost:5434/flint" {
		t.Fatalf("dsn lost: %v", m["dsn"])
	}
	if m["port"] != float64(5435) {
		t.Fatalf("port lost: %v", m["port"])
	}
	if m["extra"] != true {
		t.Fatalf("extra lost: %v", m["extra"])
	}
	eng := m["engine"].(map[string]any)
	if eng["mode"] != "managed-container" {
		t.Fatalf("mode=%v", eng["mode"])
	}
	if eng["log_level"] != "info" {
		t.Fatalf("engine sub-key lost: %v", eng["log_level"])
	}
}

func TestModeDSNMismatchNativeEnvRemote(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine":{"mode":"native"}}`)
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@dbhost:5432/flint")
	if !ModeDSNMismatch() {
		t.Fatal("native + remote FLINT_DSN want mismatch")
	}
}

func TestModeDSNMismatchContainerRemoteDSN(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@dbhost:5434/flint","engine":{"mode":"managed-container"}}`)
	if !ModeDSNMismatch() {
		t.Fatal("managed-container + remote dsn want mismatch")
	}
}

func TestModeDSNMismatchExternalDefaultEndpoint(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine":{"mode":"external"}}`)
	if !ModeDSNMismatch() {
		t.Fatal("external + default managed endpoint want mismatch")
	}
}

func TestModeDSNMismatchConsistent(t *testing.T) {
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine":{"mode":"native"}}`)
	if ModeDSNMismatch() {
		t.Fatal("native + default dsn want no mismatch")
	}
	withConfigDir(t, `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint","engine_url":"postgresql://flint:x@localhost:5555/flint","engine":{"mode":"external"}}`)
	if ModeDSNMismatch() {
		t.Fatal("external + engine_url want no mismatch")
	}
	withConfigDir(t, "")
	if ModeDSNMismatch() {
		t.Fatal("unknown mode want no mismatch")
	}
}

func resetSeams(t *testing.T) {
	t.Helper()
	savedC, savedN := containerService, nativeService
	containerService, nativeService = ContainerService{}, NativeService{}
	t.Cleanup(func() { containerService, nativeService = savedC, savedN })
}

func TestDelegationContainerSeam(t *testing.T) {
	withConfigDir(t, `{"engine":{"mode":"managed-container"}}`)
	resetSeams(t)
	calls := map[string]bool{}
	containerService.Status = func() (ServiceStatus, error) {
		calls["status"] = true
		return ServiceStatus{Running: true, Version: "1.2.3"}, nil
	}
	containerService.Start = func() error { calls["start"] = true; return nil }
	containerService.Stop = func() error { calls["stop"] = true; return nil }
	containerService.Version = func() (string, error) { calls["version"] = true; return "1.2.3", nil }
	st, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Version != "1.2.3" {
		t.Fatalf("status=%+v", st)
	}
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(); err != nil {
		t.Fatal(err)
	}
	v, err := Version()
	if err != nil || v != "1.2.3" {
		t.Fatalf("version=%q err=%v", v, err)
	}
	for _, k := range []string{"status", "start", "stop", "version"} {
		if !calls[k] {
			t.Fatalf("seam %s not called", k)
		}
	}
}

func TestDelegationNativeSeam(t *testing.T) {
	withConfigDir(t, `{"engine":{"mode":"native"}}`)
	resetSeams(t)
	nativeService.Status = func() (ServiceStatus, error) {
		return ServiceStatus{Registered: true}, nil
	}
	st, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Registered {
		t.Fatalf("status=%+v", st)
	}
}

func TestDelegationNilSeam(t *testing.T) {
	withConfigDir(t, `{"engine":{"mode":"managed-container"}}`)
	resetSeams(t)
	if _, err := Status(); err == nil {
		t.Fatal("Status want error on nil seam")
	}
	if err := Start(); err == nil {
		t.Fatal("Start want error on nil seam")
	}
}

func TestDelegationExternal(t *testing.T) {
	withConfigDir(t, `{"engine":{"mode":"external"}}`)
	resetSeams(t)
	st, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != ModeExternal || st.Detail == "" {
		t.Fatalf("status=%+v", st)
	}
	if err := Start(); err == nil {
		t.Fatal("Start want rejection in external mode")
	}
	if err := Stop(); err == nil {
		t.Fatal("Stop want rejection in external mode")
	}
	if _, err := Version(); err == nil {
		t.Fatal("Version want rejection in external mode")
	}
}

func TestDelegationUnknownMode(t *testing.T) {
	withConfigDir(t, "")
	resetSeams(t)
	if _, err := Status(); err == nil {
		t.Fatal("Status want rejection on unknown mode")
	}
	if err := Start(); err == nil {
		t.Fatal("Start want rejection on unknown mode")
	}
	if err := Stop(); err == nil {
		t.Fatal("Stop want rejection on unknown mode")
	}
	if _, err := Version(); err == nil {
		t.Fatal("Version want rejection on unknown mode")
	}
}
