package doclint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const surfaceProbeDSN = "postgres://surface_probe:surface_probe@127.0.0.1:1/surface_probe_isolated"

var (
	flintBuildOnce sync.Once
	flintBuildPath string
	flintBuildErr  error
)

func flintBin(t *testing.T) string {
	t.Helper()
	flintBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "flint-surface-contract")
		if err != nil {
			flintBuildErr = err
			return
		}
		flintBuildPath = filepath.Join(dir, "flint")
		cmd := exec.Command("go", "build", "-o", flintBuildPath, "./cmd/flint")
		cmd.Dir = "../.."
		out, berr := cmd.CombinedOutput()
		if berr != nil {
			flintBuildErr = fmt.Errorf("flint 빌드 실패: %v — %s", berr, strings.TrimSpace(string(out)))
		}
	})
	if flintBuildErr != nil {
		t.Fatal(flintBuildErr)
	}
	return flintBuildPath
}

func runFlintSurface(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(flintBin(t), args...)
	cmd.Env = append(os.Environ(),
		"FLINT_DSN="+surfaceProbeDSN,
		"FLINT_NO_TUI=1",
		"FLINT_LANG=en",
	)
	cmd.Stdin = strings.NewReader("")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

type surfaceProbe struct {
	name     string
	args     []string
	wantExit int
	wantKey  string
	fixHint  string
	note     string
}

func surfaceProbes() []surfaceProbe {
	return []surfaceProbe{
		{"seed", nil, 3, `"error":"usage"`, "--demo", "커맨드 표 seed 행 — 필수 플래그 --demo 부재 bare usage"},
		{"engine", nil, 3, `"error":"usage"`, "status|start|stop", "커맨드 표 engine 행 — 필수 서브커맨드 부재"},
		{"engine", []string{"bogus"}, 3, `"error":"usage"`, "status|start|stop", "커맨드 표 engine 행 — 미인식 서브커맨드 흡수 금지"},
		{"seed", []string{"--bogus"}, 3, `"error":"usage"`, "unknown flag: --bogus", "커맨드 표 seed 행 — 미인식 플래그 흡수 금지"},
		{"update", []string{"--bogus"}, 3, `"error":"usage"`, "unknown flag: --bogus", "커맨드 표 update 행 — 미인식 플래그 흡수 금지"},
		{"teardown", []string{"--bogus"}, 3, `"error":"usage"`, "unknown flag: --bogus", "커맨드 표 teardown 행 — 미인식 플래그 흡수 금지"},
	}
}

func surfaceFinding(p surfaceProbe, got int, out string) structuredFailFinding {
	_, callerFile, line, _ := runtime.Caller(1)
	return structuredFailFinding{
		Check: "TestSurfaceContractDepth",
		Exit:  3,
		File:  callerFile,
		Line:  line,
		Fix:   "대표 표면 핸들러 exit·플래그 정합 — lib/cli·lib/wire",
		Summary: []string{
			fmt.Sprintf("flint %s %v → exit %d (want %d)", p.name, p.args, got, p.wantExit),
			fmt.Sprintf("출력: %s", strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])),
			p.note,
		},
	}
}

func TestSurfaceContractDepth(t *testing.T) {
	table := readmeTableCommands(t, "../README.md")
	for _, p := range surfaceProbes() {
		if !table[p.name] {
			t.Errorf("%s: 프로브 커맨드가 README 커맨드 표에 없음 — 표×동작 정합 붕괴", p.name)
			continue
		}
		out, code := runFlintSurface(t, append([]string{p.name}, p.args...)...)
		if code != p.wantExit {
			t.Errorf("표면 계약 불일치\n%s", formatStructuredFailure(surfaceFinding(p, code, out)))
			continue
		}
		if !strings.Contains(out, p.wantKey) {
			t.Errorf("%s %v: 에러 키 %s 없음 — 출력 %q", p.name, p.args, p.wantKey, out)
		}
		if !strings.Contains(out, p.fixHint) {
			t.Errorf("%s %v: 수정처 힌트 %q 없음 — 출력 %q", p.name, p.args, p.fixHint, out)
		}
	}

	exitKinds := []struct {
		args     []string
		wantExit int
		note     string
	}{
		{[]string{"help"}, 0, "exit 0 — 성공"},
		{nil, 1, "exit 1 — 인자 없음 사용법"},
		{[]string{"doctor", "--json"}, 2, "exit 2 — server_down(격리 루프백 refused DSN)"},
		{[]string{"seed"}, 3, "exit 3 — 자가수정 usage"},
	}
	for _, k := range exitKinds {
		out, code := runFlintSurface(t, k.args...)
		if code != k.wantExit {
			t.Errorf("exit 4종 계약 불일치(%s): exit=%d want=%d — 출력 %q", k.note, code, k.wantExit, out)
		}
	}
	out, _ := runFlintSurface(t, "doctor", "--json")
	if !strings.Contains(out, `"error":"server_down"`) {
		t.Errorf("exit 2 경로 에러 키 server_down 없음 — 출력 %q", out)
	}
}
