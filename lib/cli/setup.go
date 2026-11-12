// setup/teardown 커맨드 — v0.4 원커맨드 설정(QUICKSTART 1커맨드).
// setup: ①docker compose up -d --wait(exec, 실패는 안내로 계속) ②Migrate ③seed 안내
// ④플러그인 설치 안내 — 진행 요약 한 줄 JSON. teardown: 컨테이너 정지,
// --purge 시 volume까지 삭제 exec. 실패해도 다음 단계 안내는 유지한다.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// composeRunFn — docker compose 실행 seam(var). 실패 시 stderr를 에러에
// 실어 안내 출력로 원인이 보이게 한다(장애 침묵 금지). 테스트는 fake 교체.
var composeRunFn = func(ctx context.Context, args ...string) error {
	argv := append([]string{"compose"}, args...)
	out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeUpFn·composeDownFn — 단계 seam. up은 --wait으로 컨테이너
// 준비 완료까지 대기(스펙 §2, 60s 타임아웃).
var (
	composeUpFn = func(ctx context.Context) error {
		return composeRunFn(ctx, "up", "-d", "--wait", "--wait-timeout", "60")
	}
	composeDownFn = func(ctx context.Context, purge bool) error {
		if purge {
			return composeRunFn(ctx, "down", "-v")
		}
		return composeRunFn(ctx, "down")
	}
)

// SetupSteps — setup 단계 고정 순서(①compose ②migrate ③seed ④플러그인).
func SetupSteps() []string {
	return []string{
		"docker compose up -d --wait --wait-timeout 60",
		"migrate",
		"seed (make seed)",
		"install plugin (flint-plugin/)",
	}
}

// setupNext — ③④ 안내. 실행이 아니라 다음 수동 단계 안내다(비간섭 원칙).
var setupNext = []string{
	"make seed — 예시 데이터 시딩",
	"flint-plugin/ 디렉터리를 플러그인 경로에 설치",
}

// setupSummary — 진행 요약 한 줄 JSON 모양.
type setupSummary struct {
	Status   string   `json:"status"`
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings,omitempty"`
	Next     []string `json:"next"`
}

// RunSetup — ①~④ 실행(③④는 안내) 후 요약 JSON과 exit 코드를 반환한다.
// compose 실패는 경고로만(ping이 성공하면 계속), ping/migrate 실패는
// degraded + 수동 재시도 안내로 exit 1.
func RunSetup(ctx context.Context, ping, migrate func() error) (string, int) {
	return runSetupSteps(ctx, ping, migrate, nil)
}

// runSetupSteps — RunSetup 본체. ux가 nil이 아니면 각 단계 진행을
// setupUX로 표시한다(요약 JSON·경고·exit 계약은 ux 유무와 무관하게 동일).
func runSetupSteps(ctx context.Context, ping, migrate func() error, ux *setupUX) (string, int) {
	var warns []string
	status := "ok"

	if ux != nil {
		ux.Step("Doltgres 기동 (docker compose up)")
	}
	if err := composeUpFn(ctx); err != nil {
		// docker 미설치(exec not found)는 전용 안내로 구분한다.
		if errors.Is(err, exec.ErrNotFound) {
			warns = append(warns, "docker 미설치 — https://docs.docker.com/get-docker/ 설치 후 재실행")
		} else {
			warns = append(warns, "docker compose up 실패 — docker 기동 후 재실행: "+err.Error())
		}
		if ux != nil {
			ux.Fail(err)
		}
	} else if ux != nil {
		ux.Success("up --wait 완료")
	}
	if ux != nil {
		ux.Step("DB 연결 확인 (ping)")
	}
	if err := ping(); err != nil {
		status = "degraded"
		warns = append(warns, "DB ping 실패 — make up 후 flint setup 재실행")
		if ux != nil {
			ux.Fail(err)
		}
	} else {
		if ux != nil {
			ux.Success("연결됨")
			ux.Step("마이그레이션")
		}
		if err := migrate(); err != nil {
			status = "degraded"
			warns = append(warns, "migrate 실패: "+err.Error())
			if ux != nil {
				ux.Fail(err)
			}
		} else if ux != nil {
			ux.Success("스키마 동기")
		}
	}

	b, err := json.Marshal(setupSummary{Status: status, Steps: SetupSteps(), Warnings: warns, Next: setupNext})
	if err != nil {
		return errLine(err), 1
	}
	code := 0
	if status != "ok" {
		code = 1
	}
	return string(b), code
}

// exposureSummary — setup --exposure 기록 보고 한 줄 JSON 모양(v0.11.0 R1).
type exposureSummary struct {
	Status   string `json:"status"`
	Exposure string `json:"exposure"`
}

// runSetupExposure — --exposure normal|minimal 기록만 수행하고 즉시 반환
// (compose·migrate 체인에 끼지 않는다 — --update-rules와 같은 단독 모드).
// setup은 store-free 진입이라 st==nil이면 여기서만 접속한다. 무효 값은
// 무효 인자 exit 3(전역 계약), 기록 실패는 exit 1.
func runSetupExposure(ctx context.Context, st Store, val string, stdout io.Writer) int {
	switch val {
	case "normal", "minimal":
	default:
		fmt.Fprintln(stdout, errLine(fmt.Errorf("setup: --exposure must be normal|minimal, got %q", val)))
		return 3
	}
	if st == nil {
		s, err := newStore(ctx, dsnFromEnv())
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		defer s.Close()
		st = s
	}
	// v0.34.3 T2: 노출 기록 upsert는 setMetaKV 단일 진입 위임.
	if err := setMetaKV(ctx, st, ExposureKey, val); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	b, err := json.Marshal(exposureSummary{Status: "ok", Exposure: val})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// runSetup — 커맨드 진입점. --dry는 단계만 출력(exec·쿼리 없음).
// --json(v0.17.0 R2)은 기존 한 줄 JSON 요약 완전 불변, 미지정 시 TTY면
// setupUX 시각 진행으로 출력한다(동작 계약은 동일 — ping·migrate 동일
// 순서·횟수). non-TTY(리다이렉션·스크립트)는 기존 JSON을 그대로 둔다.
// --update-rules(v0.9.7 R1)는 스킬 싱크만 수행하고 즉시 반환 — compose·
// migrate 체인에 끼지 않는다. --exposure(v0.11.0 R1)도 프로파일 기록만,
// --mcp(v0.16.5 R1)도 하네스 MCP 등록만 수행하고 즉시 반환한다.
// store-free 디스패치로 st== nil일 수 있다
// (첫 실행) — ping/migrate는 degraded 경고로 연기하고 1단계 compose는
// 수행한다.
func runSetup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	updateRules := false
	srcOverride := ""
	exposure := ""
	jsonMode := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry":
			b, _ := json.Marshal(map[string]any{"status": "dry", "steps": SetupSteps()})
			fmt.Fprintln(stdout, string(b))
			return 0
		case "--json":
			jsonMode = true
		case "--update-rules":
			updateRules = true
		case "--mcp":
			return runSetupMCP(stdout)
		case "--src":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(errors.New("setup: --src requires a path")))
				return 3
			}
			srcOverride = args[i]
		case "--exposure":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(errors.New("setup: --exposure requires normal|minimal")))
				return 3
			}
			exposure = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 3
		}
	}
	if exposure != "" {
		return runSetupExposure(ctx, st, exposure, stdout)
	}
	if updateRules {
		return runUpdateRules(stdout, srcOverride)
	}
	ping := func() error {
		if st == nil {
			return errors.New("store unavailable (첫 실행 — compose 후 재시도)")
		}
		return st.Ping(ctx)
	}
	migrate := func() error {
		if st == nil {
			return errors.New("store unavailable (첫 실행 — compose 후 재시도)")
		}
		return st.Migrate(ctx)
	}
	if jsonMode {
		line, code := RunSetup(ctx, ping, migrate)
		fmt.Fprintln(stdout, line)
		return code
	}
	// 미지정 시 시각 진행 — 단, TTY에서만(터미널이 아니면 기존 한 줄
	// JSON 유지: 스크립트·파이프 소비자 호환).
	ux := newSetupUX(stdout, false)
	if !ux.isTTY {
		line, code := RunSetup(ctx, ping, migrate)
		fmt.Fprintln(stdout, line)
		return code
	}
	_, code := runSetupSteps(ctx, ping, migrate, ux)
	ux.Summary(setupNext)
	return code
}

// lookPathFn — 하네스 CLI 탐지 seam(exec.LookPath). 선언은 프로덕션
// 파일에 둔다(_test.go 전용 var는 go build가 깨진다) — 테스트는 교체만.
var lookPathFn = exec.LookPath

// execCommandFn — 하네스 네이티브 CLI 실행 seam(var). 인자 name은
// lookPathFn이 반환한 절대 경로다(가짜 경로는 ENOENT로 즉시 실패 —
// 테스트가 실제 하네스 설정을 건드리지 않는다). 실패는 치명 아님.
var execCommandFn = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// (v0.21.0: mcpAddArgs switch는 harnessSpec.MCPAdd 필드로 이전 —
// 하니스 추가가 레지스트리 1행으로 끝나게.)

// mcpSetupSummary — setup --mcp 결과 한 줄 JSON 모양(v0.16.5 R1).
type mcpSetupSummary struct {
	Status     string   `json:"status"`
	Registered []string `json:"registered"`
	Skipped    []string `json:"skipped"`
	Detail     []string `json:"detail,omitempty"`
}

// runSetupMCP — A급 하니스(MCPAdd 보유) 순회: exec.LookPath 탐지 후
// 각 네이티브 CLI로 `flint mcp`를 등록한다(v0.21.0: copilot CLI 검증
// 승격 — 안내 전용 분기 폐지, 인자는 spec에서). B급(MCPAdd nil — 파일
// 설정형)은 setup 대상 아님: 목록에서 제외(doctor 의사체크·문서 안내).
// 미설치는 skipped(무해 — exit 0). 등록 실행 실패(이미 등록 등) 역시
// 치명 아님 — 설치 자체는 확인됐으므로 registered 유지·사유는 detail에.
func runSetupMCP(stdout io.Writer) int {
	registered := []string{}
	skipped := []string{}
	var detail []string
	for _, h := range harnessRegistry {
		if h.MCPAdd == nil {
			continue // B급 — 파일 설정형
		}
		path, err := lookPathFn(h.Name)
		if err != nil {
			skipped = append(skipped, h.Name)
			continue
		}
		if err := execCommandFn(path, h.MCPAdd...); err != nil {
			detail = append(detail, fmt.Sprintf("%s: %v", h.Name, err))
		}
		registered = append(registered, h.Name)
	}
	b, err := json.Marshal(mcpSetupSummary{Status: "ok", Registered: registered, Skipped: skipped, Detail: detail})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// teardownSummary — teardown 요약 한 줄 JSON 모양.
type teardownSummary struct {
	Status   string   `json:"status"`
	Purge    bool     `json:"purge"`
	Warnings []string `json:"warnings,omitempty"`
	Next     string   `json:"next"`
}

// runTeardown — 컨테이너 정지 안내 + exec. --purge 시 volume 삭제(down -v).
// 실패는 경고 + 수동 명령 안내로 exit 1. v1.0.1(F3): unknown flag는 무효
// 인자 exit 3.
func runTeardown(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	purge := false
	for _, a := range args {
		if a == "--purge" {
			purge = true
			continue
		}
		fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", a)))
		return 3
	}

	manual := "docker compose down"
	if purge {
		manual += " -v"
	}
	status := "ok"
	var warns []string
	if err := composeDownFn(ctx, purge); err != nil {
		status = "degraded"
		warns = append(warns, "정지 실패 — 수동 실행: "+manual+": "+err.Error())
	}

	b, err := json.Marshal(teardownSummary{
		Status:   status,
		Purge:    purge,
		Warnings: warns,
		Next:     "컨테이너 정지 안내: " + manual,
	})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	if status != "ok" {
		return 1
	}
	return 0
}

func init() {
	Register("setup", runSetup)
	Register("teardown", runTeardown)
}
