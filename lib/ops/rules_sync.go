// rules_sync.go — v0.9.7 R1 스킬 싱크. flint-plugin/skills/flint/ 전체를
// v0.40.0 T3: lib/cli에서 이동(식별자 대문자화 — 동작 불변).
// $CLAUDE_HOME/skills/flint/로 복사한다. 규칙이 세션에 도달할 마지막 마일
// (스펙 §1). 싱크는 복사만 — 대상 전용 파일(사용자 수동 파일)은 절대
// 삭제하지 않고, 내용이 같은 파일은 건너뛴다(멱등).
package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/synuns/flint/lib/wire"
)

// SyncRules — srcDir의 파일 전체를 destDir로 싱크. 대상에 동일 내용이
// 있으면 skip(문자열 비교), 다르면 덮어쓰기 복사. 대상 전용 파일은
// 무시(삭제 없음). 반환값은 새로 복사한 파일 수.
func SyncRules(srcDir string, destDir string) (copied int, err error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // 1단계 파일만 — 스킬 디렉터리는 평면(SKILL.md+부속)
		}
		// v0.20.6(패치 루프 6차 S5): 심볼릭 링크는 skip — ReadFile이 링크
		// 대상(소스 디렉터리에 심어진 민감 파일 포인터)을 따라가 복사·
		// 노출하는 것을 차단한다.
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		srcPath := filepath.Join(srcDir, e.Name())
		body, err := os.ReadFile(srcPath)
		if err != nil {
			return copied, err
		}
		destPath := filepath.Join(destDir, e.Name())
		if prev, rerr := os.ReadFile(destPath); rerr == nil && string(prev) == string(body) {
			continue // 멱등 skip
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return copied, err
		}
		if err := os.WriteFile(destPath, body, 0o644); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

// SkillSrcDir — 스킬 소스 경로 탐지. ① binPath 인접
// flint-plugin/skills/flint(설치형 배포) ② 워킹디렉터리 상대(저장소
// 실행 — go run ./cmd/flint). 어디에도 없으면 빈 문자열(검사 생략 신호).
func SkillSrcDir(binPath string) string {
	var cands []string
	if binPath != "" {
		cands = append(cands, filepath.Join(filepath.Dir(binPath), "flint-plugin", "skills", "flint"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "flint-plugin", "skills", "flint"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}

// CheckSkillFile — doctor 검사 skill-file(룰링: warn·exit 0). 대상
// SKILL.md 부재·소스와 불일치 시 warn + update-rules 안내. 소스 미탐지
// (빈 문자열)·소스 SKILL.md 부재는 판정 불능 — 검사를 생성하지 않는다
// (harness 패턴과 동일).
func CheckSkillFile(srcDir, destDir string) (DoctorCheck, bool) {
	if srcDir == "" {
		return DoctorCheck{}, false
	}
	src, err := os.ReadFile(filepath.Join(srcDir, "SKILL.md"))
	if err != nil {
		return DoctorCheck{}, false // 소스 SKILL.md 부재 — 판정 불가
	}
	c := DoctorCheck{Name: "skill-file"}
	destPath := filepath.Join(destDir, "SKILL.md")
	dest, rerr := os.ReadFile(destPath)
	if rerr != nil {
		c.Status = "warn" // 스킬 없어도 훅은 동작 — fail 아님(룰링)
		c.Detail = destPath + " 없음"
		c.Fix = "fix: flint setup --update-rules — 스킬 싱크"
		return c, true
	}
	if string(src) != string(dest) {
		c.Status = "warn"
		c.Detail = destPath + "이(가) 소스와 불일치"
		c.Fix = "fix: flint setup --update-rules — 스킬 싱크"
		return c, true
	}
	c.Status = "ok"
	c.Detail = destPath
	return c, true
}

// RunUpdateRules — setup --update-rules 실행. 소스는 --src 오버라이드,
// 기본 SkillSrcDir 탐지. 대상은 $CLAUDE_HOME/skills/flint(FLINT_CLAUDE_HOME
// 승계). 보고 한 줄 JSON {"status":"synced","copied":N,"dest":…}.
func RunUpdateRules(stdout io.Writer, srcOverride string) int {
	src := srcOverride
	if src == "" {
		src = SkillSrcDir(FlintBinPathFn())
	}
	if src == "" {
		fmt.Fprintln(stdout, wire.ErrLine(errors.New("update-rules: 소스 flint-plugin/skills/flint 미탐지 — --src 플래그로 지정")))
		return 1
	}
	home := ClaudeHome()
	if home == "" {
		fmt.Fprintln(stdout, wire.ErrLine(errors.New("update-rules: FLINT_CLAUDE_HOME 또는 HOME을 설정해 싱크 대상 경로를 지정")))
		return 1
	}
	dest := filepath.Join(home, "skills", "flint")
	n, err := SyncRules(src, dest)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("update-rules 싱크 실패: %w", err)))
		return 1
	}
	b, err := json.Marshal(map[string]any{"status": "synced", "copied": n, "dest": dest})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
