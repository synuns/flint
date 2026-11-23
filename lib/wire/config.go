package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const DefaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

var ConfigDir func() string

func configDirOrEmpty() string {
	if ConfigDir == nil {
		return ""
	}
	return ConfigDir()
}

func ConfigPath() string {
	d := configDirOrEmpty()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

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

func ResolveDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	if d := LoadConfigDSN(); d != "" {
		return d
	}
	return DefaultDSN
}
