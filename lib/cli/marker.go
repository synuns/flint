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
	if flintHome() == "" {
		return ""
	}
	name := "pending-distill.json"
	if kind == markerKindRecall {
		name = "recall-hint.json"
	}
	return dataFile(name)
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

// flintHome — 개인 DB 인프라 데이터 루트(v0.28.3: 백업·키·마커·캐시를
// 한 공간에 — 설정(configDir)과 데이터의 의미 분리). FLINT_HOME env >
// FLINT_CONFIG_DIR(단일 디렉터리 오버라이드 겸 테스트 격리) > ~/.flint.
func flintHome() string {
	if d := os.Getenv("FLINT_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".flint")
}

// dataFile — FLINT_HOME 하위 런타임 아티팩트 경로. 레거시 위치
// (configDir)에 파일이 있고 새 위치에 없으면 이전한다(1회 마이그레이션
// — 복사 후 원본 제거, 실패는 조용히: 다음 호출 재시도).
func dataFile(name string) string {
	home := flintHome()
	if home == "" {
		return ""
	}
	dst := filepath.Join(home, name)
	if _, err := os.Stat(dst); err != nil {
		if old := configDir(); old != "" && old != home {
			src := filepath.Join(old, name)
			if b, rerr := os.ReadFile(src); rerr == nil {
				if merr := os.MkdirAll(home, 0o700); merr == nil {
					// v0.31.3(감사 3): 원자 쓰기 — tmp+rename으로 부분
					// 실패가 불완전 dst를 영구화하는 것을 방지한다.
					if tmp, terr := os.CreateTemp(home, "."+name+"-*"); terr == nil {
						tmpName := tmp.Name()
						_, werr := tmp.Write(b)
						tmp.Close()
						if werr == nil && os.Chmod(tmpName, 0o600) == nil && os.Rename(tmpName, dst) == nil {
							_ = os.Remove(src) // 이전 완료 — 레거시 잔삭제
						} else {
							_ = os.Remove(tmpName)
						}
					}
				}
			}
		}
	}
	return dst
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
