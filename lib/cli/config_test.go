package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) {
	t.Helper()
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := os.WriteFile(ConfigPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func unsetDSN(t *testing.T) {
	t.Helper()
	t.Setenv("FLINT_DSN", "")
}

func TestConfigPathUnderConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if got, want := ConfigPath(), filepath.Join(dir, "config.json"); got != want {
		t.Fatalf("ConfigPath=%q want %q", got, want)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Run("env beats file", func(t *testing.T) {
		writeConfig(t, `{"dsn":"postgresql://file:5434/flint"}`)
		t.Setenv("FLINT_DSN", "postgresql://env:5433/flint")
		if got := ResolveDSN(); got != "postgresql://env:5433/flint" {
			t.Fatalf("ResolveDSN=%q want env DSN", got)
		}
	})
	t.Run("file when env absent", func(t *testing.T) {
		unsetDSN(t)
		writeConfig(t, `{"dsn":"postgresql://file:5434/flint"}`)
		if got := ResolveDSN(); got != "postgresql://file:5434/flint" {
			t.Fatalf("ResolveDSN=%q want file DSN", got)
		}
	})
	t.Run("default when both absent", func(t *testing.T) {
		unsetDSN(t)
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
		if got := ResolveDSN(); got != defaultDSN {
			t.Fatalf("ResolveDSN=%q want defaultDSN=%q", got, defaultDSN)
		}
	})
}

func TestConfigCorruptFallback(t *testing.T) {
	unsetDSN(t)
	cases := map[string]string{
		"corrupt json":   `{"dsn":`,
		"empty dsn":      `{"dsn":""}`,
		"non-string":     `{"dsn":5433}`,
		"null dsn":       `{"dsn":null}`,
		"dsn key absent": `{"other":"x"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, body)
			if got := LoadConfigDSN(); got != "" {
				t.Fatalf("LoadConfigDSN=%q want \"\"", got)
			}
			if got := ResolveDSN(); got != defaultDSN {
				t.Fatalf("ResolveDSN=%q want defaultDSN", got)
			}
		})
	}
	t.Run("file absent", func(t *testing.T) {
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
		if got := LoadConfigDSN(); got != "" {
			t.Fatalf("LoadConfigDSN=%q want \"\"", got)
		}
	})
}
