// setup/teardown commands — one-command setup (the QUICKSTART single command).
// setup: (1) docker compose up -d --wait (exec; on failure, continue with guidance) (2) Migrate (3) seed guidance
// (4) plugin install guidance — a one-line JSON progress summary. teardown: dry-run by default
// (only a compose down plan notice, destructive guard) — with --yes the execution is confirmed:
// stop the containers; with --purge, also delete the volumes via exec. Even on failure, the next-step
// guidance is kept.
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

// composeRunFn — docker compose execution seam (var). On failure, the stderr is attached to the error
// so the cause shows in the guidance output (no silent failures). Tests swap in a fake.
var composeRunFn = func(ctx context.Context, args ...string) error {
	argv := append([]string{"compose"}, args...)
	out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeUpFn and composeDownFn — phase seams. up waits with --wait until the containers are
// ready (spec §2, 60s timeout).
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

// setupPortDialFn — seam detecting port 5434 occupancy before compose starts. true =
// something is already listening (occupied). Default is a short-timeout net.Dial.
var setupPortDialFn = func(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// setupStderr — setup warning stderr channel seam (test capture).
var setupStderr io.Writer = os.Stderr

// SetupSteps — the fixed setup step order ((1)compose (2)migrate (3)seed (4)plugins).
func SetupSteps() []string {
	return []string{
		"docker compose up -d --wait --wait-timeout 60",
		"migrate",
		"seed (make seed)",
		"install plugin (flint-plugin/)",
	}
}

// setupNext — (3)(4) guidance. Not execution — guidance for the next manual step (non-interference principle).
var setupNext = []string{
	"make seed — seed demo data",
	"install the flint-plugin/ directory into your plugin path",
}

// setupSummary — the one-line JSON progress summary shape.
type setupSummary struct {
	Status   string   `json:"status"`
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings,omitempty"`
	Next     []string `json:"next"`
}

// RunSetup — runs steps (1)-(4) ((3)(4) guidance only) and returns the summary JSON and exit code.
// compose failure is a warning only (continue if ping succeeds); ping/migrate failure means
// degraded + manual-retry guidance with exit 1.
func RunSetup(ctx context.Context, ping, migrate func() error) (string, int) {
	return runSetupSteps(ctx, ping, migrate, nil)
}

// runSetupSteps — the RunSetup body. If ux is non-nil, each step's progress is shown via
// setupUX (the summary JSON, warnings, and exit contract are identical regardless of ux).
func runSetupSteps(ctx context.Context, ping, migrate func() error, ux *setupUX) (string, int) {
	var warns []string
	status := "ok"

	if ux != nil {
		ux.Step("Start Doltgres (docker compose up)")
	}
	// detect port 5434 (the ADR-0010 default port) occupancy before compose starts —
	// if an existing server (another container, a local postgres) already holds it, compose up
	// fails or ping connects to the wrong server. Only a one-line stderr warning; proceed anyway
	// (ping and migrate determine the actual state).
	if setupPortDialFn("localhost:5434") {
		fmt.Fprintf(setupStderr, "flint setup: port 5434 already in use — compose may fail or connect to an existing server; continuing\n")
	}
	if err := composeUpFn(ctx); err != nil {
		// docker not installed (exec not found) gets its own dedicated guidance.
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

// exposureSummary — the one-line JSON shape of the setup --exposure write report.
type exposureSummary struct {
	Status   string `json:"status"`
	Exposure string `json:"exposure"`
}

// runSetupExposure — only records --exposure normal|minimal and returns immediately
// (not part of the compose/migrate chain — a standalone mode like --update-rules).
// setup is a store-free entry, so it connects here only if st==nil. Invalid values are
// invalid-arg exit 3 (global contract); write failure is exit 1.
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
	// the exposure-write upsert is delegated to the single setMetaKV entry point.
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

// setupWizardTTYFn — terminal-check seam for wizard entry. Same criterion as the existing
// setupUX TTY detection (the command's stdout writer) — pipes, redirection,
// and CI do not enter the wizard (the existing guidance-path contract is kept).
var setupWizardTTYFn = func(stdout io.Writer) bool {
	return newSetupUX(stdout, false).isTTY
}

// setupWizardFn — wizard execution seam (test swap). (chosen, true) means the wizard
// completed — chosen is the flag equivalent (the --exposure value); ("", false) means cancelled or
// failed, falling back to the existing no-arg path (no side effects).
var setupWizardFn = func() (string, bool) {
	answers, err := tui.RunSetupWizard()
	if err != nil {
		return "", false
	}
	return answers.Exposure, true
}

// runSetup — command entry point. --dry prints the steps only (no exec, no queries).
// --json keeps the existing one-line JSON summary fully unchanged; when unspecified and on a TTY,
// output switches to the setupUX visual progress (identical behavior contract — same ping/migrate
// order and count). non-TTY (redirection, scripts) keeps the existing JSON as-is.
// --update-rules only syncs the skills and returns immediately — not part of the
// compose/migrate chain. --exposure also only records the profile; --mcp only registers
// the harness MCP and returns immediately.
// A TTY run with no arguments enters the huh wizard (assembles the flag equivalent
// and re-invokes the existing function — the flag path and non-terminal path are unchanged).
// As a store-free dispatch, st may be nil
// (first run) — ping/migrate are deferred as a degraded warning, and step-1 compose is still
// performed.
func runSetup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// moved the manual loop to the wire.ParseArgs spec table (positional args
	// not allowed). For the --dry/--mcp early returns given in parallel, args order wins (carrying over the
	// existing encounter order — reproduced by index comparison).
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
	// no args + terminal goes to the huh wizard — exposure selection ->
	// backup-key guidance, then the existing chain and the flag-equivalent write run through the existing functions
	// (the wizard only assembles values). Cancel and non-terminal keep the existing no-arg path unchanged.
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

// setupPingMigrate — the st-nil-guarded ping/migrate closure (extracted from the existing runSetup body
// — the wizard path reuses the same closure).
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

// runSetupChain — runs the main setup chain (extracted from the tail of the existing runSetup; contract
// unchanged). --json and non-TTY use the existing one-line JSON summary; TTY uses setupUX visual progress.
func runSetupChain(ctx context.Context, st Store, stdout io.Writer, jsonMode bool) int {
	ping, migrate := setupPingMigrate(ctx, st)
	if jsonMode {
		line, code := RunSetup(ctx, ping, migrate)
		fmt.Fprintln(stdout, line)
		return code
	}
	// when unspecified, visual progress — but only on a TTY (otherwise keep the existing one-line
	// JSON: script/pipe consumer compatibility).
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

// lookPathFn — harness CLI detection seam (exec.LookPath). Declared in the production file
// (a _test.go-only var breaks go build) — tests only swap it.
var lookPathFn = exec.LookPath

// execCommandFn — harness native CLI execution seam (var). The name argument is the
// absolute path returned by lookPathFn (a fake path fails instantly with ENOENT —
// so tests never touch real harness settings). Failure is non-fatal.
var execCommandFn = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// mcpSetupSummary — the one-line JSON shape of the setup --mcp result.
type mcpSetupSummary struct {
	Status     string   `json:"status"`
	Registered []string `json:"registered"`
	Skipped    []string `json:"skipped"`
	Detail     []string `json:"detail,omitempty"`
}

// runSetupMCP — iterates the tier-A harnesses (those with MCPAdd): detect via exec.LookPath, then
// register `flint mcp` with each native CLI (promoted from the copilot CLI
// validation — the guidance-only branch is gone; the argument list comes from the spec). Tier B (MCPAdd nil —
// file-config style) is not a setup target: excluded from the list (doctor pseudo-check + doc guidance).
// Not installed means skipped (harmless — exit 0). A registration run failure (already registered, etc.) is also
// non-fatal — the install itself was confirmed, so registered is kept with the reason in detail.
func runSetupMCP(stdout io.Writer) int {
	registered := []string{}
	skipped := []string{}
	var detail []string
	for _, h := range harnessRegistry {
		if h.MCPAdd == nil {
			continue // tier B — file-config style
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

// teardownSummary — the one-line JSON teardown summary shape.
type teardownSummary struct {
	Status   string   `json:"status"`
	Purge    bool     `json:"purge"`
	Warnings []string `json:"warnings,omitempty"`
	Next     string   `json:"next"`
}

// runTeardown — container-stop guidance + exec. Destructive guard — the default is a
// dry-run (a JSON notice of what would go down and the compose down plan, exit 0); only --yes
// executes (carrying over the confirm-to-run convention of maintain --invalidate --ids —
// preventing the incident where a no-arg call immediately ran compose down and stopped the production DB).
// With --purge, volumes are deleted (down -v). Failure is a warning + manual-command guidance, exit 1.
// unknown flags are invalid-arg exit 3.
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
