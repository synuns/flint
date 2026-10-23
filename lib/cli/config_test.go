// config_test.go — T74 Review Focus: test_resolve_precedence ·
// test_config_corrupt_fallback. ConfigPath·LoadConfigDSN·ResolveDSN 계약 검증.
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig — FLINT_CONFIG_DIR을 TempDir로 잡고 config.json 기록.
func writeConfig(t *testing.T, content string) {
	t.Helper()
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := os.WriteFile(ConfigPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unsetDSN — FLINT_DSN 해제 후 테스트 종료 시 복원.
func unsetDSN(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("FLINT_DSN")
	os.Unsetenv("FLINT_DSN")
	t.Cleanup(func() {
		if had {
			os.Setenv("FLINT_DSN", old)
		} else {
			os.Unsetenv("FLINT_DSN")
		}
	})
}

// TestConfigPathUnderConfigDir — ConfigPath = configDir()/config.json.
func TestConfigPathUnderConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if got, want := ConfigPath(), filepath.Join(dir, "config.json"); got != want {
		t.Fatalf("ConfigPath=%q want %q", got, want)
	}
}

// TestResolvePrecedence — FLINT_DSN > config.json > defaultDSN (스펙 §2).
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
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir()) // config.json 부재 보장
		if got := ResolveDSN(); got != defaultDSN {
			t.Fatalf("ResolveDSN=%q want defaultDSN=%q", got, defaultDSN)
		}
	})
}

// TestConfigCorruptFallback — 부재·파손·빈값·비문자열 dsn은 ""로 폴백.
func TestConfigCorruptFallback(t *testing.T) {
	unsetDSN(t)
	cases := map[string]string{
		"corrupt json":  `{"dsn":`,
		"empty dsn":     `{"dsn":""}`,
		"non-string":    `{"dsn":5433}`,
		"null dsn":      `{"dsn":null}`,
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
