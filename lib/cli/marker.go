// marker.go — T71 마커 헬퍼: PostCompact가 남기고 Stop/SessionStart가 소비하는
// pending-distill.json (적립 증류 대기 표시). 개인 단일 에이전트 가정 — 원자성 불요.
// v0.18.2: 회상 부재 1회성 마커(recall-hint.json) 분리 — 내구성(증류 대기)과
// 1회성(회상 안내)은 수명 주기가 달라 같은 파일을 쓰면 서로를 덮어쓴다.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Marker — 마커 파일 내용(종류 공통).
type Marker struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// 마커 종류 — 파일명이 달라진다(동시 존재 가능).
const (
	markerKindDistill = "distill" // pending-distill.json — 내구(증류 시까지)
	markerKindRecall  = "recall"  // recall-hint.json — 1회성(다음 SessionStart)
)

// markerFileOf — 종류별 마커 파일 전체 경로. configDir이 ""면 "".
func markerFileOf(kind string) string {
	d := configDir()
	if d == "" {
		return ""
	}
	name := "pending-distill.json"
	if kind == markerKindRecall {
		name = "recall-hint.json"
	}
	return filepath.Join(d, name)
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

// MarkerFile — 증류 대기 마커 파일 전체 경로(하위 호환 별명).
func MarkerFile() string { return markerFileOf(markerKindDistill) }

// RecallMarkerFile — 회상 부재 마커 파일 전체 경로(v0.18.2).
func RecallMarkerFile() string { return markerFileOf(markerKindRecall) }

// WriteMarker — 증류 대기 마커 기록(하위 호환 별명).
func WriteMarker(sessionID string, reason string) error {
	return WriteTypedMarker(markerKindDistill, sessionID, reason)
}

// WriteTypedMarker — JSON 마샬 후 디렉터리(0700) 생성하고 종류별 마커 기록.
// CreatedAt은 현재 UTC RFC3339.
func WriteTypedMarker(kind, sessionID, reason string) error {
	path := markerFileOf(kind)
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
func ReadMarker() (*Marker, error) { return ReadTypedMarker(markerKindDistill) }

// ReadTypedMarker — 종류별 마커 읽기(v0.18.2). 부재는 (nil, nil).
func ReadTypedMarker(kind string) (*Marker, error) {
	path := markerFileOf(kind)
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

// ClearMarker — 증류 대기 마커 삭제(하위 호환 별명). 파일이 없어도 nil.
func ClearMarker() error { return ClearTypedMarker(markerKindDistill) }

// ClearTypedMarker — 종류별 마커 삭제(v0.18.2). 파일이 없어도 nil.
func ClearTypedMarker(kind string) error {
	path := markerFileOf(kind)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SessionLabelFile — 현재 세션 라벨 파일 경로(v0.20.5 패치 루프 5차·
// 마일스톤 B②). session-start 훅이 하니스 세션 id를 남기고 read가
// --session 미지정 시 조용히 채택한다 — G4 라벨 자동화(에이전트 협조
// 불필요). session-end가 지운다(종료 후 스테일 라벨 방지). 병렬 세션은
// last-writer-wins(라벨은 보조 계정 — 룰링).
func SessionLabelFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "session-current")
}

// WriteSessionLabel — 현재 세션 라벨 1줄 기록(빈 라벨은 미기록).
func WriteSessionLabel(label string) error {
	if label == "" {
		return nil
	}
	path := SessionLabelFile()
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(label), 0o600)
}

// ReadSessionLabel — 현재 세션 라벨(부재·빈 파일은 ok=false).
func ReadSessionLabel() (string, bool) {
	b, err := os.ReadFile(SessionLabelFile())
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	return s, s != ""
}

// ClearSessionLabel — 세션 라벨 파일 제거(session-end).
func ClearSessionLabel() {
	_ = os.Remove(SessionLabelFile())
}
