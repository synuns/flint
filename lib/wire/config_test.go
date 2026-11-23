package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prev := ConfigDir
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = prev })
	t.Setenv("FLINT_DSN", "")
	return dir
}

func TestResolveDSNConfigPortSubstituted(t *testing.T) {
	withConfigDir(t, map[string]string{
		"config.json": `{"port": 5599}` + "\n",
	})
	got := ResolveDSN()
	if !strings.Contains(got, ":5599") {
		t.Fatalf("ResolveDSN=%q, want 포트 5599 치환(기본 DSN)", got)
	}
	if strings.Contains(got, ":5434") {
		t.Fatalf("기본 포트 잔존: %q", got)
	}
}

func TestResolveDSNPortAppliesToConfigDSN(t *testing.T) {
	withConfigDir(t, map[string]string{
		"config.json": `{"dsn": "postgresql://flint:pw@localhost:5434/flint?sslmode=disable", "port": 6100}` + "\n",
	})
	got := ResolveDSN()
	for _, want := range []string{":6100", "flint:pw@", "/flint?sslmode=disable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ResolveDSN=%q, want %q 포함", got, want)
		}
	}
	if strings.Contains(got, ":5434") {
		t.Fatalf("원 포트 잔존: %q", got)
	}
}

func TestResolveDSNEngineURLWinsOverDSNAndPort(t *testing.T) {
	withConfigDir(t, map[string]string{
		"config.json": `{"dsn": "postgresql://flint@127.0.0.1:5434/flint", "engine_url": "postgresql://flint:pw@127.0.0.1:5599/flint", "port": 7000}` + "\n",
	})
	if got := ResolveDSN(); got != "postgresql://flint:pw@127.0.0.1:5599/flint" {
		t.Fatalf("ResolveDSN=%q, want engine_url 원문 우선(포트 미치환)", got)
	}
	if got := LoadConfigDSN(); got != "postgresql://flint:pw@127.0.0.1:5599/flint" {
		t.Fatalf("LoadConfigDSN=%q, want engine_url 우선", got)
	}
}

func TestResolveDSNEnvWinsOverConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"dsn": "postgresql://cfg@127.0.0.1:5434/flint"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := ConfigDir
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = prev })
	t.Setenv("FLINT_DSN", "postgresql://env@127.0.0.1:6432/flint")
	if got := ResolveDSN(); got != "postgresql://env@127.0.0.1:6432/flint" {
		t.Fatalf("ResolveDSN=%q, want FLINT_DSN 환경 우선", got)
	}
}

func TestResolveDSNIgnoresInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "70000", "abc"} {
		withConfigDir(t, map[string]string{
			"config.json": `{"port": "` + port + `"}` + "\n",
		})
		if got := ResolveDSN(); !strings.Contains(got, ":5434") {
			t.Fatalf("port=%s: ResolveDSN=%q, want 기본 포트 유지", port, got)
		}
	}
}

func TestResolveDSNDefaults(t *testing.T) {
	withConfigDir(t, map[string]string{})
	if got := ResolveDSN(); got != DefaultDSN {
		t.Fatalf("ResolveDSN=%q, want 기본 DSN", got)
	}
}

func TestWithDSNPort(t *testing.T) {
	cases := []struct {
		in   string
		port int
		want string
	}{
		{"postgresql://flint:pw@localhost:5434/flint", 5599, "postgresql://flint:pw@localhost:5599/flint"},
		{"postgresql://flint@127.0.0.1/flint", 5599, "postgresql://flint@127.0.0.1:5599/flint"},
		{"postgresql://flint:pw@[::1]:5434/flint?sslmode=disable", 5599, "postgresql://flint:pw@[::1]:5599/flint?sslmode=disable"},
	}
	for _, tc := range cases {
		if got := WithDSNPort(tc.in, tc.port); got != tc.want {
			t.Fatalf("WithDSNPort(%q,%d)=%q, want %q", tc.in, tc.port, got, tc.want)
		}
	}
	if got := WithDSNPort("not a url", 5599); got != "not a url" {
		t.Fatalf("비DSN 입력 변형: %q", got)
	}
}
