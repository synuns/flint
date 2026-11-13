// session.go — v0.39.0 T1(§1): ReadSessionLabel(및 경로 규칙
// SessionLabelFile)을 lib/cli marker.go에서 계약층으로 이동. 라벨
// '저장' 함수(WriteSessionLabel·ClearSessionLabel)는 cli marker.go
// 잔류 — ConfigDir seam(cli init 주입)으로 파일시스템 위치 규칙은
// 커맨드 패키지 소유를 유지한다.
// v1.0.2 R2-A2: 라벨 파일을 세션별(session-<id>.label)로 분리 — 구 단일
// 공유 파일(session-current)은 병렬 세션이 서로의 라벨을 덮어쓰거나
// 제거하던 경합(read_events 라벨 오염)의 원천이었다.
// v1.1.0 T2: 구 session-current 폴백 읽기·청산 제거 — 잔존 파일은
// 무시한다(사용자 디스크 — 미삭제).
package wire

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 세션 라벨 파일명 조각 — session-<id>.label.
const (
	sessionLabelPrefix = "session-"
	sessionLabelSuffix = ".label"
)

// SessionLabelFile — 세션별 라벨 파일 경로(v1.0.2 R2-A2). session-start
// 훅이 하니스 세션 id를 남기고 read가 --session 미지정 시 조용히 채택
// 한다 — G4 라벨 자동화(에이전트 협조 불필요). session-end가 이 세션의
// 파일만 지운다(종료 후 스테일 라벨 방지 — 병렬 세션 라벨 보존). session에
// 경로 구분자가 있거나 파일명으로 쓸 수 없을 만큼 길면 ""(호출측 조용히
// skip — 라벨은 보조 계정). configDir은 ConfigDir seam 주입 경유.
func SessionLabelFile(session string) string {
	d := configDirOrEmpty()
	if d == "" || session == "" || strings.ContainsAny(session, "/") || len(session) > 200 {
		return ""
	}
	return filepath.Join(d, sessionLabelPrefix+session+sessionLabelSuffix)
}

// ReadSessionLabel — 현재 세션 라벨: session-*.label 중 mtime 최신 파일의
// 내용. 부재·빈 내용은 ok=false(구 session-current 폴백은 v1.1.0 T2
// 제거 — 잔존 파일 무시). 동시 병렬 세션의 판정 불가 채택은 기존
// last-writer-wins 룰링 승계(라벨은 보조 계정 — METRICS.md).
// v1.2.3 T9(A4): 채택(읽기 성공) 시 그 라벨 파일의 mtime을 터치 —
// 14일(기본) 초과 활성 세션의 라벨이 타 세션 session-start 정리
// (PruneSessionLabels)에서 사라지는 것을 막는다. mtime 갱신 실패는
// 조용히 무시(읽기전용 FS — 라벨 조회 실패 경로는 불변).
func ReadSessionLabel() (string, bool) {
	d := configDirOrEmpty()
	if d == "" {
		return "", false
	}
	ms, err := filepath.Glob(filepath.Join(d, sessionLabelPrefix+"*"+sessionLabelSuffix))
	if err != nil || len(ms) == 0 {
		return "", false
	}
	newest, newestT := "", time.Time{}
	for _, m := range ms {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		if newest == "" || fi.ModTime().After(newestT) {
			newest, newestT = m, fi.ModTime()
		}
	}
	if newest == "" {
		return "", false
	}
	b, err := os.ReadFile(newest)
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", false
	}
	// 채택 mtime 터치(T9 A4) — 활성 사용 라벨의 정리 생존. 실패 무시.
	now := time.Now()
	_ = os.Chtimes(newest, now, now)
	return s, true
}

// PruneSessionLabels — 만료된 세션 라벨 파일 제거(v1.0.2 R2-A2).
// session-end를 못 만난 세션(크래시·강종)의 잔존 파일이 무한히 쌓이는
// 것을 막는다. maxAge는 호출측(RunSessionStart)이 events_retention_days
// 기본값으로 산출 — 라벨이 가리키는 read_events 행이 정리된 뒤의 라벨은
// 스테일(계측 정리 임계값 재사용 — 신규 튜닝값 아님, meta 오버라이드
// 승계). session-start마다 실행(nudge·compact가 타는 같은 훅 경로 — 별도
// 데몬·스캔 없음).
func PruneSessionLabels(maxAge time.Duration) {
	d := configDirOrEmpty()
	if d == "" || maxAge <= 0 {
		return
	}
	ms, err := filepath.Glob(filepath.Join(d, sessionLabelPrefix+"*"+sessionLabelSuffix))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, m := range ms {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(m)
		}
	}
}
