// doctor.go — T64: flint doctor. brew doctor 준용 — 검사 5종을 돌아
// fail만 exit 1, warn은 0, fail마다 수정 명령 한 줄(fix)을 제시한다.
// 무효 인자(unknown flag·--check 값 누락·무효 검사명)는 exit 3(전역 계약).
// 유지보수 경로(오프라인 CLI)라 핫패스에 간섭하지 않는다(§2).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flint/lib/store"
)

// DoctorChecks — 검사 순서(스펙 §1 5종 + v0.7 port).
func DoctorChecks() []string {
	return []string{"hook-file", "db", "schema", "integrity", "last-seen", "port"}
}

func init() { Register("doctor", runDoctor) }

// doctorCheck — 개별 검사 결과. status = ok|warn|fail.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// doctorOutput — 한 줄 JSON 출력 모양. status는 결과 최악값.
type doctorOutput struct {
	Status string        `json:"status"`
	Checks []doctorCheck `json:"checks"`
}

// Check — doctor 검사 결과 공개 별명(v0.9.4 --fix 리포트 인자).
type Check = doctorCheck

// runDoctor — --list-checks·--check NAME 서브셋·--fix 자가수복(v0.9.4,
// hook-file에만 적용)·기본 전체 검사. 무효 인자(unknown flag·--check 값
// 누락·무효 --check명)는 exit 3(전역 계약: 무효 인자 3). fail 있으면 1,
// warn은 0. 무 --fix 경로의 출력·exit은 완전 불변.
func runDoctor(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	subset := ""
	fix := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list-checks":
			b, _ := json.Marshal(map[string][]string{"checks": DoctorChecks()})
			fmt.Fprintln(stdout, string(b))
			return 0
		case "--fix":
			fix = true
		case "--check":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(errors.New("doctor: --check requires a name")))
				return 3
			}
			subset = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 3
		}
	}
	names := DoctorChecks()
	if subset != "" {
		// v0.10.2 R3: backup-age 단독 실행 — DoctorChecks() 이름 공간 밖
		// 별도 체인의 유일한 서브셋 예외(룰링: 6종·port 마지막 계약 불변,
		// harness-*·skill-file은 미확장). 실행·출력·--fix·exit 계약은
		// 기존 서브셋 경로를 그대로 승계한다.
		if subset != "backup-age" {
			ok := false
			for _, n := range names {
				if n == subset {
					ok = true
					break
				}
			}
			if !ok {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("doctor: unknown check %q", subset)))
				return 3
			}
		}
		names = []string{subset}
	}

	checks := make([]doctorCheck, 0, len(names))
	for _, n := range names {
		checks = append(checks, runDoctorCheck(ctx, st, n))
	}
	// 하니스 검진(v0.9 §4)은 전체 검사 체인 끝에 추가. --check 서브셋은
	// DoctorChecks() 이름 공간만 대상 — harness-*는 이름 없는 별도 체인이므로
	// 서브셋 실행에는 끼지 않는다.
	if subset == "" {
		checks = append(checks, harnessChecks()...)
		// skill-file(v0.9.7 R1)도 harness와 같은 별도 체인 — 소스가
		// 탐지되는 경우에만 결과를 생성하고 warn은 exit를 깨지 않는다
		// (룰링: 스킬 없어도 훅은 동작 — fail은 과잉). home 미탐지 시
		// 검사를 생략한다(checkHookFile와 같은 처리 — Join("",…)은
		// 상대경로가 돼 cwd 오탐).
		if home := claudeHome(); home != "" {
			if c, ok := checkSkillFile(skillSrcDir(flintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
				checks = append(checks, c)
			}
		}
		// backup-age(v0.10.0 R4)도 같은 별도 체인 — 실행 목록 말미.
		// DoctorChecks() 이름 공간(6종·port 마지막)은 기존 계약 그대로
		// 둔다(기존 검사·필드 순서 불변).
		checks = append(checks, checkBackupAge(ctx, st))
		// conflicts(v0.13.0 R3)도 backup-age 뒤 체인 말미 — 모순 후보는
		// warn이면 족하다(fail 아님, 스펙 §3 R3).
		checks = append(checks, checkConflicts(ctx, st))
	}
	// v0.9.4 --fix: hook-file fail에만 자가수복 적용(다른 검사는 사용자
	// 설정·외부 파일이라 자동화 범위 밖 — 코디네이터 ruling). 적용 성공 시
	// 해당 검사만 재평가하고 doctorFixReport 한 줄로 보고한다.
	fixed := false
	if fix {
		for i, c := range checks {
			if c.Name != "hook-file" || c.Status != "fail" {
				continue
			}
			path := filepath.Join(claudeHome(), "settings.json")
			bin := flintBinPathFn()
			applied, _, err := applyHookFix(path, bin)
			if err != nil {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("doctor --fix: 훅 복구 중단(파일 무변경): %w", err)))
				return 1
			}
			if applied {
				checks[i] = checkHookFile() // 재평가
				fixed = true
			}
		}
	}
	overall := "ok"
	for _, c := range checks {
		switch c.Status {
		case "fail":
			overall = "fail"
		case "warn":
			if overall != "fail" {
				overall = "warn"
			}
		}
	}
	if fixed {
		fmt.Fprintln(stdout, doctorFixReport(checks))
		if overall == "fail" {
			return 1
		}
		return 0
	}
	b, err := json.Marshal(doctorOutput{Status: overall, Checks: checks})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	if overall == "fail" {
		return 1
	}
	return 0
}

// runDoctorCheck — 검사명 디스패치.
func runDoctorCheck(ctx context.Context, st Store, name string) doctorCheck {
	switch name {
	case "hook-file":
		return checkHookFile()
	case "db":
		return checkDB(ctx, st)
	case "schema":
		return checkSchema(ctx, st)
	case "integrity":
		return checkIntegrity(ctx, st)
	case "last-seen":
		return checkLastSeen(ctx, st)
	case "port":
		return checkPort(ctx)
	case "backup-age":
		// v0.10.2 R3 서브셋 특수경로 전용 — 전체 실행은 이름 공간 밖
		// 별도 체인(subset=="" 블록)에서 직접 append하므로 여기는
		// --check backup-age 단독 실행에서만 도달한다.
		return checkBackupAge(ctx, st)
	}
	return doctorCheck{Name: name, Status: "fail", Detail: "unknown check"}
}

// claudeHome — 훅 설정 디렉터리. FLINT_CLAUDE_HOME 오버라이드 우선.
func claudeHome() string {
	if h := os.Getenv("FLINT_CLAUDE_HOME"); h != "" {
		return h
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, ".claude")
}

// checkHookFile — 훅 설정(settings.json)에 flint 등록 여부.
// 미등록은 적립 계측(write --source hook)이 누락되는 fail.
func checkHookFile() doctorCheck {
	c := doctorCheck{Name: "hook-file"}
	home := claudeHome()
	path := filepath.Join(home, "settings.json")
	if home == "" {
		c.Status, c.Fix = "fail", "fix: FLINT_CLAUDE_HOME 또는 HOME을 설정해 훅 설정 경로를 지정"
		return c
	}
	b, err := os.ReadFile(path)
	if err != nil {
		c.Status = "fail"
		c.Detail = path + " 없음"
		c.Fix = "fix: settings.json 생성 후 flint session-start(SessionStart)·session-end(Stop) 훅 등록 — flint-plugin/hooks/hooks.json 참조"
		return c
	}
	if !containsFlintHook(string(b)) {
		c.Status = "fail"
		c.Detail = path + "에 flint 훅 없음"
		c.Fix = "fix: settings.json hooks에 flint session-start(SessionStart)·session-end(Stop) 훅 추가 — flint-plugin/hooks/hooks.json 참조"
		return c
	}
	c.Status = "ok"
	c.Detail = path
	return c
}

// flintWordRe — command 문자열에서 flint 실행 파일을 단어 경계로 매칭
// ('myflint'·'flintx' 같은 접두·접미 우연 일치 배제).
var flintWordRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])flint(?:[^A-Za-z0-9_-]|$)`)

// containsFlintHook — settings.json의 hooks 객체 안 command 필드에 flint
// 실행이 등록됐는지. 본문 전문 포함 검사('flint' 5글자)는 주석·다른 도구
// 설정의 우연 일치가 과다해 JSON 구조(hooks → SessionStart·PostToolUse 등
// 배열 → hooks[].command)로 정밀 매칭한다. 파싱 불가 본문은 미등록 취급.
func containsFlintHook(body string) bool {
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &doc) != nil {
		return false
	}
	raw, ok := doc["hooks"]
	if !ok {
		return false
	}
	var hooks any
	if json.Unmarshal(raw, &hooks) != nil {
		return false
	}
	return valueHasFlintCommand(hooks)
}

// valueHasFlintCommand — hooks 값 트리 안 모든 객체의 command 필드 검사.
// hooks.json 실체는 이벤트 배열 → {matcher, hooks:[{type,command}]} 2중
// 중첩이지만, 단순 평 배열({command:...})도 통과시킨다(등록 형태 다양성).
func valueHasFlintCommand(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if cmd, ok := t["command"].(string); ok && flintWordRe.MatchString(cmd) {
			return true
		}
		for _, child := range t {
			if valueHasFlintCommand(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if valueHasFlintCommand(child) {
				return true
			}
		}
	}
	return false
}

// flintBinPathFn — 훅 명령에 쓸 flint 바이너리 절대경로 seam(var).
// PATH에 flint가 없으면 bare 명령이 조용히 죽는다(v0.9.4 배경) —
// os.Executable로 직접 해석한다. 해석 실패 시 빈 문자열.
var flintBinPathFn = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// flintHookCmd — hooks.json 명령의 bare `flint`를 binPath 절대경로로
// 치환한 훅 명령 1개. 2>/dev/null || true — 훅 실패가 세션을 흔들지
// 않게 한다(원본 flint-plugin/hooks/hooks.json 승계).
func flintHookCmd(binPath, sub string) map[string]any {
	cmd := binPath + " " + sub + " 2>/dev/null || true"
	return map[string]any{"type": "command", "command": cmd, "timeout": 2}
}

// flintHookDefs — flint 훅 3개(v0.11.0 R4 통합: 이벤트 → 추가 엔트리
// 목록). 세션 시작·종료 시퀀스는 session-start·session-end 1커맨드가
// 내부에서 실행하고, matcher·jq session_id 파싱·타임아웃·리다이렉션
// 패턴은 flint-plugin/hooks/hooks.json 그대로 승계한다.
func flintHookDefs(binPath string) map[string][]map[string]any {
	session := "startup|resume|clear|compact"
	withSession := func(sub string) map[string]any {
		return map[string]any{
			"matcher": session,
			"hooks":   []any{flintHookCmd(binPath, sub)},
		}
	}
	plain := func(sub string) map[string]any {
		return map[string]any{"hooks": []any{flintHookCmd(binPath, sub)}}
	}
	return map[string][]map[string]any{
		"SessionStart": {
			withSession(`session-start --session "$(jq -r '.session_id // empty' 2>/dev/null)"`),
		},
		"Stop": {
			plain(`session-end --session "$(jq -r '.session_id // empty' 2>/dev/null)"`),
		},
		"PostCompact": {plain("compact")},
	}
}

// flintHookSubRe — flint 훅 커맨드 1개 식별(실행 파일 경로 무관). flint
// 실행 뒤 훅 서브커맨드(hot·done·nudge·compact·session-start·
// session-end)가 오는 명령 — 구형 5엔트리 직접 실행과 신형 통합 커맨드
// 모두 잡는다. 'myflint'·'flintx' 같은 접두·접미 우연 일치는 경계 클래스
// 로 배제(flintWordRe와 같은 규칙 + 경로 접두 허용).
var flintHookSubRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_-])(?:\S*/)?flint(?:\.\S+)?\s+"?(session-start|session-end|hot|done|nudge|compact)(?:[\s"]|$)`)

// anyHookCommand — hooks 값 트리 안 command 필드 중 pred를 만족하는
// 것이 하나라도 있는지(valueHasFlintCommand와 같은 순회).
func anyHookCommand(v any, pred func(string) bool) bool {
	switch t := v.(type) {
	case map[string]any:
		if cmd, ok := t["command"].(string); ok && pred(cmd) {
			return true
		}
		for _, child := range t {
			if anyHookCommand(child, pred) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if anyHookCommand(child, pred) {
				return true
			}
		}
	}
	return false
}

// hasFlintSub — 트리 안에 flint <sub> 커맨드 존재 여부.
func hasFlintSub(v any, sub string) bool {
	return anyHookCommand(v, func(cmd string) bool {
		m := flintHookSubRe.FindStringSubmatch(cmd)
		return m != nil && m[1] == sub
	})
}

// stripFlintHookEntry — 훅 그룹 1개에서 flint 훅 커맨드를 걷어낸다
// (교체 대상 식별 — 플러그인이 등록한 엔트리만, 타 도구 커맨드는 불변).
// 남는 커맨드가 없으면 그룹 자체를 삭제한다(false).
func stripFlintHookEntry(g map[string]any) bool {
	hs, ok := g["hooks"].([]any)
	if !ok {
		return true
	}
	kept := make([]any, 0, len(hs))
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if ok {
			if cmd, _ := hm["command"].(string); cmd != "" && flintHookSubRe.MatchString(cmd) {
				continue
			}
		}
		kept = append(kept, h)
	}
	if len(kept) == 0 {
		return false
	}
	g["hooks"] = kept
	return true
}

// applyHookFix — settings.json에 flint 훅 3개 등록·교체(자가수복 R1,
// v0.11.0 R4 통합 3엔트리). 읽기→파싱→신형 존재 검사(멱등)→타임스탬프
// 백업→구형 flint 엔트리 걷어내고 신형 append→MarshalIndent 쓰기.
// 기존 구형 5엔트리(hot·nudge/done·nudge 직접 실행)는 교체 대상 —
// flint 커맨드가 섞인 그룹만 정리하고 타 도구 훅·다른 키는 전부 불변.
// 신형(session-start·session-end)이 이미 있으면 (false, "", nil) skip.
// 파싱 실패는 파일·백업 모두 무변경 채로 err. 파일 부재 시(v0.9.5 R3)
// 최소 뼈대 {"hooks":{}}를 새로 생성한다 — 원본이 없으니 백업은
// 생략(backup=""), 부모 디렉터리도 함께 생성. 이후 merge 경로(멱등·
// 절대경로·timeout 계약)를 그대로 재사용한다.
func applyHookFix(path string, binPath string) (applied bool, backupPath string, err error) {
	b, rerr := os.ReadFile(path)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return false, "", rerr
	}
	existed := rerr == nil
	if !existed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, "", err
		}
		b = []byte(`{"hooks":{}}`)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return false, "", fmt.Errorf("settings.json 파싱 실패: %w", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	if hasFlintSub(hooks, "session-start") && hasFlintSub(hooks, "session-end") {
		return false, "", nil // 멱등 — 신형 3엔트리 이미 등록됨
	}
	if existed { // 백업은 원본이 있을 때만
		backupPath = path + ".flint-bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backupPath, b, 0o644); err != nil {
			return false, "", err
		}
	}
	// 구형 flint 직접 실행 훅(hot·done·nudge·compact) 교체 — flint
	// 커맨드만 걷어내고 빈 그룹은 삭제. 타 도구 그룹은 그대로 앞쪽에 남는다.
	for ev, prev := range hooks {
		list, ok := prev.([]any)
		if !ok {
			continue
		}
		merged := make([]any, 0, len(list))
		for _, g := range list {
			if gm, ok := g.(map[string]any); ok && !stripFlintHookEntry(gm) {
				continue // flint 커맨드만 있던 그룹 — 통합 엔트리로 대체
			}
			merged = append(merged, g)
		}
		hooks[ev] = merged
	}
	for ev, add := range flintHookDefs(binPath) {
		entries := add // []map[string]any → []any 변환해 append
		merged := make([]any, 0, len(add)+2)
		if prev, ok := hooks[ev].([]any); ok {
			merged = append(merged, prev...)
		}
		for _, e := range entries {
			merged = append(merged, e)
		}
		hooks[ev] = merged
	}
	doc["hooks"] = hooks
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, "", err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, "", err
	}
	return true, backupPath, nil
}

// doctorFixReport — --fix 적용 후 결과 보고 한 줄 JSON. status "fixed" +
// 검사별 name:status. 다른 fail 검사도 이름:상태로 그대로 노출된다.
func doctorFixReport(checks []Check) string {
	m := map[string]any{"status": "fixed"}
	for _, c := range checks {
		m[c.Name] = c.Status
	}
	b, err := json.Marshal(m)
	if err != nil {
		return `{"status":"fixed"}`
	}
	return string(b)
}

// harnessSpec — v0.9 하니스 검진 대상 1종(스펙 C1~C5). Name은 체크명
// 접미·문서 파일명과 일치. Rel은 홈 기준 상대 경로(cursor만 프로젝트
// 루트=워킹디렉터리 기준). EnvOverride는 해당 디렉터리 자체를 교체
// (claudeHome·FLINT_CLAUDE_HOME 패턴 승계 — 테스트 주입용).
type harnessSpec struct {
	Name        string
	Rel         string
	EnvOverride string
	Project     bool // true면 Rel의 기준이 홈이 아닌 워킹디렉터리(cursor)
}

// harnessRegistry — 하니스 5종. C6: 전 하니스 MCP stdio JSON-RPC.
var harnessRegistry = []harnessSpec{
	{Name: "codex", Rel: ".codex/config.toml", EnvOverride: "FLINT_CODEX_HOME"},
	{Name: "gemini", Rel: ".gemini/settings.json", EnvOverride: "FLINT_GEMINI_HOME"},
	{Name: "opencode", Rel: ".config/opencode/opencode.json", EnvOverride: "FLINT_OPENCODE_HOME"},
	{Name: "copilot", Rel: ".copilot/mcp-config.json", EnvOverride: "FLINT_COPILOT_HOME"},
	{Name: "cursor", Rel: ".cursor/mcp.json", EnvOverride: "FLINT_CURSOR_DIR", Project: true},
}

// path — 검사 대상 설정 파일 절대 경로. 오버라이드 env는 Rel이 붙던
// 기존 디렉터리(cursor는 프로젝트 루트) 자체를 교체한다. 홈·wd 조회
// 불능은 빈 문자열(부재 취급 — 미설치 하니스는 경고가 아님).
func (h harnessSpec) path() string {
	if h.Project {
		root := ""
		if v := os.Getenv(h.EnvOverride); v != "" {
			root = v
		} else if wd, err := os.Getwd(); err == nil {
			root = wd
		}
		if root == "" {
			return ""
		}
		return filepath.Join(root, h.Rel)
	}
	if v := os.Getenv(h.EnvOverride); v != "" {
		return filepath.Join(v, filepath.Base(h.Rel))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, h.Rel)
}

// harnessChecks — 하니스 5종 검진(§4). 설정 파일이 있는 하니스만
// 결과를 생성한다(부재 → 빈 슬라이스). 읽기 전용(비간섭).
func harnessChecks() []doctorCheck {
	var out []doctorCheck
	for _, h := range harnessRegistry {
		if c, ok := checkHarnessFile(h); ok {
			out = append(out, c)
		}
	}
	return out
}

// checkHarnessFile — 하니스 1종 판정. (a) 파일 부재 → 검사 생략(ok=false),
// (b) 본문에 "flint" 없음 → fail + 문서 경로 fix, (c) 포함 → ok.
// 등록 판정은 문자열 포함(포맷 무관 — C6. 주석만 있어도 ok).
func checkHarnessFile(h harnessSpec) (doctorCheck, bool) {
	path := h.path()
	if path == "" {
		return doctorCheck{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{}, false // 미설치 하니스 — 검사 생략
	}
	c := doctorCheck{Name: "harness-" + h.Name}
	if strings.Contains(string(b), "flint") {
		c.Status, c.Detail = "ok", path
		return c, true
	}
	c.Status = "fail"
	c.Detail = path + "에 flint 미등록"
	c.Fix = "fix: docs/adapters/" + h.Name + ".md 참조해 flint MCP 서버 등록"
	return c, true
}

// checkDB — Ping.
func checkDB(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "db"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint setup — docker compose up -d"
		return c
	}
	if err := st.Ping(ctx); err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint setup — docker compose up -d"
		return c
	}
	c.Status = "ok"
	return c
}

// checkSchema — meta schema_version >= 4(수치 비교 — v0.6 계측 컬럼 승격
// 확인. 향후 v5 DB를 정상으로 수용, 미달·비수 값만 실패).
func checkSchema(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "schema"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — 스키마 승격 확인"
		return c
	}
	rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — DB 접속·스키마 확인"
		return c
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if n, convErr := strconv.Atoi(version); convErr != nil || n < 4 {
		c.Status = "fail"
		c.Detail = "schema_version=" + version
		c.Fix = "fix: v0.6 CLI로 재접속해 스키마 승격 — flint status"
		return c
	}
	c.Status = "ok"
	return c
}

// checkIntegrity — flint_knowledge_* 인덱스 3행(v3 필터 인덱스).
func checkIntegrity(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "integrity"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — 인덱스 승격 확인"
		return c
	}
	rows, err := queryOnce(ctx, st,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'knowledge' AND indexname LIKE 'flint_knowledge_%'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — DB 접속·인덱스 확인"
		return c
	}
	n := -1
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &n)
	}
	if n < 3 {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("flint_knowledge_* 인덱스 %d개(기대 3)", n)
		c.Fix = "fix: flint status — v3 인덱스 승격 재시도"
		return c
	}
	c.Status = "ok"
	return c
}

// checkLastSeen — 3경로 last_* 경과 + grace(nodata 패턴). grace는
// maintain --check와 같은 loadThresholds 체계(stall_hot_h·stall_write_h
// 메타 오버라이드, 기본 48/72h)를 쓴다 — 임계값 소스가 둘이면 결론이
// 상충한다. 초과 또는 기록 부재(never)는 warn — exit를 깨지 않는다.
func checkLastSeen(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — DB 접속"
		return c
	}
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn" // 접속 불량은 stall 오탐 방지를 위해 warn(조용히)
		c.Detail = "last_* 조회 실패: " + err.Error()
		return c
	}
	seen := map[string]time.Time{}
	for _, r := range rows {
		if len(r) != 2 || r[0] == nil || r[1] == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(r[1]))
		if err != nil {
			continue // 파손 값은 없는 취급(무효 값 폴백)
		}
		seen[fmt.Sprint(r[0])] = ts
	}
	// 임계값은 maintain와 공유 체계 — 조회 실패 시에도 기본 grace로 폴백.
	th, thErr := loadThresholds(ctx, st)
	if thErr != nil {
		th = store.DefaultThresholds()
	}
	warns := 0
	if detail := stallDetail(seen, "last_hot_at", th.StallHotH, &warns); detail != "" {
		c.Detail = detail
	}
	if detail := stallDetail(seen, "last_write_at", th.StallWriteH, &warns); detail != "" {
		if c.Detail != "" {
			c.Detail += "; "
		}
		c.Detail += detail
	}
	if warns > 0 {
		c.Status = "warn"
		return c
	}
	c.Status = "ok"
	return c
}

// backupAgeWarn — 자동 백업 신선도 임계값(v0.10.0 R4). 72h 상수 —
// TUNING 이연(스펙 §3 R4 Notes).
const backupAgeWarn = 72 * time.Hour

// checkBackupAge — meta last_backup 경과 판정(v0.10.0 R4). 72h 이상
// 경과·미설정·파손 값·조회 실패·st nil 전부 warn — 백업 부재가 flint
// 동작을 막지 않으므로 fail 아니다(룰링). checkLastSeen과 같은
// queryOnce·RFC3339 패턴을 승계한다.
func checkBackupAge(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "backup-age"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "last_backup 미설정 — flint done 훅·수동 backup 확인"
		return c
	}
	rows, err := queryOnce(ctx, st, `SELECT key, value FROM meta WHERE key = 'last_backup'`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "last_backup 조회 실패: " + err.Error() + " — flint done 훅·수동 backup 확인"
		return c
	}
	last := ""
	if len(rows) > 0 && len(rows[0]) > 1 && rows[0][1] != nil {
		last = fmt.Sprint(rows[0][1])
	}
	ts, perr := time.Parse(time.RFC3339, last)
	if perr != nil {
		ts = time.Time{} // 파손 값은 미설정 취급(무효 값 폴백)
	}
	if ts.IsZero() {
		c.Status = "warn"
		c.Detail = "last_backup 미설정 — flint done 훅·수동 backup 확인"
		return c
	}
	if d := time.Since(ts); d >= backupAgeWarn {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("자동 백업 %d시간 경과 — flint done 훅·수동 backup 확인", int(d.Hours()))
		return c
	}
	c.Status = "ok"
	return c
}

// checkConflicts — 모순 후보 그룹 수 판정(v0.13.0 R3). active 동일
// topic 2건+ 그룹(maintain --conflict-suspects와 같은 GROUP BY 조건)을
// 세어 1+면 warn, 0이면 ok. 조회 실패·st nil도 warn — fail 아니다(모순
// 의심은 동작을 막지 않는다). 쿼리는 T2 그룹 조회 조건의 중복 구현
// (플랜 승인 — lib/db/admin.go는 T2 소유).
func checkConflicts(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "conflicts"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "모순 후보 조회 불가 — maintain --conflict-suspects 확인"
		return c
	}
	rows, err := queryOnce(ctx, st,
		`SELECT topic FROM knowledge WHERE invalid_at IS NULL AND status = 'active' GROUP BY topic HAVING count(*) > 1`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "모순 후보 조회 실패: " + err.Error() + " — maintain --conflict-suspects 확인"
		return c
	}
	if n := len(rows); n > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("모순 후보 %d그룹 — maintain --conflict-suspects", n)
		return c
	}
	c.Status = "ok"
	return c
}

// stallDetail — 키의 경과가 grace를 넘으면 경고 문구 반환(부재=never).
func stallDetail(seen map[string]time.Time, key string, graceH int, warns *int) string {
	ts, ok := seen[key]
	if !ok {
		*warns++
		return key + " 기록 없음(never)"
	}
	if d := time.Since(ts); d > time.Duration(graceH)*time.Hour {
		*warns++
		return fmt.Sprintf("%s 경과 %s(grace %dh 초과)", key, d.Round(time.Hour), graceH)
	}
	return ""
}

// doctorPort — flint 기대 호스트 게시 포트(compose 오버라이드 전 기본값).
// v0.9.7 ADR-0010: 외부 컨테이너의 5433 점유로 5434로 이전.
const doctorPort = 5434

// composePsFn — docker compose ps seam(var). docker 부재·compose 프로젝트
// 부재 등 모든 오류는 checkPort가 skip으로 처리한다(비간섭).
var composePsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "compose", "ps", "--format", "json").Output()
}

// composePsPublisher — ps 각 포트 매핑. docker 공식 스키마(B6):
// PublishedPort = 호스트 게시 포트. Exited 컨테이너는 Publishers null.
type composePsPublisher struct {
	PublishedPort int `json:"PublishedPort"`
}

// composePsEntry — ps 컨테이너 1행.
type composePsEntry struct {
	Name       string               `json:"Name"`
	State      string               `json:"State"`
	Publishers []composePsPublisher `json:"Publishers"`
}

// portCheck — port 검사 단독 실행. 결과 한 줄 JSON을 out에 쓰고,
// fail이면 1, ok·skip면 0을 반환한다(skip는 exit 영향 없음).
func portCheck(ctx context.Context, out io.Writer) int {
	c := checkPort(ctx)
	b, err := json.Marshal(c)
	if err != nil {
		fmt.Fprintln(out, errLine(err))
		return 1
	}
	fmt.Fprintln(out, string(b))
	if c.Status == "fail" {
		return 1
	}
	return 0
}

// checkPort — 5434 게시 상태 판정. ① flint 실행+5434 → ok
// ② flint 부재/정지 → fail + up -d 안내 ③ 타 컨테이너 점유 → fail +
// 점유자 이름 + 포트 이동 안내. composePsFn 오류는 skip(docker 미설치
// 환경에서 doctor 나머지 검사를 덮지 않는다).
func checkPort(ctx context.Context) doctorCheck {
	c := doctorCheck{Name: "port"}
	// docker 무응답 시 무기한 블록 방지 — 2초 상한. 만료는 composePsFn
	// 오류로 떨어져 기존 skip 경로를 따른다(비간섭).
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := composePsFn(tctx)
	if err != nil {
		c.Status = "skip"
		c.Detail = "docker compose ps 실패: " + err.Error()
		return c
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		c.Status = "skip"
		c.Detail = "compose ps 출력 파싱 실패: " + perr.Error()
		return c
	}
	// ① flint 컨테이너 실행 중 + 5434 게시.
	for _, e := range entries {
		if isFlintContainer(e.Name) && e.State == "running" && publishesPort(e, doctorPort) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s가 %d 게시", e.Name, doctorPort)
			return c
		}
	}
	// ③ 타 컨테이너가 5434 점유 — 점유자를 지목한다.
	for _, e := range entries {
		if publishesPort(e, doctorPort) {
			c.Status = "fail"
			c.Detail = fmt.Sprintf("포트 %d을(를) %s이(가) 점유", doctorPort, e.Name)
			c.Fix = "fix: 포트 이동 — FLINT_DSN + compose 오버라이드(TUNING §2)"
			return c
		}
	}
	// ② flint 없음/정지.
	c.Status = "fail"
	c.Detail = fmt.Sprintf("flint 컨테이너가 %d을(를) 게시하지 않음", doctorPort)
	c.Fix = "fix: docker compose up -d (flint 저장소)"
	return c
}

// parseComposePs — ps --format json 출력 파싱. 최신 docker는 연속 객체
// (JSONL), 구버전은 배열을 내보내므로 json.Decoder 스트리밍으로 양쪽을
// 처리한다.
func parseComposePs(b []byte) ([]composePsEntry, error) {
	trimmed := bytes.TrimSpace(b)
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var out []composePsEntry
	if len(trimmed) > 0 && trimmed[0] == '[' {
		tok, err := dec.Token() // '[' 소비
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return nil, errors.New("배열 시작 아님")
		}
		for dec.More() {
			var e composePsEntry
			if err := dec.Decode(&e); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	for {
		var e composePsEntry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
}

// isFlintContainer — 컨테이너 이름이 flint 서비스임(flint-doltgres-1 등).
// 접두 매칭 — 'conflint'처럼 flint를 포함하나 접두가 아닌 이름의 오탐 배제.
func isFlintContainer(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "flint")
}

// publishesPort — 엔트리의 Publishers 중 호스트 게시 포트 일치 여부.
// Exited 컨테이너의 Publishers null은 빈 슬라이스로 디코딩된다.
func publishesPort(e composePsEntry, port int) bool {
	for _, p := range e.Publishers {
		if p.PublishedPort == port {
			return true
		}
	}
	return false
}
