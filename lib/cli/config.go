// config.go — T74 설정 파일: configDir/config.json의 DSN을 읽어
// FLINT_DSN > config.json > defaultDSN 우선순위로 해결한다(스펙 §2).
// 파손·부재는 조용히 폴백 — 설정 파일 문제로 커맨드가 죽지 않게.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ConfigPath — configDir()/config.json. configDir이 ""면 "".
func ConfigPath() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

// LoadConfigDSN — {"dsn":string} 파싱. 부재·파손·빈값·비문자열 → "".
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

// ResolveDSN — FLINT_DSN > LoadConfigDSN > defaultDSN.
func ResolveDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	if d := LoadConfigDSN(); d != "" {
		return d
	}
	return defaultDSN
}
