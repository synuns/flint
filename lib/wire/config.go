// config.go — T74 설정 파일(계약층 이동, v0.38.0 §1):
// configDir()/config.json의 DSN을 읽어 FLINT_DSN > config.json >
// DefaultDSN 우선순위로 해결한다(스펙 §2). 파손·부재는 조용히 폴백 —
// 설정 파일 문제로 커맨드가 죽지 않게.
package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultDSN — FLINT_DSN 미지정 시 기본 접속 문자열(ADR-0010: 포트 5434).
const DefaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

// ConfigDir — 설정 디렉터리 해결 주입점. 계약층은 파일시스템 위치 규칙을
// 소유하지 않으므로 커맨드 패키지(lib/cli marker.go configDir)가 init에서
// 주입한다. 미주입 시 설정 파일 경로는 ""(env/기본값 폴백만 동작).
var ConfigDir func() string

// configDirOrEmpty — 주입된 해결기 호출(미주입 시 "").
func configDirOrEmpty() string {
	if ConfigDir == nil {
		return ""
	}
	return ConfigDir()
}

// ConfigPath — configDir()/config.json. configDir이 ""면 "".
func ConfigPath() string {
	d := configDirOrEmpty()
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
