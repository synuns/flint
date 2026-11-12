// session.go — v0.39.0 T1(§1): ReadSessionLabel(및 경로 규칙
// SessionLabelFile)을 lib/cli marker.go에서 계약층으로 이동. 라벨
// '저장' 함수(WriteSessionLabel·ClearSessionLabel)는 cli marker.go
// 잔류 — ConfigDir seam(cli init 주입)으로 파일시스템 위치 규칙은
// 커맨드 패키지 소유를 유지한다.
package wire

import (
	"os"
	"path/filepath"
	"strings"
)

// SessionLabelFile — 현재 세션 라벨 파일 경로(v0.20.5 패치 루프 5차·
// 마일스톤 B②). session-start 훅이 하니스 세션 id를 남기고 read가
// --session 미지정 시 조용히 채택한다 — G4 라벨 자동화(에이전트 협조
// 불필요). session-end가 지운다(종료 후 스테일 라벨 방지). 병렬 세션은
// last-writer-wins(라벨은 보조 계정 — 룰링). cli marker.go 이동분 —
// configDir은 ConfigDir seam(cliwire_alias init 주입) 경유.
func SessionLabelFile() string {
	d := configDirOrEmpty()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "session-current")
}

// ReadSessionLabel — 현재 세션 라벨(부재·빈 파일은 ok=false).
// cli marker.go 이동분 — 계약 동일.
func ReadSessionLabel() (string, bool) {
	b, err := os.ReadFile(SessionLabelFile())
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	return s, s != ""
}
