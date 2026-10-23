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

// runDoctor — --list-checks·--check NAME 서브셋·기본 전체 검사.
// 무효 인자(unknown flag·--check 값 누락·무효 --check명)는 exit 3(전역
// 계약: 무효 인자 3). fail 있으면 1, warn은 0.
func runDoctor(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	subset := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list-checks":
			b, _ := json.Marshal(map[string][]string{"checks": DoctorChecks()})
			fmt.Fprintln(stdout, string(b))
			return 0
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
		names = []string{subset}
	}

	checks := make([]doctorCheck, 0, len(names))
	for _, n := range names {
		checks = append(checks, runDoctorCheck(ctx, st, n))
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
		c.Fix = "fix: settings.json 생성 후 flint hot(SessionStart)·done(Stop) 훅 등록 — flint-plugin/hooks/hooks.json 참조"
		return c
	}
	if !containsFlintHook(string(b)) {
		c.Status = "fail"
		c.Detail = path + "에 flint 훅 없음"
		c.Fix = "fix: settings.json hooks에 flint hot(SessionStart)·done(Stop) 훅 추가 — flint-plugin/hooks/hooks.json 참조"
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
const doctorPort = 5433

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

// checkPort — 5433 게시 상태 판정. ① flint 실행+5433 → ok
// ② flint 부재/정지 → fail + up -d 안내 ③ 타 컨테이너 점유 → fail +
// 점유자 이름 + 포트 이동 안내. composePsFn 오류는 skip(docker 미설치
// 환경에서 doctor 나머지 검사를 덮지 않는다).
func checkPort(ctx context.Context) doctorCheck {
	c := doctorCheck{Name: "port"}
	b, err := composePsFn(ctx)
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
	// ① flint 컨테이너 실행 중 + 5433 게시.
	for _, e := range entries {
		if isFlintContainer(e.Name) && e.State == "running" && publishesPort(e, doctorPort) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s가 %d 게시", e.Name, doctorPort)
			return c
		}
	}
	// ③ 타 컨테이너가 5433 점유 — 점유자를 지목한다.
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
func isFlintContainer(name string) bool {
	return strings.Contains(strings.ToLower(name), "flint")
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
