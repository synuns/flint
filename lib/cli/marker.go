// marker.go — T71 마커 헬퍼: PostCompact가 남기고 Stop/SessionStart가 소비하는
// pending-distill.json (적립 증류 대기 표시). 개인 단일 에이전트 가정 — 원자성 불요.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Marker — 증류 대기 마커 파일 내용.
type Marker struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// configDir — FLINT_CONFIG_DIR 우선, 없으면 os.UserConfigDir()/flint.
// UserConfigDir 실패 시 "" (MarkerFile도 "" → 호출측 조용히 스킵).
func configDir() string {
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "flint")
}

// MarkerFile — 마커 파일 전체 경로. configDir이 ""면 "".
func MarkerFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "pending-distill.json")
}

// WriteMarker — JSON 마샬 후 디렉터리(0700) 생성하고 마커 기록.
// CreatedAt은 현재 UTC RFC3339.
func WriteMarker(sessionID string, reason string) error {
	path := MarkerFile()
	if path == "" {
		return os.ErrInvalid
	}
	m := Marker{SessionID: sessionID, Reason: reason, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadMarker — 마커 조회. 부재 시 (nil, nil), 파손 시 (nil, err).
func ReadMarker() (*Marker, error) {
	path := MarkerFile()
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ClearMarker — 마커 삭제. 파일이 없어도 nil.
func ClearMarker() error {
	path := MarkerFile()
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
