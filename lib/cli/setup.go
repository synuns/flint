package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

var composeRunFn = func(ctx context.Context, args ...string) error {
	argv := append([]string{"compose"}, args...)
	out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

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

var setupPortDialFn = func(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

var setupStderr io.Writer = os.Stderr

func SetupSteps() []string {
	return []string{
		"docker compose up -d --wait --wait-timeout 60",
		"migrate",
		"seed (flint seed --demo)",
		"install plugin (flint-plugin/)",
	}
}

var setupNext = []string{
	"flint seed --demo — seed demo data into the isolated demo database",
	"install the flint-plugin/ directory into your plugin path",
}

type setupSummary struct {
	Status   string   `json:"status"`
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings,omitempty"`
	Next     []string `json:"next"`
}

func RunSetup(ctx context.Context, ping, migrate func() error) (string, int) {
	return runSetupSteps(ctx, ping, migrate, nil)
}

func runSetupSteps(ctx context.Context, ping, migrate func() error, ux *setupUX) (string, int) {
	var warns []string
	status := "ok"

	if ux != nil {
		ux.Step("Start Doltgres (docker compose up)")
	}

	if setupPortDialFn("localhost:5434") {
		fmt.Fprintf(setupStderr, "flint setup: port 5434 already in use — compose may fail or connect to an existing server; continuing\n")
	}
	if err := composeUpFn(ctx); err != nil {

		if errors.Is(err, exec.ErrNotFound) {
			warns = append(warns, "docker not installed — install from https://docs.docker.com/get-docker/ and re-run")
		} else {
			warns = append(warns, "docker compose up failed — start docker and re-run: "+err.Error())
		}
		if ux != nil {
			ux.Fail(err)
		}
	} else if ux != nil {
		ux.Success("up --wait complete")
	}
	if ux != nil {
		ux.Step("Check DB connection (ping)")
	}
	if err := ping(); err != nil {
		status = "degraded"
		warns = append(warns, "DB ping failed — run make up then flint setup again")
		if ux != nil {
			ux.Fail(err)
		}
	} else {
		if ux != nil {
			ux.Success("connected")
			ux.Step("Migration")
		}
		if err := migrate(); err != nil {
			status = "degraded"
			warns = append(warns, "migrate failed: "+err.Error())
			if ux != nil {
				ux.Fail(err)
			}
		} else if ux != nil {
			ux.Success("schema synced")
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

type exposureSummary struct {
	Status   string `json:"status"`
	Exposure string `json:"exposure"`
}

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

var setupWizardTTYFn = func(stdout io.Writer) bool {
	return newSetupUX(stdout, false).isTTY
}

var setupWizardFn = func() (string, bool) {
	answers, err := tui.RunSetupWizard()
	if err != nil {
		return "", false
	}
	return answers.Exposure, true
}

func runSetup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--dry"},
			{Name: "--json"},
			{Name: "--update-rules"},
			{Name: "--mcp"},
			{Name: "--src", Value: true},
			{Name: "--exposure", Value: true},
		},
		Usage: "usage: flint setup [--dry] [--json] [--update-rules] [--src PATH] [--exposure normal|minimal] [--mcp]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, errLine(perr))
		return 3
	}
	updateRules := res.Given("--update-rules")
	jsonMode := res.Given("--json")
	srcOverride, exposure := res.Value("--src"), res.Value("--exposure")
	if res.Given("--src") && !res.HasValue("--src") {
		fmt.Fprintln(stdout, errLine(errors.New("setup: --src requires a path")))
		return 3
	}
	if res.Given("--exposure") && !res.HasValue("--exposure") {
		fmt.Fprintln(stdout, errLine(errors.New("setup: --exposure requires normal|minimal")))
		return 3
	}
	dryIdx, mcpIdx := -1, -1
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--dry":
			dryIdx = o.Index
		case "--mcp":
			mcpIdx = o.Index
		}
	}
	if dryIdx >= 0 && (mcpIdx < 0 || dryIdx < mcpIdx) {
		b, _ := json.Marshal(map[string]any{"status": "dry", "steps": SetupSteps()})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if mcpIdx >= 0 {
		return runSetupMCP(stdout)
	}
	if exposure != "" {
		return runSetupExposure(ctx, st, exposure, stdout)
	}
	if updateRules {
		return runUpdateRules(stdout, srcOverride)
	}

	if len(args) == 0 && setupWizardTTYFn(stdout) {
		if chosen, ok := setupWizardFn(); ok {
			if code := runSetupChain(ctx, st, stdout, false); code != 0 {
				return code
			}
			return runSetupExposure(ctx, st, chosen, stdout)
		}
	}
	return runSetupChain(ctx, st, stdout, jsonMode)
}

func setupPingMigrate(ctx context.Context, st Store) (func() error, func() error) {
	ping := func() error {
		if st == nil {
			return errors.New("store unavailable (first run — retry after compose)")
		}
		return st.Ping(ctx)
	}
	migrate := func() error {
		if st == nil {
			return errors.New("store unavailable (first run — retry after compose)")
		}
		return st.Migrate(ctx)
	}
	return ping, migrate
}

func runSetupChain(ctx context.Context, st Store, stdout io.Writer, jsonMode bool) int {
	ping, migrate := setupPingMigrate(ctx, st)
	if jsonMode {
		line, code := RunSetup(ctx, ping, migrate)
		fmt.Fprintln(stdout, line)
		return code
	}

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

var lookPathFn = exec.LookPath

var execCommandFn = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

type mcpSetupSummary struct {
	Status     string   `json:"status"`
	Registered []string `json:"registered"`
	Skipped    []string `json:"skipped"`
	Detail     []string `json:"detail,omitempty"`
}

func runSetupMCP(stdout io.Writer) int {
	registered := []string{}
	skipped := []string{}
	var detail []string
	for _, h := range harnessRegistry {
		if h.MCPAdd == nil {
			continue
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

type teardownSummary struct {
	Status   string   `json:"status"`
	Purge    bool     `json:"purge"`
	Warnings []string `json:"warnings,omitempty"`
	Next     string   `json:"next"`
}

func runTeardown(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	purge, confirm := false, false
	for _, a := range args {
		switch a {
		case "--purge":
			purge = true
		case "--yes":
			confirm = true
		default:
			fmt.Fprintln(stdout, errLine(wire.UsageErrorf("unknown flag: %s — usage: flint teardown [--purge] [--yes]", a)))
			return 3
		}
	}

	manual := "docker compose down"
	if purge {
		manual += " -v"
	}
	if !confirm {
		b, err := json.Marshal(teardownSummary{
			Status: "dry_run",
			Purge:  purge,
			Next:   "would run: " + manual + " — re-run with --yes to execute",
		})
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	status := "ok"
	var warns []string
	if err := composeDownFn(ctx, purge); err != nil {
		status = "degraded"
		warns = append(warns, "stop failed — run manually: "+manual+": "+err.Error())
	}

	b, err := json.Marshal(teardownSummary{
		Status:   status,
		Purge:    purge,
		Warnings: warns,
		Next:     "to stop the containers: " + manual,
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
