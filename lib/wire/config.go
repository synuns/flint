// config.go — the settings file: reads configDir()/config.json's DSN and
// resolves it with the priority FLINT_DSN > config.json > DefaultDSN (spec §2).
// Corrupt or absent falls back silently — a settings-file problem must not kill
// the command.
package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultDSN — the default connection string when FLINT_DSN is unset (ADR-0010: port 5434).
const DefaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

// ConfigDir — the settings-directory resolution injection point. The contract
// layer does not own filesystem location rules, so the command package (lib/cli
// marker.go configDir) injects it at init. When not injected, the settings-file path is "" (only the env/default fallbacks operate).
var ConfigDir func() string

// configDirOrEmpty — calls the injected resolver ("" when not injected).
func configDirOrEmpty() string {
	if ConfigDir == nil {
		return ""
	}
	return ConfigDir()
}

// ConfigPath — configDir()/config.json. "" when configDir is "".
func ConfigPath() string {
	d := configDirOrEmpty()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

// LoadConfigDSN — parses {"dsn":string}. Absent, corrupt, empty, or non-string -> "".
func LoadConfigDSN() string {
	p := ConfigPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var c struct {
		DSN any `json:"dsn"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return ""
	}
	s, ok := c.DSN.(string)
	if !ok || s == "" {
		return ""
	}
	return s
}

// ResolveDSN — FLINT_DSN > LoadConfigDSN > DefaultDSN.
func ResolveDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	if d := LoadConfigDSN(); d != "" {
		return d
	}
	return DefaultDSN
}
