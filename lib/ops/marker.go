// marker.go — T71 마커 헬퍼: PostCompact가 남기고 Stop/SessionStart가 소비하는
// pending-distill.json (적립 증류 대기 표시). 개인 단일 에이전트 가정 — 원자성 불요.
// v0.18.2: 회상 부재 1회성 마커(recall-hint.json) 분리 — 내구성(증류 대기)과
// 1회성(회상 안내)은 수명 주기가 달라 같은 파일을 쓰면 서로를 덮어쓴다.
// v0.40.0 T2: lib/cli에서 이동(동작·출력·exit 불변 — 식별자 대문자화).
// knowledge의 WriteTypedMarkerFn seam은 cli init이 이 내보내기 함수(별칭
// 경유)를 주입받는다 — 동일 계약 유지.
package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// Marker — 마커 파일 내용(종류 공통).
type Marker struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// 마커 종류 — 파일명이 달라진다(동시 존재 가능).
const (
	MarkerKindDistill = "distill" // pending-distill.json — 내구(증류 시까지)
	MarkerKindRecall  = "recall"  // recall-hint.json — 1회성(다음 SessionStart)
)

// markerFileOf — 종류별 마커 파일 전체 경로. configDir이 ""면 "".
func markerFileOf(kind string) string {
	if flintHome() == "" {
		return ""
	}
	name := "pending-distill.json"
	if kind == MarkerKindRecall {
		name = "recall-hint.json"
	}
	return DataFile(name)
}

// ConfigDir — FLINT_CONFIG_DIR 우선, 없으면 os.UserConfigDir()/flint.
// UserConfigDir 실패 시 "" (MarkerFile도 "" → 호출측 조용히 스킵).
func ConfigDir() string {
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

// DataFile — FLINT_HOME 하위 런타임 아티팩트 경로. 레거시 위치
// (configDir)에 파일이 있고 새 위치에 없으면 이전한다(1회 마이그레이션
// — 복사 후 원본 제거, 실패는 조용히: 다음 호출 재시도).
func DataFile(name string) string {
	home := flintHome()
	if home == "" {
		return ""
	}
	dst := filepath.Join(home, name)
	if _, err := os.Stat(dst); err != nil {
		if old := ConfigDir(); old != "" && old != home {
			src := filepath.Join(old, name)
			if b, rerr := os.ReadFile(src); rerr == nil {
				if merr := os.MkdirAll(home, 0o700); merr == nil {
					// v0.33.0(패치 감사 4): 이전 시도의 크래시 잔존 tmp 청산 —
					// CreateTemp 직전에 동일 패턴을 지운다(1회성 마이그레이션).
					if leftovers, gerr := filepath.Glob(filepath.Join(home, "."+name+"-*")); gerr == nil {
						for _, lv := range leftovers {
							_ = os.Remove(lv)
						}
					}
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

func init() {
	// 계약층 ConfigDir 주입(마커 이동과 함께 ops 소유로 — cli cliwire_alias
	// init의 동일 주입과 같은 함수값이라 이중 대입 무해).
	wire.ConfigDir = ConfigDir
}

// MarkerFile — 증류 대기 마커 파일 전체 경로(하위 호환 별명).
func MarkerFile() string { return markerFileOf(MarkerKindDistill) }

// RecallMarkerFile — 회상 부재 마커 파일 전체 경로(v0.18.2).
func RecallMarkerFile() string { return markerFileOf(MarkerKindRecall) }

// WriteMarker — 증류 대기 마커 기록(하위 호환 별명).
func WriteMarker(sessionID string, reason string) error {
	return WriteTypedMarker(MarkerKindDistill, sessionID, reason)
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
func ReadMarker() (*Marker, error) { return ReadTypedMarker(MarkerKindDistill) }

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
func ClearMarker() error { return ClearTypedMarker(MarkerKindDistill) }

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

// WriteSessionLabel — 현재 세션 라벨 1줄 기록(빈 라벨은 미기록).
// 라벨 파일 경로 규칙은 wire.SessionLabelFile이 소유(v0.39.0 T1 이동분).
// v1.0.2 R2-A2: 세션별 파일(session-<id>.label) — 병렬 세션이 서로의
// 라벨을 덮어쓰지 않는다(구 단일 공유 파일 경합 폐쇄).
func WriteSessionLabel(label string) error {
	if label == "" {
		return nil
	}
	path := wire.SessionLabelFile(label)
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(label), 0o600)
}

// ClearSessionLabel — 이 세션의 라벨 파일 제거(session-end). v1.0.2
// R2-A2: 세션별 제거 — 병렬 세션의 라벨을 지우지 않는다(구 공유 파일
// 제거 경합 폐쇄). 구 session-current 청산은 v1.1.0 T2 제거 — 잔존
// 파일은 무시한다(사용자 디스크 — 미삭제).
func ClearSessionLabel(session string) {
	if session != "" {
		if p := wire.SessionLabelFile(session); p != "" {
			_ = os.Remove(p)
		}
	}
}
