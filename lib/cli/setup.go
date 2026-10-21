// setup/teardown 커맨드 — v0.4 원커맨드 설정(QUICKSTART 1커맨드).
// setup: ①docker compose up -d(exec, 실패는 안내로 계속) ②Migrate ③seed 안내
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

// composeUpFn·composeDownFn — exec seam. 테스트는 fake 교체(순수 단계테스트).
var (
	composeUpFn = func(ctx context.Context) error {
		return composeRun(ctx, "up", "-d")
	}
	composeDownFn = func(ctx context.Context, purge bool) error {
		if purge {
			return composeRun(ctx, "down", "-v")
		}
		return composeRun(ctx, "down")
	}
)

// composeRun — docker compose 실행. 실패 시 stderr를 에러에 실어
// 안내 출력로 원인이 보이게 한다(장애 침묵 금지).
func composeRun(ctx context.Context, args ...string) error {
	argv := append([]string{"compose"}, args...)
	out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetupSteps — setup 단계 고정 순서(①compose ②migrate ③seed ④플러그인).
func SetupSteps() []string {
	return []string{
		"docker compose up -d",
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
	var warns []string
	status := "ok"

	if err := composeUpFn(ctx); err != nil {
		warns = append(warns, "docker compose up 실패 — docker 기동 후 재실행: "+err.Error())
	}
	if err := ping(); err != nil {
		status = "degraded"
		warns = append(warns, "DB ping 실패 — make up 후 flint setup 재실행")
	} else if err := migrate(); err != nil {
		status = "degraded"
		warns = append(warns, "migrate 실패: "+err.Error())
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

// runSetup — 커맨드 진입점. --dry는 단계만 출력(exec·쿼리 없음).
// store-free 디스패치로 st== nil일 수 있다(첫 실행) — ping/migrate는
// degraded 경고로 연기하고 1단계 compose는 수행한다.
func runSetup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for _, a := range args {
		if a == "--dry" {
			b, _ := json.Marshal(map[string]any{"status": "dry", "steps": SetupSteps()})
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", a)))
		return 1
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
	line, code := RunSetup(ctx, ping, migrate)
	fmt.Fprintln(stdout, line)
	return code
}

// teardownSummary — teardown 요약 한 줄 JSON 모양.
type teardownSummary struct {
	Status   string   `json:"status"`
	Purge    bool     `json:"purge"`
	Warnings []string `json:"warnings,omitempty"`
	Next     string   `json:"next"`
}

// runTeardown — 컨테이너 정지 안내 + exec. --purge 시 volume 삭제(down -v).
// 실패는 경고 + 수동 명령 안내로 exit 1.
func runTeardown(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	purge := false
	for _, a := range args {
		if a == "--purge" {
			purge = true
			continue
		}
		fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", a)))
		return 1
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
