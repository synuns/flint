// doctor.go — flint doctor. brew doctor 준용 — 검사 5종을 돌아
// fail만 exit 1, warn은 0, fail마다 수정 명령 한 줄(fix)을 제시한다.
// 무효 인자(unknown flag·--check 값 누락·무효 검사명)는 exit 3(전역 계약).
// 유지보수 경로(오프라인 CLI)라 핫패스에 간섭하지 않는다(§2).
package ops

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

	"sort"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// cli 소유 구현 주입 seam — ops→cli 임포트 금지 회피(knowledge
// ParseAsOfSpecFn 관례). 버전·업데이트(version.go·update.go — cli 잔류)도
// 여기 걸린다.
// 실구현은 cli/root.go composition root init이 주입한다.
var (
	// CurrentVersionFn 등 — checkUpdate(버전 지연 경고).
	CurrentVersionFn     func() string
	FetchLatestVersionFn func() (string, error)
	ValidVersionFn       func(v string) bool
	CompareVersionsFn    func(a, b string) int
	// KnownMetaKeyFn — checkMetaHygiene의 미지 키 판정(maintain.go).
	KnownMetaKeyFn func(k string) bool
	// AnyToTimeFn — checkPendingAge의 valid_at 파싱(maintain.go).
	AnyToTimeFn func(v any) (time.Time, bool)
	// IsConflictGroupFn·ConflictDupThresholdFn — checkConflicts의 모순
	// 분류(maintain.go). ThresholdFn은 var라 호출 시점 읽기 래퍼로 주입.
	IsConflictGroupFn      func(g db.ConflictGroup, dupThreshold float64) bool
	ConflictDupThresholdFn func() float64
	// BackupEncryptOnFn·BackupKeyPathFn — checkBackupKey(backup.go).
	BackupEncryptOnFn func(v string) bool
	BackupKeyPathFn   func() string
	// SkillSrcDirFn·CheckSkillFileFn — skill-file 검사(rules_sync.go).
	SkillSrcDirFn    func(binPath string) string
	CheckSkillFileFn func(srcDir, destDir string) (DoctorCheck, bool)
)

// CheckSpec — doctor 검사 1종의 등록 표현.
// Name·Fn·Chain("core"|"tail")·OnStoreNil("fail"|"warn"|""=st 미사용)이
// 단일 등록표에 모여 있고, 전체 실행 체인·runDoctorCheck 디스패치·
// --check 허용 목록·--list-checks가 전부 여기서 파생한다.
type CheckSpec struct {
	Name string
	Fn   func(ctx context.Context, st wire.Store) DoctorCheck
	// ThFn — thresholds 소비 검사의 주입판: 실행 스코프 1회
	// 로드 값(doctorThresholds)을 받는다. 디스패치(runCheckSpec)는
	// ThFn이 있으면 우선, 없으면 Fn. thresholds를 쓰지 않는 검사는
	// Fn만 세운다.
	ThFn func(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck
	// Chain — "core"는 DoctorChecks 이름 공간(6종·port 마지막 계약),
	// "tail"은 전체 실행 체인 말미의 tail 체인(10종).
	Chain string
	// OnStoreNil — st==nil일 때의 판정("fail"|"warn"). 검사가 st를 쓰지
	// 않으면 ""(hook-file·port·update). 코어 4종(db·schema·integrity·
	// last-seen) fail·tail warn의 기존 행동을 속성으로 명문화한다.
	OnStoreNil string
}

// doctorChecks — 체크 등록표(코어6+tail10 = 16종). harness-* 14종·
// skill-file은 파일 존재 감지(동적 하네스/스킬 감지)라 등록표 밖 별도
// 체인 — 통합 대상 아니다(룰링). 새 검사 추가는 이 표에 1행만.
// thresholds 소비 검사 7종(last-seen·backup-age·zero-near·
// pending-age·event-volume·meta-hygiene·secrets)은 ThFn 주입판으로
// 등록 — 실행 스코프 1회 로드(doctorThresholds)를 공유한다(secrets 포함).
func doctorChecks() []CheckSpec {
	return []CheckSpec{
		{Name: "hook-file", Chain: "core", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return checkHookFile() }},
		{Name: "db", Chain: "core", OnStoreNil: "fail", Fn: checkDB},
		{Name: "schema", Chain: "core", OnStoreNil: "fail", Fn: checkSchema},
		{Name: "integrity", Chain: "core", OnStoreNil: "fail", Fn: checkIntegrity},
		{Name: "last-seen", Chain: "core", OnStoreNil: "fail", ThFn: checkLastSeenTh},
		{Name: "port", Chain: "core", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return checkPort(ctx) }},
		{Name: "backup-age", Chain: "tail", OnStoreNil: "warn", ThFn: checkBackupAgeTh},
		{Name: "conflicts", Chain: "tail", OnStoreNil: "warn", Fn: CheckConflicts},
		{Name: "zero-near", Chain: "tail", OnStoreNil: "warn", ThFn: checkZeroNearTh},
		{Name: "pending-age", Chain: "tail", OnStoreNil: "warn", ThFn: checkPendingAgeTh},
		{Name: "event-volume", Chain: "tail", OnStoreNil: "warn", ThFn: checkEventVolumeTh},
		{Name: "backup-key", Chain: "tail", OnStoreNil: "warn", Fn: checkBackupKey},
		{Name: "meta-hygiene", Chain: "tail", OnStoreNil: "warn", ThFn: checkMetaHygieneTh},
		{Name: "update", Chain: "tail", OnStoreNil: "", Fn: checkUpdate},
		{Name: "recall-starved", Chain: "tail", OnStoreNil: "warn", Fn: checkRecallStarved},
		{Name: "secrets", Chain: "tail", OnStoreNil: "warn", ThFn: checkSecretsTh},
	}
}

// doctorThresholds — doctor 실행 스코프 thresholds 로더(지연
// 1회·메모). 검사 6종이 각자 meta IN 쿼리를 날리던 것(전체 실행에서
// 최대 6회)을 실행당 1회 공유로 — 로드 실패 기본값 폴백은 WriteThresholds
// 관례 불변. st==nil은 로드 없이 기본값(검사별 nil 가드가 판정을 소유 —
// 로드 시점 nil 인터페이스 호출 방지).
func doctorThresholds(ctx context.Context, st wire.Store) func() store.Thresholds {
	var (
		th     store.Thresholds
		loaded bool
	)
	return func() store.Thresholds {
		if !loaded {
			if st != nil {
				th = knowledge.WriteThresholds(ctx, st)
			} else {
				th = store.DefaultThresholds()
			}
			loaded = true
		}
		return th
	}
}

// runCheckSpec — 등록표 검사 디스패치: ThFn 검사는 실행 스코프
// thresholds(th)를 주입받고, 그 외는 Fn을 그대로 실행한다.
func runCheckSpec(ctx context.Context, st wire.Store, th func() store.Thresholds, s CheckSpec) DoctorCheck {
	if s.ThFn != nil {
		return s.ThFn(ctx, st, th())
	}
	return s.Fn(ctx, st)
}

// doctorSpec — 등록표에서 이름으로 검색.
func doctorSpec(name string) (CheckSpec, bool) {
	for _, s := range doctorChecks() {
		if s.Name == name {
			return s, true
		}
	}
	return CheckSpec{}, false
}

// doctorChainNames — 등록표에서 특정 체인("core"|"tail")의 이름 순서.
func doctorChainNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

// DoctorChecks — 코어 체인 이름 순서(스펙 §1 5종 + port). 등록표
// (doctorChecks) chain=="core" 필터 파생 — 공개 표면 유지.
func DoctorChecks() []string {
	return doctorChainNames("core")
}

// doctorTailChecks — tail 체인 이름 순서(DoctorChecks() 이름 공간 밖,
// 전체 실행 체인 말미). 등록표 chain=="tail" 필터 파생 — --check 단독
// 실행 허용 목록도 이 목록과 정합. harness-*·skill-file은 미확장(별도 체인).
func doctorTailChecks() []string {
	return doctorChainNames("tail")
}

// DoctorCheck — 개별 검사 결과. status = ok|warn|fail.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// doctorOutput — 한 줄 JSON 출력 모양. status는 결과 최악값.
type doctorOutput struct {
	Status string        `json:"status"`
	Checks []DoctorCheck `json:"checks"`
}

// RunDoctor — --list-checks·--check NAME 서브셋·--fix 자가수복
// (hook-file에만 적용)·기본 전체 검사. 무효 인자(unknown flag·--check 값
// 누락·무효 --check명)는 exit 3(전역 계약: 무효 인자 3). fail 있으면 1,
// warn은 0. 무 --fix 경로의 출력·exit은 완전 불변.
func RunDoctor(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// 위치 인자 불허(wire.ParseArgs 사양 테이블 — 무접두 토큰도
	// unknown flag exit 3, 기존 계약). 값 검증은 호출부에 그대로.
	// --json 플래그 동반.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--list-checks"},
			{Name: "--fix"},
			// 기계 판독 명시 채널 — DoctorCheck 필드 구조의
			// 한 줄 JSON을 고정한다(기본 텍스트 출력은 불변).
			{Name: "--json"},
			{Name: "--check", Value: true},
		},
		Usage: "usage: flint doctor [--list-checks] [--check NAME] [--fix] [--json]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	if res.Given("--list-checks") {
		// 등록표 전량(코어6+tail10) — 구 6종만 내던
		// 회귀 방지(실제 실행 16종 중 10종이 발견 불가했음).
		names := make([]string, 0, len(doctorChecks()))
		for _, s := range doctorChecks() {
			names = append(names, s.Name)
		}
		b, _ := json.Marshal(map[string][]string{"checks": names})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	subset, fix, jsonOut := res.Value("--check"), res.Given("--fix"), res.Given("--json")
	if res.Given("--check") && !res.HasValue("--check") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("doctor: --check requires a name — usage: flint doctor [--list-checks] [--check NAME] [--fix] [--json]")))
		return 3
	}
	if subset != "" {
		// --check 허용 목록은 등록표 전량에서 파생 — 코어
		// 6종·tail 10종 전부 단독 실행 가능.
		// 전체 리포트가 보여주는 체크명을 --check로 실행할
		// 수 있어야 한다 — skill-file·harness-*는 등록표 밖 별도 체인
		// 이지만 단독 실행을 허용한다.
		_, ok := doctorSpec(subset)
		if subset == "skill-file" || strings.HasPrefix(subset, "harness-") {
			ok = true
		}
		if !ok {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("doctor: unknown check %q — usage: flint doctor [--list-checks] [--check NAME] [--fix] [--json] (names via --list-checks)", subset)))
			return 3
		}
		checks := []DoctorCheck{runDoctorCheck(ctx, st, subset)}
		bumpDoctorWarnCounters(ctx, st, checks) // 발화 카운터
		return doctorReport(checks, fix, jsonOut, stdout)
	}

	// 전체 실행 체인 — 등록표 파생(코어 6종, 하니스·스킬 별도 체인,
	// tail 10종 순서는 기존 append 순서와 동일하게 유지).
	// F6(성능): thresholds는 실행 스코프 1회 로드(doctorThresholds 지연
	// 메모)를 ThFn 검사 6종이 공유 — 검사 각자의 meta IN 쿼리 폐지.
	th := doctorThresholds(ctx, st)
	checks := make([]DoctorCheck, 0, len(doctorChecks())+len(HarnessRegistry))
	for _, s := range doctorChecks() {
		if s.Chain == "core" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}
	// 하니스 검진(v0.9 §4)은 코어 체인 뒤에 추가 — 파일이 있는 하니스만
	// 결과를 생성한다(동적 감지라 등록표 밖 별도 체인).
	checks = append(checks, harnessChecks()...)
	// skill-file도 harness와 같은 별도 체인 — 소스가
	// 탐지되는 경우에만 결과를 생성하고 warn은 exit를 깨지 않는다
	// (룰링: 스킬 없어도 훅은 동작 — fail은 과잉). home 미탐지 시
	// 검사를 생략한다(checkHookFile와 같은 처리 — Join("",…)은
	// 상대경로가 돼 cwd 오탐).
	if home := ClaudeHome(); home != "" {
		if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
			checks = append(checks, c)
		}
	}
	for _, s := range doctorChecks() {
		if s.Chain == "tail" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}
	bumpDoctorWarnCounters(ctx, st, checks) // 발화 카운터
	return doctorReport(checks, fix, jsonOut, stdout)
}

// bumpDoctorWarnCounters — 발화 카운터: warn 판정 검사별
// `fired_doctor_<check>` meta 누적 증분(spec §2.5 — 진단 D 원천, 횟수만
// 기록·개인정보 무관). 실행당 각 검사 종류 최대 1증분. 종류 수 상한은
// 검사 이름 공간 그대로 — 등록표 16종 + harness-* 14종 + skill-file 1종
// = 31종. fail·ok·skip은 경고 발화가 아니므로 미계상, 최종 checks 슬라이스
// 기준이라 --fix 재평가 전 판정의 이중 계상도 없다. 실패는 조용히(§13).
func bumpDoctorWarnCounters(ctx context.Context, st wire.Store, checks []DoctorCheck) {
	if st == nil {
		return
	}
	for _, c := range checks {
		if c.Status == "warn" {
			knowledge.BumpMetaCounter(ctx, st, "fired_doctor_"+c.Name)
		}
	}
}

// doctorReport — 검사 결과 취합·--fix 자가수복·출력·exit. 등록표 파생
// 재구성에서 서브셋·전체 경로가 공유하는 후반부. jsonOut은
// --json — DoctorCheck 필드 구조의 한 줄 JSON을 고정한다
// (--fix 적용 보고도 doctorOutput 구조로; 기본 --fix 보고는 불변).
func doctorReport(checks []DoctorCheck, fix bool, jsonOut bool, stdout io.Writer) int {
	// --fix: hook-file fail에만 자가수복 적용(다른 검사는 사용자
	// 설정·외부 파일이라 자동화 범위 밖 — 코디네이터 ruling). 적용 성공 시
	// 해당 검사만 재평가하고 doctorFixReport 한 줄로 보고한다.
	fixed := false
	if fix {
		for i, c := range checks {
			if c.Name != "hook-file" || c.Status != "fail" {
				continue
			}
			path := filepath.Join(ClaudeHome(), "settings.json")
			bin := FlintBinPathFn()
			applied, _, err := ApplyHookFix(path, bin)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("doctor --fix: hook repair aborted, file unchanged: %w", err)))
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
	if fixed && !jsonOut {
		fmt.Fprintln(stdout, DoctorFixReport(checks))
		if overall == "fail" {
			return 1
		}
		return 0
	}
	b, err := json.Marshal(doctorOutput{Status: overall, Checks: checks})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	if overall == "fail" {
		return 1
	}
	return 0
}

// runDoctorCheck — 검사명 디스패치. 등록표(doctorChecks) 조회가 1차
// 소스, 등록표 밖 별도 체인
// (skill-file·harness-*)만 여기 사례로 남는다. ThFn 검사는 실행 스코프
// thresholds(doctorThresholds — 단독 실행 1회 로드)를 주입받는다(F6).
func runDoctorCheck(ctx context.Context, st wire.Store, name string) DoctorCheck {
	if s, ok := doctorSpec(name); ok {
		return runCheckSpec(ctx, st, doctorThresholds(ctx, st), s)
	}
	switch name {
	case "skill-file":
		// 전체 리포트 체크명과 --check 이름 공간 정합.
		// home 미탐지·소스 미탐지는 skip 판정(전체 체인과 동일 규칙).
		if home := ClaudeHome(); home != "" {
			if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
				return c
			}
		}
		return DoctorCheck{Name: name, Status: "ok", Detail: "skill source not found — check skipped"}
	}
	if strings.HasPrefix(name, "harness-") {
		// harness-<name> 단독 실행 — 등록 레지스트리에서
		// 해당 하니스만 판정. 미설치(파일 부재)는 skip, 미등록 하니스명은
		// unknown.
		for _, h := range HarnessRegistry {
			if "harness-"+h.Name == name {
				if c, ok := checkHarnessFile(h); ok {
					return c
				}
				return DoctorCheck{Name: name, Status: "ok", Detail: "harness not installed — check skipped"}
			}
		}
	}
	return DoctorCheck{Name: name, Status: "fail", Detail: "unknown check"}
}

// ClaudeHome — 훅 설정 디렉터리. FLINT_CLAUDE_HOME 오버라이드 우선.
func ClaudeHome() string {
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
func checkHookFile() DoctorCheck {
	c := DoctorCheck{Name: "hook-file"}
	home := ClaudeHome()
	path := filepath.Join(home, "settings.json")
	if home == "" {
		c.Status, c.Fix = "fail", "fix: set FLINT_CLAUDE_HOME or HOME to locate the hook settings path"
		return c
	}
	b, err := os.ReadFile(path)
	if err != nil {
		c.Status = "fail"
		c.Detail = path + " not found"
		c.Fix = "fix: install the plugin (/plugin marketplace add synuns/flint → /plugin install flint@flint) or register flint hooks in settings.json — see flint-plugin/hooks/hooks.json"
		return c
	}
	if !containsFlintHook(string(b)) {
		c.Status = "fail"
		c.Detail = path + ": no flint hooks"
		c.Fix = "fix: reinstall the plugin or add flint session-start(SessionStart) and session-end(Stop) hooks to settings.json hooks — see flint-plugin/hooks/hooks.json"
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

// FlintBinPathFn — 훅 명령에 쓸 flint 바이너리 절대경로 seam(var).
// PATH에 flint가 없으면 bare 명령이 조용히 죽는다 —
// os.Executable로 직접 해석한다. 해석 실패 시 빈 문자열. cli 테스트
// 교체 관례(doctor_v094_test)는 cli 측 var 별칭 경유로 전파된다
// (ops_alias_1.go init 읽기 래퍼).
var FlintBinPathFn = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// flintHookCmd — hooks.json 명령의 bare `flint`를 binPath 절대경로로
// 치환한 훅 명령 1개. 2>/dev/null || true — 훅 실패가 세션을 흔들지
// 않게 한다(원본 flint-plugin/hooks/hooks.json 승계). timeout 10은
// DB 지연 시 2초 안에 주입이 통째로 소실되던 것을 흡수.
// eval 래핑 — jq @sh의 홑따옴표 인용을 셸이 인용으로
// 파싱해 단일 인자로 전달(따옴표 리터럴 잔류 방지). eval 입력은 고정
// 리터럴(binPath·sub)과 @sh 인용값뿐이라 임의 코드 경로가 없다.
// binPath 홑따옴표 인용 — 설치 경로의 공백이 eval
// 안에서 단어 분할되어 훅을 무음 소실시키던 것 폐쇄(shQuote).
func flintHookCmd(binPath, sub string) map[string]any {
	cmd := `eval "` + shQuote(binPath) + " " + sub + `" 2>/dev/null || true`
	return map[string]any{"type": "command", "command": cmd, "timeout": 10}
}

// shQuote — POSIX 홑따옴표 인용: 내부 홑따옴표는 닫기-이스케이프-여는
// 3단 시퀀스로 끊어 이스케이프한다(백슬래시+홑따옴표 조합 — gofmt 안정)
// (공백·메타 문자 무관 단일 인자 — eval 이중 인용 안에서도 안전).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// flintHookDefs — flint 훅 3개(통합: 이벤트 → 추가 엔트리
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
			// jq @sh가 홑따옴표 셸 인용을 만들고 eval이
			// 이를 올바르게 파싱해 단일 인자로 전달한다. 구 이중 인용 치환도
			// 결과가 재해석되지는 않았으나 인용 계층이 암묵적이었다 — 명시
			// 인용(@sh)+eval 파싱으로 원문 보존을 구조적으로 보장한다.
			// jq 폴백은 "" 대신 empty — session_id 부재 시
			// 빈 라벨을 흘려보내지 않고 플래그 자체를 생략한다(훅 진입점의
			// 무효=부재 취급과 동일 방향). hooks.json과 동일 패턴.
			withSession(`session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"Stop": {
			plain(`session-end --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"PostCompact": {plain("compact")},
	}
}

// flintHookSubRe — flint 훅 커맨드 1개 식별(실행 파일 경로 무관). flint
// 실행 뒤 훅 서브커맨드(hot·done·nudge·compact·session-start·
// session-end)가 오는 명령 — 구형 5엔트리 직접 실행과 신형 통합 커맨드
// 모두 잡는다. 'myflint'·'flintx' 같은 접두·접미 우연 일치는 경계 클래스
// 로 배제(flintWordRe와 같은 규칙 + 경로 접두 허용). 홑따옴표
// 인용된 경로의 닫는 ' 수용(shQuote 형식 — 구 무인용 형식도 계속 매칭).
var flintHookSubRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_-])(?:\S*/)?flint(?:\.\S+)?'?\s+"?(session-start|session-end|hot|done|nudge|compact)(?:[\s"]|$)`)

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

// ApplyHookFix — settings.json에 flint 훅 3개 등록·교체(자가수복,
// 통합 3엔트리). 읽기→파싱→신형 존재 검사(멱등)→타임스탬프
// 백업→구형 flint 엔트리 걷어내고 신형 append→MarshalIndent 쓰기.
// 기존 구형 5엔트리(hot·nudge/done·nudge 직접 실행)는 교체 대상 —
// flint 커맨드가 섞인 그룹만 정리하고 타 도구 훅·다른 키는 전부 불변.
// 신형(session-start·session-end)이 이미 있으면 (false, "", nil) skip.
// 파싱 실패는 파일·백업 모두 무변경 채로 err. 파일 부재 시
// 최소 뼈대 {"hooks":{}}를 새로 생성한다 — 원본이 없으니 백업은
// 생략(backup=""), 부모 디렉터리도 함께 생성. 이후 merge 경로(멱등·
// 절대경로·timeout 계약)를 그대로 재사용한다.
func ApplyHookFix(path string, binPath string) (applied bool, backupPath string, err error) {
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
		return false, "", fmt.Errorf("cannot parse settings.json: %w", err)
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
		// v0.28.0(H③): 백업 사본은 원본 권한을 승계 — WriteFile 고정
		// 퍼모드(0644)가 0600으로 관리되던 원본의 사본을 강등하던 결함.
		mode := os.FileMode(0o600)
		if fi, serr := os.Stat(path); serr == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(backupPath, b, mode); err != nil {
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

// DoctorFixReport — --fix 적용 후 결과 보고 한 줄 JSON. status "fixed" +
// 검사별 name:status. 다른 fail 검사도 이름:상태로 그대로 노출된다.
func DoctorFixReport(checks []DoctorCheck) string {
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

// HarnessSpec — 하니스 검진 대상 1종(스펙 C1~C5). Name은 체크명
// 접미·문서 파일명과 일치. Rel은 홈 기준 상대 경로(cursor만 프로젝트
// 루트=워킹디렉터리 기준). EnvOverride는 해당 디렉터리 자체를 교체
// (ClaudeHome·FLINT_CLAUDE_HOME 패턴 승계 — 테스트 주입용).
type HarnessSpec struct {
	Name        string
	Rel         string
	EnvOverride string
	Project     bool // true면 Rel의 기준이 홈이 아닌 워킹디렉터리(cursor·vscode·trae)
	// MCPAdd — 네이티브 CLI 자동 등록 인자. nil이면 파일 설정형(B급)
	// — setup --mcp 대상 아님(doctor 의사체크·문서 안내만).
	MCPAdd []string
}

// HarnessRegistry — 하니스 14종. C6: 전 하니스
// MCP stdio JSON-RPC. A급(자동 등록, 공식 문서로 CLI 검증) 8종 +
// B급(파일 설정형 — doctor 의사체크만) 6종. continue는 설정 경로가
// 파일 단위로 확정되지 않아 문서 전용(룰링).
var HarnessRegistry = []HarnessSpec{
	// A급 — 자동 감지·등록.
	{Name: "codex", Rel: ".codex/config.toml", EnvOverride: "FLINT_CODEX_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "--", "flint", "mcp"}},
	{Name: "gemini", Rel: ".gemini/settings.json", EnvOverride: "FLINT_GEMINI_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "flint", "mcp"}},
	{Name: "opencode", Rel: ".config/opencode/opencode.json", EnvOverride: "FLINT_OPENCODE_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "--", "flint", "mcp"}},
	{Name: "copilot", Rel: ".copilot/mcp-config.json", EnvOverride: "FLINT_COPILOT_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "--", "flint", "mcp"}},
	{Name: "cursor", Rel: ".cursor/mcp.json", EnvOverride: "FLINT_CURSOR_DIR", Project: true,
		MCPAdd: []string{"mcp", "add", "flint", "flint", "mcp"}},
	{Name: "qwen", Rel: ".qwen/settings.json", EnvOverride: "FLINT_QWEN_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "flint", "mcp"}},
	{Name: "devin", Rel: ".config/devin/mcp_config.json", EnvOverride: "FLINT_DEVIN_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "--", "flint", "mcp"}},
	{Name: "cline", Rel: ".cline/data/settings/cline_mcp_settings.json", EnvOverride: "FLINT_CLINE_HOME",
		MCPAdd: []string{"mcp", "add", "flint", "--yes", "--", "flint", "mcp"}},
	// B급 — 파일 설정형(등록 CLI 없음).
	{Name: "windsurf", Rel: ".codeium/windsurf/mcp_config.json", EnvOverride: "FLINT_WINDSURF_HOME"},
	{Name: "zed", Rel: ".config/zed/settings.json", EnvOverride: "FLINT_ZED_HOME"},
	{Name: "amp", Rel: ".config/amp/settings.json", EnvOverride: "FLINT_AMP_HOME"},
	{Name: "vscode", Rel: ".mcp.json", EnvOverride: "FLINT_VSCODE_DIR", Project: true},
	{Name: "goose", Rel: ".config/goose/config.yaml", EnvOverride: "FLINT_GOOSE_HOME"},
	{Name: "trae", Rel: ".trae/mcp.json", EnvOverride: "FLINT_TRAE_DIR", Project: true},
}

// path — 검사 대상 설정 파일 절대 경로. 오버라이드 env는 Rel이 붙던
// 기존 디렉터리(cursor는 프로젝트 루트) 자체를 교체한다. 홈·wd 조회
// 불능은 빈 문자열(부재 취급 — 미설치 하니스는 경고가 아님).
func (h HarnessSpec) path() string {
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

// harnessChecks — 등록 하니스 검진(§4, 14종). 설정 파일이 있는 하니스만
// 결과를 생성한다(부재 → 빈 슬라이스). 읽기 전용(비간섭).
func harnessChecks() []DoctorCheck {
	var out []DoctorCheck
	for _, h := range HarnessRegistry {
		if c, ok := checkHarnessFile(h); ok {
			out = append(out, c)
		}
	}
	return out
}

// checkHarnessFile — 하니스 1종 판정. (a) 파일 부재 → 검사 생략(ok=false),
// (b) 본문에 "flint" 없음 → fail + 문서 경로 fix, (c) 포함 → ok.
// 등록 판정은 문자열 포함(포맷 무관 — C6. 주석만 있어도 ok).
func checkHarnessFile(h HarnessSpec) (DoctorCheck, bool) {
	path := h.path()
	if path == "" {
		return DoctorCheck{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return DoctorCheck{}, false // 미설치 하니스 — 검사 생략
	}
	c := DoctorCheck{Name: "harness-" + h.Name}
	if strings.Contains(string(b), "flint") {
		c.Status, c.Detail = "ok", path
		return c, true
	}
	c.Status = "fail"
	c.Detail = path + ": flint not registered"
	c.Fix = "fix: register the flint MCP server — see docs/adapters/" + h.Name + ".md"
	return c, true
}

// checkDB — Ping.
func checkDB(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "db"}
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

// checkSchema — meta schema_version >= 4(수치 비교 — 계측 컬럼 승격
// 확인. 향후 v5 DB를 정상으로 수용, 미달·비수 값만 실패).
func checkSchema(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "schema"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — verify schema migration"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — check DB connection and schema"
		return c
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if n, convErr := strconv.Atoi(version); convErr != nil || n < 4 {
		c.Status = "fail"
		c.Detail = "schema_version=" + version
		c.Fix = "fix: reconnect with the v0.6 CLI to migrate the schema — flint status"
		return c
	}
	c.Status = "ok"
	return c
}

// checkIntegrity — flint_knowledge_* 인덱스 3행(v3 필터 인덱스).
func checkIntegrity(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "integrity"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — verify index migration"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'knowledge' AND indexname LIKE 'flint_knowledge_%'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — check DB connection and indexes"
		return c
	}
	n := -1
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &n)
	}
	if n < 3 {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("flint_knowledge_* indexes: %d (expected 3)", n)
		c.Fix = "fix: flint status — retry the v3 index migration"
		return c
	}
	c.Status = "ok"
	return c
}

// checkLastSeenTh — 3경로 last_* 경과 + grace(nodata 패턴). grace는
// maintain --check와 같은 loadThresholds 체계(stall_hot_h·stall_write_h
// 메타 오버라이드, 기본 48/72h)를 쓴다 — 임계값 소스가 둘이면 결론이
// 상충한다. 초과 또는 기록 부재(never)는 warn — exit를 깨지 않는다.
// thresholds는 주입(doctor 체인이 실행 스코프 1회 로드 공유).
func checkLastSeenTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — check DB connection"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn" // 접속 불량은 stall 오탐 방지를 위해 warn(조용히)
		c.Detail = "last_* query failed: " + err.Error()
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
	// read 경로가 meta last_read_at을 더 쓰지 않는다(쓰기
	// 4→2) — 회상 최근성은 read_events 최대 시각에서 파생해 병합한다
	// (잔존 meta 값과 더 최근 쪽. 조회 실패는 기존 값 유지 — 조용히).
	if rows, rerr := wire.QueryOnce(ctx, st,
		`SELECT max(ts) FROM read_events`, nil); rerr == nil &&
		len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			if prev, had := seen["last_read_at"]; !had || t.After(prev) {
				seen["last_read_at"] = t
			}
		}
	}
	// 임계값은 maintain와 공유 체계 — 주입 th(F6: 실행 스코프 1회 로드,
	// 실패 기본값 폴백은 WriteThresholds가 적용)에서 grace를 읽는다.
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

// checkMetaHygiene — meta 키 위생. 미지 키(사용 중단 후보)·
// 보존 창(daily_retention_days)+7일 유예 초과 daily 카운터 경고.
// 실체는 thresholds 주입판(checkMetaHygieneTh) — doctor 체인은
// 실행 스코프 1회 로드 값을, 이 래퍼는 직접 호출자용 자체 로드.
func checkMetaHygiene(ctx context.Context, st wire.Store) DoctorCheck {
	return checkMetaHygieneTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkMetaHygieneTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "meta-hygiene"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query meta"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT key FROM meta ORDER BY key`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "meta query failed: " + err.Error()
		return c
	}
	thKeys := map[string]bool{}
	for _, k := range wire.ThresholdsKeys() {
		thKeys[k] = true
	}
	retention := th.DailyRetentionD
	if retention <= 0 {
		retention = 14
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(retention + 7)).Format("2006-01-02")
	unknown, staleDaily := 0, 0
	var samples []string
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		k := fmt.Sprint(r[0])
		switch {
		case strings.HasPrefix(k, "daily:"):
			// 플랫 키(daily:<kind>:<date>) 지원 — 마지막
			// 세그먼트 날짜로 판정(maintain.go dailyKeyDate 공유).
			if d, ok := dailyKeyDate(k); ok && d < cutoff {
				staleDaily++
			}
		case KnownMetaKeyFn(k) || thKeys[k]: // pb_ 등 접두 포함(리뷰 1)
		default:
			// tuning meta 키도 허용.
			known := false
			for _, mk := range TuningMetaKeys() {
				if mk.Key == k {
					known = true
					break
				}
			}
			if !known {
				unknown++
				if len(samples) < 5 {
					// 미지 키 원문도 detail에 노출되는
					// 출력이라 살균 출구를 통과시킨다.
					samples = append(samples, render.Text(k))
				}
			}
		}
	}
	if unknown == 0 && staleDaily == 0 {
		c.Status = "ok"
		c.Detail = "meta key hygiene ok (" + strconv.Itoa(len(rows)) + " keys total)"
		return c
	}
	c.Status = "warn"
	detail := ""
	if unknown > 0 {
		detail = fmt.Sprintf("%d unknown keys (%s)", unknown, strings.Join(samples, ", "))
	}
	if staleDaily > 0 {
		if detail != "" {
			detail += " · "
		}
		detail += fmt.Sprintf("%d daily counters past retention window (+%dd grace)", staleDaily, 7)
	}
	c.Detail = detail
	c.Fix = "fix: flint maintain --meta-audit — manually clean up deprecated keys and stale daily counters"
	return c
}

// checkBackupKey — 암호화 백업 키 상태. meta
// backup_encrypt=true일 때만 실검사 — 키 부재면 다음 자동 백업이 새
// 키를 만들어 기존 암호문과 호환되지 않는 혼재가 생긴다(사전 경고가
// 존재 이유). 권한은 0600 기준(다른 로컬 계정·프로세스로부터 보호).
// 조회 오류는 '미사용 ok'가 아니라 warn(거짓 ok 금지, secrets와
// 같은 계약).
func checkBackupKey(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "backup-key"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot check key status"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'backup_encrypt'`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "backup_encrypt query failed — retry; if it persists, check the schema"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 || !BackupEncryptOnFn(fmt.Sprint(rows[0][0])) {
		c.Status = "ok"
		c.Detail = "encryption not in use (plaintext backups — opt in via backup_encrypt)"
		return c
	}
	key := BackupKeyPathFn()
	if key == "" {
		c.Status = "warn"
		c.Detail = "cannot determine config directory — cannot build key path"
		return c
	}
	fi, err := os.Stat(key)
	if err != nil {
		c.Status = "warn"
		c.Detail = "backup_encrypt is on but the key file is missing — the next encrypted backup will create a new key, risking an incompatible mix with existing ciphertext"
		c.Fix = "fix: locate the existing key (" + key + ") then verify it with flint backup --encrypt --out <path>"
		return c
	}
	if fi.Mode().Perm() != 0o600 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("key file mode %o — must be 0600 (blocks other local accounts)", fi.Mode().Perm())
		c.Fix = "fix: chmod 600 " + key
		return c
	}
	c.Status = "ok"
	c.Detail = "backup encryption key ok (0600)"
	return c
}

// CheckBackupAge — meta last_backup 경과 판정. 경과
// thresholds 레지스트리(meta backup_age_warn_h, 기본 72h — TUNING §2).
// 이상 경과·미설정·파손 값·
// 조회 실패·st nil 전부 warn — 백업 부재가 flint 동작을 막지 않으므로
// fail 아니다(룰링). checkLastSeen과 같은 queryOnce·RFC3339 패턴을 승계한다.
// 실체는 thresholds 주입판(checkBackupAgeTh) — doctor 체인은
// 실행 스코프 1회 로드 값을, 이 공개 래퍼는 직접 호출자용 자체 로드.
func CheckBackupAge(ctx context.Context, st wire.Store) DoctorCheck {
	return checkBackupAgeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkBackupAgeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "backup-age"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT key, value FROM meta WHERE key = 'last_backup'`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "last_backup query failed: " + err.Error() + " — check the flint done hook or run backup manually"
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
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}
	// 임계값은 meta 오버라이드 우선(주입 th) — 조회 실패 폴백은 상단
	// 로더가 적용, 값 폴백(<=0 → 전체 기본값)은 여기서 유지한다.
	if th.BackupAgeWarnH <= 0 {
		th = store.DefaultThresholds()
	}
	if d := time.Since(ts); d >= time.Duration(th.BackupAgeWarnH)*time.Hour {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("last backup %dh ago — check the flint done hook or run backup manually", int(d.Hours()))
		return c
	}
	c.Status = "ok"
	return c
}

// DoctorConflictLimit — doctor conflicts 조회 상한.
// maintain 기본(10)보다 넉넉히 — 상위 10그룹만 세면 초과 모순이
// 미보고로 누락된다. 여전히 상한이 있어 무제한 스캔은 없다.
const DoctorConflictLimit = 50

// conflictStore — 모순 후보 보고에 필요한 추가 조회(요약 포함 그룹).
// maintain.go 정의와 동일 메서드 집합(구조적 타입이라 단언 행동 불변).
type conflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

// CheckConflicts — 모순 후보 그룹 수 판정. active 동일
// topic 2건+ 그룹(maintain --conflict-suspects와 같은 GROUP BY 조건)을
// 요약 유사도로 분류해(isConflictGroup — 동일 기준) 모순 후보만
// 세어 1+면 warn, 0이면 ok. 순수 중복 그룹(유사 요약)은 세지 않는다 —
// 목록 출력과 동치여야 안내받은 --conflict-suspects가 결과를 보장한다.
// 조회는 maintain와 같은 ConflictStore seam(ConflictGroups)으로만 —
// GROUP BY SQL을 여기 다시 쓰지 않는다. 상한은 doctor
// 전용 DoctorConflictLimit(50) — 결과가 상한에 도달하면 detail에
// 초과 가능을 밝혀 미보고로 오인하지 않게 한다.
// seam 미지원·조회 실패·st nil도 warn — fail 아니다(모순 의심은
// 동작을 막지 않는다).
func CheckConflicts(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "conflicts"}
	cs, ok := st.(conflictStore)
	if st == nil || !ok {
		c.Status = "warn"
		c.Detail = "cannot query conflict candidates — check maintain --conflict-suspects"
		return c
	}
	groups, err := cs.ConflictGroups(ctx, DoctorConflictLimit)
	if err != nil {
		c.Status = "warn"
		c.Detail = "conflict candidate query failed: " + err.Error() + " — check maintain --conflict-suspects"
		return c
	}
	// 분류 임계값은 thresholds 레지스트리 기본값(conflictDupThreshold).
	// meta 오버라이드 적용 경로는 maintain
	// --conflict-suspects — doctor 검사는 계약상 ConflictGroups
	// seam 외 조회(임계값 읽기를 포함)를 하지 않는다.
	n := 0
	for _, g := range groups {
		if IsConflictGroupFn(g, ConflictDupThresholdFn()) {
			n++
		}
	}
	if n > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d conflict candidate groups — maintain --conflict-suspects", n)
		if len(groups) >= DoctorConflictLimit {
			c.Detail += fmt.Sprintf(" · group query limit %d reached — more may exist", DoctorConflictLimit)
		}
		return c
	}
	c.Status = "ok"
	return c
}

// doctorZeroNearWindow — zero_near 검사 aggregation 창(일).
// 오늘 포함 최근 7일 — report --days 기본창과 같은 규모. 표본 하한·경고
// 비율은 thresholds 레지스트리(zero_near_min_read 기본 10·
// zero_near_warn_pct 기본 30 — TUNING §10).
const doctorZeroNearWindow = 7

// CheckZeroNear — 최근 7일 일별 read 계측 합산에서 근접 제로히트 비율
// 판정. read_stats blob·daily read 카운터 폐지로
// 산출 원천은 WindowedDaily(daily:* 잔존값 + read_events 파생 병합 — report
// 와 단일 소스)로 이전. 판정 식(read ≥ 표본 하한 && read_zero_near*100/
// read ≥ 경고 비율 → warn)·출력 문구는 전환 전과 동등하다. 조회 실패·st
// nil은 warn. 검색 품질 의심은 동작을 막지 않으므로 fail 아니다(룰링).
// 실체는 thresholds 주입판(checkZeroNearTh) — doctor 체인은
// 실행 스코프 1회 로드 값을, 이 공개 래퍼는 직접 호출자용 자체 로드.
func CheckZeroNear(ctx context.Context, st wire.Store) DoctorCheck {
	return checkZeroNearTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkZeroNearTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "zero-near"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query daily metrics — check read_zero_near via flint report"
		return c
	}
	// 임계값 2종은 meta 오버라이드 우선(주입 th) — 조회 실패 폴백(기본값)은
	// 상단 로더가 적용.
	days, err := WindowedDaily(ctx, st, doctorZeroNearWindow)
	if err != nil {
		c.Status = "warn"
		c.Detail = "daily metrics query failed: " + err.Error() + " — check read_zero_near via flint report"
		return c
	}
	read, near := 0, 0
	for _, d := range days {
		read += d.Read
		near += d.ReadNear
	}
	if read < th.ZeroNearMinRead {
		c.Status = "ok"
		c.Detail = fmt.Sprintf("insufficient samples (7d read %d < %d) — verdict deferred", read, th.ZeroNearMinRead)
		return c
	}
	if pct := near * 100 / read; pct >= th.ZeroNearWarnPct {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("zero_near_pct %d%% (%d near of %d reads over 7d) — possible vocabulary mismatch — search quality could improve",
			pct, read, near)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("zero_near_pct %d%% (7d read %d)", near*100/read, read)
	return c
}

// CheckPendingAge — pending 최고령 valid_at 경과 판정. 경과
// 임계는 thresholds 레지스트리(meta pending_age_days, 기본 7일 — TUNING).
// 임계 이상 경과·조회 실패·st nil은
// warn(방치가 동작을 막지 않는다 — backup-age 룰링 승계), 임계 이내·
// pending 부재는 ok. detail에 만료 명령(maintain --expire-pending N) 안내를
// 붙인다. 정리 자체는 dry-run 기본 명령에 맡긴다(자동 전환 아님).
// 실체는 thresholds 주입판(checkPendingAgeTh) — doctor 체인은
// 실행 스코프 1회 로드 값을, 이 공개 래퍼는 직접 호출자용 자체 로드.
func CheckPendingAge(ctx context.Context, st wire.Store) DoctorCheck {
	return checkPendingAgeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkPendingAgeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "pending-age"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query pending age — clean up with maintain --expire-pending N"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT valid_at FROM knowledge
		WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at ASC LIMIT 1`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "pending age query failed: " + err.Error() + " — clean up with maintain --expire-pending N"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		c.Status = "ok"
		return c
	}
	va, ok := AnyToTimeFn(rows[0][0])
	if !ok {
		c.Status = "ok" // 파손 값은 pending 부재 취급(무효 값 폴백)
		return c
	}
	// 임계값은 meta 오버라이드 우선(주입 th) — 조회 실패 폴백은 상단
	// 로더가 적용, 값 폴백(<=0 → 전체 기본값)은 여기서 유지한다.
	if th.PendingAgeDays <= 0 {
		th = store.DefaultThresholds()
	}
	if d := time.Since(va); d >= time.Duration(th.PendingAgeDays)*24*time.Hour {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("oldest pending %dd past — clean up with maintain --expire-pending N",
			int(d.Hours()/24))
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
		return key + " never recorded"
	}
	if d := time.Since(ts); d > time.Duration(graceH)*time.Hour {
		*warns++
		return fmt.Sprintf("%s elapsed %s (over %dh grace)", key, d.Round(time.Hour), graceH)
	}
	return ""
}

// doctorPort — flint 기대 호스트 게시 포트(compose 오버라이드 전 기본값).
// v0.9.7 ADR-0010: 외부 컨테이너의 5433 점유로 5434로 이전.
const doctorPort = 5434

// ComposePsFn — docker compose ps seam(var). docker 부재·compose 프로젝트
// 부재 등 모든 오류는 checkPort가 skip으로 처리한다(비간섭). cli 측
// 테스트 교체(doctor_v07_test)는 ops_alias_1.go init의 읽기 래퍼로
// 실행 경로에 전파된다.
var ComposePsFn = func(ctx context.Context) ([]byte, error) {
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

// checkPort — 5434 게시 상태 판정. ① flint 실행+5434 → ok
// ② flint 부재/정지 → fail + up -d 안내 ③ 타 컨테이너 점유 → fail +
// 점유자 이름 + 포트 이동 안내. ComposePsFn 오류는 skip(docker 미설치
// 환경에서 doctor 나머지 검사를 덮지 않는다).
func checkPort(ctx context.Context) DoctorCheck {
	c := DoctorCheck{Name: "port"}
	// docker 무응답 시 무기한 블록 방지 — 2초 상한. 만료는 ComposePsFn
	// 오류로 떨어져 기존 skip 경로를 따른다(비간섭).
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		c.Status = "skip"
		c.Detail = "docker compose ps failed: " + err.Error()
		return c
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		c.Status = "skip"
		c.Detail = "cannot parse compose ps output: " + perr.Error()
		return c
	}
	// ① flint 컨테이너 실행 중 + 5434 게시.
	for _, e := range entries {
		if IsFlintContainer(e.Name) && e.State == "running" && publishesPort(e, doctorPort) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s publishes %d", e.Name, doctorPort)
			return c
		}
	}
	// ③ 타 컨테이너가 5434 점유 — 점유자를 지목한다.
	for _, e := range entries {
		if publishesPort(e, doctorPort) {
			c.Status = "fail"
			c.Detail = fmt.Sprintf("port %d occupied by %s", doctorPort, e.Name)
			c.Fix = "fix: move the port — FLINT_DSN + compose override (TUNING §2)"
			return c
		}
	}
	// ② flint 없음/정지.
	c.Status = "fail"
	c.Detail = fmt.Sprintf("flint container does not publish %d", doctorPort)
	c.Fix = "fix: docker compose up -d (flint repository)"
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
			return nil, errors.New("expected array start")
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

// IsFlintContainer — 컨테이너 이름이 flint 서비스임(flint-doltgres-1 등).
// 접두 매칭 — 'conflint'처럼 flint를 포함하나 접두가 아닌 이름의 오탐 배제.
func IsFlintContainer(name string) bool {
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

// checkEventVolume — 효과 계측
// 테이블 용량 점검. 3테이블 합계 행 수가 events_volume_warn(기본
// 50000, meta 오버라이드)을 넘으면 warn — 보존일(events_retention_days)
// 단축 또는 PruneReadEvents 정상 작동 여부 점검 안내. 성장은 정상
// 동작의 결과이므로 fail 아니다(비간섭 원칙 — warn 안내만).
// 실체는 thresholds 주입판(checkEventVolumeTh) — doctor 체인은
// 실행 스코프 1회 로드 값을, 이 래퍼는 직접 호출자용 자체 로드.
func checkEventVolume(ctx context.Context, st wire.Store) DoctorCheck {
	return checkEventVolumeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkEventVolumeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "event-volume"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query metrics tables"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT (SELECT count(*) FROM read_events)
		     + (SELECT count(*) FROM session_starts)
		     + (SELECT count(*) FROM hot_events)`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		c.Status = "warn"
		c.Detail = "metrics table aggregation failed — schema v7 may not be migrated"
		return c
	}
	n, _ := render.CellToInt(rows[0][0])
	if n >= th.EventsVolumeWarn {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("metrics tables at %d rows — threshold %d (events_volume_warn). Check retention (events_retention_days, hot_events_retention_days)", n, th.EventsVolumeWarn)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("metrics tables at %d rows", n)
	return c
}

// checkUpdate — 현재 빌드 대비 최신 릴리스 지연
// 경고. doctor는 사용자 실행 경로라 네트워크 확인을 직접 수행한다(5초
// 타임아웃). 지연은 warn 안내만(fail 아님 — 업데이트는 명시적 실행 계약).
func checkUpdate(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "update"}
	if !ValidVersionFn(CurrentVersionFn()) {
		c.Status = "ok"
		c.Detail = "build version not finalized (dev) — verdict deferred"
		return c
	}
	latest, err := FetchLatestVersionFn()
	cur := CurrentVersionFn()
	if err != nil {
		c.Status = "ok"
		c.Detail = "cannot check the latest version (possibly offline) — verdict deferred"
		return c
	}
	if CompareVersionsFn(latest, cur) > 0 {
		c.Status = "warn"
		c.Detail = "v" + cur + " → v" + latest + " outdated"
		c.Fix = "fix: flint update"
		return c
	}
	c.Status = "ok"
	c.Detail = "v" + cur + " up to date"
	return c
}

// checkRecallStarved — 누적 회상 부재 카운터 관측. minimal
// 프로파일이 마커를 자동 종결해도 이 카운터는 프로파일 무관 —
// doctor가 피드백 루프의 최후 관측 창구가 된다. 카운터 자체는 안내
// (warn), 리셋 안내는 flint tuning reset 개념이 없는 비레지스트리
// 키라 삭제 안내로.
func checkRecallStarved(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "recall-starved"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query the recall counter"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT value FROM meta WHERE key = $1`, []any{"recall_starved_count"})
	if err != nil {
		c.Status = "warn" // 조회 실패를 깨끗한 소견으로 위장하지 않는다(리뷰 ③).
		c.Detail = "recall counter query failed"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		c.Status = "ok"
		c.Detail = "no recall-starved history"
		return c
	}
	if n, ok := render.CellToInt(rows[0][0]); ok && n > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d recall-starved sessions cumulated — the done warning may have been hidden by the minimal profile or hook wiring", n)
		c.Fix = "fix: run flint read before the next session starts — once resolved, remove the counter via psql (meta key recall_starved_count)"
		return c
	}
	c.Status = "ok"
	return c
}

// checkSecrets — 보유 지식 사후 비밀 스캔.
// 저장 시점 스캔(write ScanSecrets)은 회피·이후 유입을 못 잡는다 —
// doctor가 knowledge 전수를 재스캔해 발견 시 warn(본문 아닌 패턴명+id만
// 출력 — CLAUDE.md 개인 데이터 원칙). 세션 summary도 포함(PII 최다).
// 실체는 thresholds 주입판(checkSecretsTh) — doctor
// 체인은 실행 스코프 1회 로드 값(doctorThresholds)을 공유하고, 이 래퍼는
// 직접 호출자용 자체 로드(checkMetaHygiene F6 패턴 승계).
func checkSecrets(ctx context.Context, st wire.Store) DoctorCheck {
	return checkSecretsTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkSecretsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "secrets"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query for the secrets scan"
		return c
	}
	// 리뷰 ①②⑥: 무효화(soft) 행은 제외(무효화 자체가 이 체크의 해소
	// 경로)·스캔 열은 쓰기 시점과 동일 전열(topic·summary·tags·entity·
	// domain·origin — sanitizePayload 참조)·메모리는 행 단위 스트리밍
	// (Store.Query 커서 — 전체 테이블 구체화 회피).
	// 임계값은 주입 th(실행 스코프 1회 로드,
	// 실패 기본값 폴백은 WriteThresholds가 적용)에서 읽는다.
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, tags, entity, domain, origin
		FROM knowledge WHERE invalid_at IS NULL
		LIMIT $1`, []any{th.SecretsScanRows})
	if err != nil {
		c.Status = "warn"
		c.Detail = "knowledge query failed — check the schema"
		return c
	}
	if len(rows) >= th.SecretsScanRows {
		// 상한 도달 — 부분 스캔임을 명시(오탐
		// 아닌 미도달 오탐 방지 — '미발견'으로 거짓 보고하지 않는다).
		c.Status = "warn"
		c.Detail = fmt.Sprintf("row limit %d reached — scan not run (large DB)", th.SecretsScanRows)
		c.Fix = "fix: fall back to write-time warnings — adjust the limit via the secrets_scan_row_limit key"
		return c
	}
	// 리뷰 ⑥ 룰링: Store.Query는 [][]any(전체 구체화)라 진짜 스트리밍은
	// 인터페이스 확장이 필요 — 개인 규모(수천 행)에서 과잉. 전열 확장·
	// 중복 방지·무효화 제외만으로 취지(사각 폐쇄)를 달성하고 메모리는
	// doctor의 다른 전수 쿼리(checkRecallStarved 등)와 동일 수준.
	found := 0
	seen := map[string]bool{} // 리뷰 ③: id+패턴 중복 계수 방지
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		text := strings.Join([]string{fmt.Sprint(r[1]), fmt.Sprint(r[2]), fmt.Sprint(r[3]), fmt.Sprint(r[4]), fmt.Sprint(r[5]), fmt.Sprint(r[6])}, " ")
		for _, name := range store.ScanSecrets(text) {
			id := fmt.Sprint(r[0])
			key := name + "#" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			found++
		}
	}
	// sessions 테이블 사후 스캔 — summary가 PII
	// 최다 필드라 주석 의도가 코드에 없던 것을 회수.
	// 조회 실패·상한 도달은 이미 잡은 지식 발견을 버리지 않고
	// 격차만 명시(리뷰 2·3) — '세션까지 스캔했고 미발견'이라는 거짓 ok도
	// 내지 않는다. LIMIT는 지식 경로와 동일 상한(메모리 무한 증식 방지).
	srows, serr := wire.QueryOnce(ctx, st, `
		SELECT id, agent, summary FROM sessions
		LIMIT $1`, []any{th.SecretsScanRows})
	gap := "" // 스캔 격차 주석(발견이 있으면 함께 보고)
	switch {
	case serr != nil:
		gap = "· session query failed (sessions unscanned)"
	case len(srows) >= th.SecretsScanRows:
		gap = "· session row limit reached (partial scan)"
	}
	sessionFound := false
	if serr == nil {
		for _, r := range srows {
			if len(r) < 3 {
				continue
			}
			text := fmt.Sprint(r[1]) + " " + fmt.Sprint(r[2])
			for _, name := range store.ScanSecrets(text) {
				key := name + "#session:" + fmt.Sprint(r[0])
				if !seen[key] {
					seen[key] = true
					found++
					sessionFound = true
				}
			}
		}
	}
	// projects.name 사후 스캔 — restore·레거시 유입은
	// 쓰기 시점 스캔을 거치지 않는다(세션 경로와 같은 사각). 발견 key는
	// 패턴명#project:id로 본문 미출력 계약 유지.
	prows, perr := wire.QueryOnce(ctx, st, `
		SELECT id, name FROM projects
		LIMIT $1`, []any{th.SecretsScanRows})
	switch {
	case perr != nil:
		gap += "· project query failed (projects unscanned)"
	case len(prows) >= th.SecretsScanRows:
		gap += "· project row limit reached (partial scan)"
	}
	projectFound := false
	if perr == nil {
		for _, r := range prows {
			if len(r) < 2 {
				continue
			}
			for _, name := range store.ScanSecrets(fmt.Sprint(r[1])) {
				key := name + "#project:" + fmt.Sprint(r[0])
				if !seen[key] {
					seen[key] = true
					found++
					projectFound = true
				}
			}
		}
	}
	if found == 0 && gap != "" {
		c.Status = "warn"
		c.Detail = "incomplete scan " + gap
		c.Fix = "fix: rerun flint doctor — if it persists, check the schema and secrets_scan_row_limit"
		return c
	}
	if found == 0 {
		c.Status = "ok"
		c.Detail = "no secrets found in knowledge, sessions, or projects"
		return c
	}
	c.Status = "warn"
	disp := make([]string, 0, 5)
	for _, k := range sortedKeys(seen) {
		if len(disp) == 5 {
			break
		}
		disp = append(disp, k)
	}
	// allowlist 적용 상태 노출 — 오타·무단 등록 감지 수단.
	// 세션 유래 발견(session:N)의 해소는 knowledge 무효화가
	// 아니다 — 세션 요약은 재종료(done) 갱신·운영 DB 정리 경로만 존재.
	// 안내가 무관 지식 행 무효화를 유도하지 않게 분기한다.
	c.Detail = fmt.Sprintf("%d secret candidates (%s) %s· allowlist %d entries",
		found, strings.Join(disp, ", "), gapSuffix(gap), store.SecretsAllowlistCount())
	if sessionFound || projectFound {
		c.Fix = "fix: for session/project findings (session:N, project:N), re-finalize the agent (done) or clean the sessions/projects rows in the operating DB — for knowledge findings, maintain --invalidate --id N --reason secret"
	} else {
		c.Fix = "fix: flint maintain --invalidate --id N --reason secret (rescan skips invalidated rows)"
	}
	return c
}

// sortedKeys — map 키 정렬(결정적 출력).
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gapSuffix — 스캔 격차 주석의 detail 접미 정규화(빈 값은 "— ... " 유지).
func gapSuffix(gap string) string {
	if gap == "" {
		return "— clears after invalidating the targets "
	}
	return gap + " "
}
