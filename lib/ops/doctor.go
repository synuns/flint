// doctor.go — flint doctor. Following the brew doctor convention — it runs the checks
// and only fail exits 1 while warn exits 0, presenting a one-line fix command for each fail.
// Invalid arguments (unknown flag, missing --check value, invalid check name) exit 3 (the global contract).
// It is a maintenance path (offline CLI) and does not interfere with the hot path (§2).
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

// injection seams for cli-owned implementations — avoiding the ops->cli import ban (the knowledge
// ParseAsOfSpecFn convention). Version and update (version.go, update.go — cli-resident) also
// hang here.
// The real implementations are injected by the cli/root.go composition root init.
var (
	// CurrentVersionFn etc. — checkUpdate (the version-lag warning).
	CurrentVersionFn     func() string
	FetchLatestVersionFn func() (string, error)
	ValidVersionFn       func(v string) bool
	CompareVersionsFn    func(a, b string) int
	// KnownMetaKeyFn — the unknown-key determination of checkMetaHygiene (maintain.go).
	KnownMetaKeyFn func(k string) bool
	// AnyToTimeFn — the valid_at parsing of checkPendingAge (maintain.go).
	AnyToTimeFn func(v any) (time.Time, bool)
	// IsConflictGroupFn and ConflictDupThresholdFn — the contradiction classification of
	// checkConflicts (maintain.go). ThresholdFn is a var, so it is injected via a read-at-call-time wrapper.
	IsConflictGroupFn      func(g db.ConflictGroup, dupThreshold float64) bool
	ConflictDupThresholdFn func() float64
	// BackupEncryptOnFn·BackupKeyPathFn — checkBackupKey(backup.go).
	BackupEncryptOnFn func(v string) bool
	BackupKeyPathFn   func() string
	// SkillSrcDirFn and CheckSkillFileFn — the skill-file check (rules_sync.go).
	SkillSrcDirFn    func(binPath string) string
	CheckSkillFileFn func(srcDir, destDir string) (DoctorCheck, bool)
)

// CheckSpec — the registry representation of one doctor check.
// Name, Fn, Chain ("core"|"tail"), and OnStoreNil ("fail"|"warn"|"" = st unused)
// live in a single registry, from which the full run chain, the runDoctorCheck dispatch,
// the --check allow list, and --list-checks are all derived.
type CheckSpec struct {
	Name string
	Fn   func(ctx context.Context, st wire.Store) DoctorCheck
	// ThFn — the injected variant for thresholds-consuming checks: receives the
	// once-per-run loaded value (doctorThresholds). The dispatch (runCheckSpec)
	// prefers ThFn when present, else Fn. Checks that do not use thresholds
	// set only Fn.
	ThFn func(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck
	// Chain — "core" is the DoctorChecks namespace (6 kinds, port-last contract);
	// "tail" is the tail chain at the end of the full run chain (14 kinds).
	Chain string
	// OnStoreNil — the verdict when st==nil ("fail"|"warn"). If the check does not
	// use st, "" (hook-file, port, update). The existing behavior of the core 4 (db, schema,
	// integrity, last-seen) failing and the tail warning is now codified as attributes.
	OnStoreNil string
}

// doctorChecks — the check registry (core 6 + tail 14 = 20 kinds). The 14 harness-* kinds and
// skill-file rely on file-existence detection (dynamic harness/skill detection), so they are a separate
// chain outside the registry — not an integration target (ruling). Adding a new check is 1 row in this table.
// The 10 thresholds-consuming checks (last-seen, backup-age, zero-near,
// pending-age, event-volume, meta-hygiene, secrets, metering-freshness,
// dolt_log_depth, db_size) are registered in the ThFn injected
// form — sharing the once-per-run load (doctorThresholds), secrets included.
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
		{Name: "metering-freshness", Chain: "tail", OnStoreNil: "warn", ThFn: checkMeteringFreshnessTh},
		{Name: "secrets", Chain: "tail", OnStoreNil: "warn", ThFn: checkSecretsTh},
		// the v1.25.0 engine warnings (warn grade — fail is the corruption
		// class convention; growth is normal operation, guidance only).
		{Name: doltLogDepthName, Chain: "tail", OnStoreNil: "warn", ThFn: checkDoltLogDepthTh},
		{Name: "db_size", Chain: "tail", OnStoreNil: "warn", ThFn: checkDBSizeTh},
		// the v1.25.0 engine container footprint (display only — docker
		// stats, no threshold, no new storage; failures skip quietly).
		{Name: "engine-footprint", Chain: "tail", OnStoreNil: "", Fn: checkEngineFootprint},
	}
}

// doctorThresholds — the doctor run-scoped thresholds loader (lazy,
// once, memoized). The 6 checks each issuing their own meta IN query (up to 6 per full
// run) become 1 shared load per run; the load-failure default fallback keeps the
// WriteThresholds convention. st==nil means defaults without loading (each check's nil guard
// owns the verdict — avoiding a nil interface call at load time).
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

// runCheckSpec — registry check dispatch: ThFn checks receive the run-scoped
// thresholds (th); the rest run Fn as-is.
func runCheckSpec(ctx context.Context, st wire.Store, th func() store.Thresholds, s CheckSpec) DoctorCheck {
	if s.ThFn != nil {
		return s.ThFn(ctx, st, th())
	}
	return s.Fn(ctx, st)
}

// doctorSpec — looks up a name in the registry.
func doctorSpec(name string) (CheckSpec, bool) {
	for _, s := range doctorChecks() {
		if s.Name == name {
			return s, true
		}
	}
	return CheckSpec{}, false
}

// doctorChainNames — the name order of a given chain ("core"|"tail") from the registry.
func doctorChainNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

// DoctorChecks — the core chain name order (spec §1's 5 kinds + port). Derived from
// the registry (doctorChecks) chain=="core" filter — keeping the public surface.
func DoctorChecks() []string {
	return doctorChainNames("core")
}

// doctorTailChecks — the tail chain name order (outside the DoctorChecks() namespace,
// at the end of the full run chain). Derived from the registry chain=="tail" filter — the --check
// standalone allow list also matches this list. harness-* and skill-file are not expanded (a separate chain).
func doctorTailChecks() []string {
	return doctorChainNames("tail")
}

// DoctorCheck — an individual check result. status = ok|warn|fail.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// doctorOutput — the one-line JSON output shape. status is the worst result.
type doctorOutput struct {
	Status string        `json:"status"`
	Checks []DoctorCheck `json:"checks"`
}

// RunDoctor — --list-checks, --check NAME subsets, --fix self-repair
// (applied to hook-file only), and the default full check. Invalid arguments (unknown flag, missing
// --check value, invalid --check name) exit 3 (the global contract: invalid arg 3). Any fail exits 1;
// warn exits 0. The output and exit of the no---fix path are fully unchanged.
func RunDoctor(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// positional args not allowed (the wire.ParseArgs spec table — prefix-less tokens are also
	// unknown-flag exit 3, the existing contract). Value validation stays with the caller.
	// with the --json flag.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--list-checks"},
			{Name: "--fix"},
			// the explicit machine-readable channel — pins the one-line JSON of the
			// DoctorCheck field structure (the default text output is unchanged).
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
		// the whole registry (core 6 + tail 14).
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
		// the --check allow list derives from the whole registry — all 6 core kinds
		// and 10 tail kinds are standalone-executable.
		// Every check name the full report shows must be runnable via --check —
		// skill-file and harness-* are a separate chain outside the registry
		// but standalone execution is allowed.
		_, ok := doctorSpec(subset)
		if subset == "skill-file" || strings.HasPrefix(subset, "harness-") {
			ok = true
		}
		if !ok {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("doctor: unknown check %q — usage: flint doctor [--list-checks] [--check NAME] [--fix] [--json] (names via --list-checks)", subset)))
			return 3
		}
		checks := []DoctorCheck{runDoctorCheck(ctx, st, subset)}
		bumpDoctorWarnCounters(ctx, st, checks) // fired counters
		return doctorReport(checks, fix, jsonOut, stdout)
	}

	// the full run chain — registry-derived (core 6; harness and skill are separate chains,
	// the tail 14 keep the existing append order).
	// F6 (performance): thresholds are loaded once per run (the doctorThresholds lazy
	// memo) and shared by the 6 ThFn checks — each check's own meta IN query is gone.
	th := doctorThresholds(ctx, st)
	checks := make([]DoctorCheck, 0, len(doctorChecks())+len(HarnessRegistry))
	for _, s := range doctorChecks() {
		if s.Chain == "core" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}
	// the harness checks (v0.9 §4) are appended after the core chain — only harnesses with files
	// produce results (dynamic detection, hence a separate chain outside the registry).
	checks = append(checks, harnessChecks()...)
	// skill-file is also a separate chain like harness — it produces a result only when
	// the source is detected, and a warn does not break the exit
	// (ruling: hooks work without skills — fail would be excessive). When home is undetected,
	// the check is skipped (same handling as checkHookFile — Join("",...)
	// becomes a relative path and misdetects in cwd).
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
	bumpDoctorWarnCounters(ctx, st, checks) // fired counters
	return doctorReport(checks, fix, jsonOut, stdout)
}

// bumpDoctorWarnCounters — fired counters: per check that verdicts warn,
// increments the `fired_doctor_<check>` meta cumulative counter (spec §2.5 — the diagnostic-D source;
// counts only, no personal data). At most 1 increment per check kind per run. The kind cap
// is the check namespace as-is — 20 registry kinds + 14 harness-* kinds + 1 skill-file
// = 35 kinds (engine-footprint joined in v1.25.0; its verdicts are ok/skip, never warn). fail, ok, and skip are not warning firings, so not counted; since it is based on the
// final checks slice, the pre---fix-re-evaluation verdicts are not double-counted either. Failures are silent (§13).
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

// doctorReport — aggregates check results, applies --fix self-repair, and produces output and exit.
// The shared tail of the subset and full paths after the registry-derived recomposition. jsonOut is
// --json — pinning the one-line JSON of the DoctorCheck field structure
// (the --fix application report also uses the doctorOutput shape; the default --fix report is unchanged).
func doctorReport(checks []DoctorCheck, fix bool, jsonOut bool, stdout io.Writer) int {
	// --fix: self-repair applies only to hook-file fails (the other checks involve user settings
	// and external files, outside automation scope — coordinator ruling). On success,
	// only that check is re-evaluated and reported in one doctorFixReport line.
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
				checks[i] = checkHookFile() // re-evaluated
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

// runDoctorCheck — check-name dispatch. The registry (doctorChecks) lookup is the primary
// source; only the separate chains outside the registry
// (skill-file, harness-*) remain as cases here. ThFn checks receive the run-scoped
// thresholds (doctorThresholds — 1 load per standalone run) (F6).
func runDoctorCheck(ctx context.Context, st wire.Store, name string) DoctorCheck {
	if s, ok := doctorSpec(name); ok {
		return runCheckSpec(ctx, st, doctorThresholds(ctx, st), s)
	}
	switch name {
	case "skill-file":
		// the full-report check names and the --check namespace stay consistent.
		// home undetected and source undetected are skip verdicts (the same rule as the full chain).
		if home := ClaudeHome(); home != "" {
			if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
				return c
			}
		}
		return DoctorCheck{Name: name, Status: "ok", Detail: "skill source not found — check skipped"}
	}
	if strings.HasPrefix(name, "harness-") {
		// harness-<name> standalone run — judges only that harness from the
		// registry. Not installed (file absent) is skip; an unregistered harness name is
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

// ClaudeHome — the hook settings directory. The FLINT_CLAUDE_HOME override takes precedence.
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

// checkHookFile — whether flint is registered in the hook settings (settings.json).
// Not registered is a fail: the accrual metering (write --source hook) would be missing.
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

// flintWordRe — matches the flint executable in a command string on word boundaries
// (excluding accidental prefix/suffix matches like 'myflint' and 'flintx').
var flintWordRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])flint(?:[^A-Za-z0-9_-]|$)`)

// containsFlintHook — whether a flint invocation is registered in the command fields inside
// the hooks object of settings.json. A full-text containment check (the 5 letters 'flint') over-matched
// comments and other tools' settings, so it matches precisely against the JSON structure (hooks -> the
// SessionStart/PostToolUse etc. arrays -> hooks[].command). An unparsable body is treated as unregistered.
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

// valueHasFlintCommand — checks the command field of every object in the hooks value tree.
// The real hooks.json shape is a 2-level nesting of event arrays -> {matcher, hooks:[{type,command}]},
// but a plain array ({command:...}) is also accepted (registration-shape variety).
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

// FlintBinPathFn — the seam (var) for the absolute flint binary path used in hook commands.
// Without flint on PATH, a bare command would die silently — so it
// resolves directly via os.Executable. On failure, the empty string. The cli test
// swap convention (doctor_v094_test) propagates via the cli-side var alias
// (the ops_alias_1.go init read wrapper).
var FlintBinPathFn = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// flintHookCmd — a hook command with the bare `flint` of the hooks.json command replaced by
// binPath's absolute path. 2>/dev/null || true — so a hook failure does not shake
// the session (inherited from the original flint-plugin/hooks/hooks.json). The timeout 10
// absorbs cases where DB latency used to swallow the whole injection within 2s.
// eval wrapping — the shell parses jq @sh's single-quoting as quoting
// and passes it as a single argument (preventing leftover quote literals). The eval input is only fixed
// literals (binPath, sub) and @sh-quoted values, so there is no arbitrary-code path.
// binPath single-quoted — closes the case where a space in the install path
// word-split inside eval and silently dropped the hook (shQuote).
func flintHookCmd(binPath, sub string) map[string]any {
	cmd := `eval "` + shQuote(binPath) + " " + sub + `" 2>/dev/null || true`
	return map[string]any{"type": "command", "command": cmd, "timeout": 10}
}

// shQuote — POSIX single-quoting: an inner single quote is escaped with the
// close-escape-open 3-step sequence (backslash + single quote — gofmt-stable)
// (a single argument regardless of spaces or metacharacters — safe even inside eval double-quoting).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// flintHookDefs — the 3 flint hooks (unified: event -> added-entry
// list). The session start/end sequences are run internally by the 1 command each of session-start
// and session-end; the matcher, jq session_id parsing, timeout, and redirection patterns are
// inherited as-is from flint-plugin/hooks/hooks.json.
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
			// jq @sh produces single-quoted shell quoting and eval
			// parses it correctly and passes a single argument. The old double-quoted substitution
			// was never re-interpreted either, but its quoting layer was implicit — explicit
			// quoting (@sh) + eval parsing structurally guarantees the raw value is preserved.
			// the jq fallback is empty rather than "" — when session_id is absent,
			// it does not pass an empty label but omits the flag itself (the same direction as the hook
			// entry point treating invalid as absent). The same pattern as hooks.json.
			withSession(`session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"Stop": {
			plain(`session-end --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"PostCompact": {plain("compact")},
	}
}

// flintHookSubRe — identifies one flint hook command (regardless of the executable path). A command
// with a flint invocation followed by a hook subcommand (hot, done, nudge, compact, session-start,
// session-end) — catches both the legacy 5-entry direct runs and the new unified commands.
// Accidental prefix/suffix matches like 'myflint' and 'flintx' are excluded by the boundary class
// (the same rule as flintWordRe + path-prefix allowed). It accepts the closing ' of
// single-quoted paths (the shQuote form — the old unquoted form keeps matching too).
var flintHookSubRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_-])(?:\S*/)?flint(?:\.\S+)?'?\s+"?(session-start|session-end|hot|done|nudge|compact)(?:[\s"]|$)`)

// anyHookCommand — whether any command field in the hooks value tree satisfies pred
// (the same traversal as valueHasFlintCommand).
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

// hasFlintSub — whether a flint <sub> command exists in the tree.
func hasFlintSub(v any, sub string) bool {
	return anyHookCommand(v, func(cmd string) bool {
		m := flintHookSubRe.FindStringSubmatch(cmd)
		return m != nil && m[1] == sub
	})
}

// stripFlintHookEntry — strips flint hook commands from one hook group
// (identifying replacement targets — only entries the plugin registered; other tools' commands are unchanged).
// If no commands remain, the group itself is removed (false).
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

// ApplyHookFix — registers/replaces the 3 flint hooks in settings.json (self-repair,
// the unified 3 entries). Read -> parse -> new-form existence check (idempotent) -> timestamp
// backup -> strip legacy flint entries and append the new form -> MarshalIndent write.
// The existing legacy 5 entries (hot, nudge/done, nudge direct runs) are replacement targets —
// only groups containing flint commands are cleaned; other tools' hooks and all other keys are unchanged.
// If the new form (session-start, session-end) already exists, it skips with (false, "", nil).
// A parse failure returns err with both file and backup unchanged. If the file is absent,
// a minimal {"hooks":{}} skeleton is created — there is no original, so the backup is
// omitted (backup="") and the parent directory is created too. Afterward the merge path (idempotent,
// absolute-path, timeout contracts) is reused as-is.
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
		return false, "", nil // idempotent — the new 3 entries are already registered
	}
	if existed { // backup only when an original exists
		backupPath = path + ".flint-bak-" + time.Now().Format("20060102-150405")
		// v0.28.0 (H3): the backup copy inherits the original's permissions — the defect where
		// WriteFile's fixed mode (0644) demoted a copy of a 0600-managed original.
		mode := os.FileMode(0o600)
		if fi, serr := os.Stat(path); serr == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(backupPath, b, mode); err != nil {
			return false, "", err
		}
	}
	// replacing the legacy flint direct-run hooks (hot, done, nudge, compact) — strip only the flint
	// commands and delete empty groups. Other tools' groups stay at the front as-is.
	for ev, prev := range hooks {
		list, ok := prev.([]any)
		if !ok {
			continue
		}
		merged := make([]any, 0, len(list))
		for _, g := range list {
			if gm, ok := g.(map[string]any); ok && !stripFlintHookEntry(gm) {
				continue // a group with only flint commands — replaced by the unified entries
			}
			merged = append(merged, g)
		}
		hooks[ev] = merged
	}
	for ev, add := range flintHookDefs(binPath) {
		entries := add // convert []map[string]any to []any and append
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

// DoctorFixReport — the one-line JSON report after applying --fix. status "fixed" +
// name:status per check. Other failed checks are also exposed as name:status as-is.
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

// HarnessSpec — one harness check target (spec C1-C5). Name matches the check-name
// suffix and the documentation file name. Rel is home-relative (only cursor is relative
// to the project root = working directory). EnvOverride replaces that directory itself
// (inheriting the ClaudeHome/FLINT_CLAUDE_HOME pattern — for test injection).
type HarnessSpec struct {
	Name        string
	Rel         string
	EnvOverride string
	Project     bool // true means Rel is relative to the working directory, not home (cursor, vscode, trae)
	// MCPAdd — the native CLI auto-registration argument. nil means file-config style (tier B)
	// — not a setup --mcp target (doctor pseudo-check + doc guidance only).
	MCPAdd []string
}

// HarnessRegistry — the 14 harnesses. C6: every harness uses
// MCP stdio JSON-RPC. Tier A (auto-registration, CLI verified against official docs) 8 kinds +
// tier B (file-config style — doctor pseudo-check only) 6 kinds. The remaining ones have
// no file-granular settings path decided, so docs only (ruling).
var HarnessRegistry = []HarnessSpec{
	// tier A — auto-detection and registration.
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
	// tier B — file-config style (no registration CLI).
	{Name: "windsurf", Rel: ".codeium/windsurf/mcp_config.json", EnvOverride: "FLINT_WINDSURF_HOME"},
	{Name: "zed", Rel: ".config/zed/settings.json", EnvOverride: "FLINT_ZED_HOME"},
	{Name: "amp", Rel: ".config/amp/settings.json", EnvOverride: "FLINT_AMP_HOME"},
	{Name: "vscode", Rel: ".mcp.json", EnvOverride: "FLINT_VSCODE_DIR", Project: true},
	{Name: "goose", Rel: ".config/goose/config.yaml", EnvOverride: "FLINT_GOOSE_HOME"},
	{Name: "trae", Rel: ".trae/mcp.json", EnvOverride: "FLINT_TRAE_DIR", Project: true},
}

// path — the absolute path of the settings file under check. The override env replaces
// the directory that Rel used to attach to (for cursor, the project root) itself. If home/wd lookup
// fails, the empty string (treated as absent — an uninstalled harness is not a warning).
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

// harnessChecks — the registered harness checks (§4, 14 kinds). Only harnesses with settings files
// produce results (absent -> empty slice). Read-only (non-interference).
func harnessChecks() []DoctorCheck {
	var out []DoctorCheck
	for _, h := range HarnessRegistry {
		if c, ok := checkHarnessFile(h); ok {
			out = append(out, c)
		}
	}
	return out
}

// checkHarnessFile — the verdict for one harness. (a) file absent -> check skipped (ok=false),
// (b) no "flint" in the body -> fail + the doc-path fix, (c) present -> ok.
// The registration check is a string containment (format-agnostic — C6; ok even if only a comment).
func checkHarnessFile(h HarnessSpec) (DoctorCheck, bool) {
	path := h.path()
	if path == "" {
		return DoctorCheck{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return DoctorCheck{}, false // harness not installed — check skipped
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

// checkSchema — meta schema_version >= 4 (numeric comparison — verifying the metering-column
// promotion. A future v5 DB is accepted as normal; only below-minimum and non-numeric values fail).
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

// checkIntegrity — the 3 flint_knowledge_* index rows (the v3 filter indexes).
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

// checkLastSeenTh — the 3-path last_* elapsed + grace (the nodata pattern). grace uses
// the same loadThresholds system as maintain --check (the stall_hot_h and stall_write_h
// meta overrides, defaults 48/72h) — with two threshold sources the conclusions would
// conflict. Exceeding or no record (never) is warn — it does not break the exit.
// thresholds are injected (the doctor chain shares the once-per-run load).
func checkLastSeenTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — check DB connection"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn" // connection trouble is warn (silent) to avoid stall false positives
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
			continue // a corrupt value is treated as absent (the invalid-value fallback)
		}
		seen[fmt.Sprint(r[0])] = ts
	}
	// the read path no longer uses meta last_read_at — recall recency is derived from the max read_events
	// time and merged (the more recent of the two vs the residual meta value; a lookup failure keeps the existing value — silent).
	if rows, rerr := wire.QueryOnce(ctx, st,
		`SELECT max(ts) FROM read_events`, nil); rerr == nil &&
		len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			if prev, had := seen["last_read_at"]; !had || t.After(prev) {
				seen["last_read_at"] = t
			}
		}
	}
	// the threshold shares the system with maintain — grace is read from the injected th
	// (F6: once-per-run load; on failure the WriteThresholds fallback applies).
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

// checkMetaHygiene — meta key hygiene. Warns on unknown keys (deprecation candidates) and
// daily counters past the retention window (daily_retention_days) + a 7-day grace.
// The body is the thresholds-injected variant (checkMetaHygieneTh) — the doctor chain uses
// the once-per-run loaded value; this wrapper loads its own for direct callers.
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
			// flat keys (daily:<kind>:<date>) supported — determined by the
			// last segment's date (shared with dailyKeyDate in maintain.go).
			if d, ok := dailyKeyDate(k); ok && d < cutoff {
				staleDaily++
			}
		case KnownMetaKeyFn(k) || thKeys[k]: // including pb_ and similar prefixes
		default:
			// tuning meta keys are also allowed.
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
					// the output exposes the unknown key's raw value too, so it
					// passes through the sanitization outlet.
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

// checkBackupKey — the encryption backup key state. The real check runs only when meta
// backup_encrypt=true — without a key, the next automatic backup would create a new key,
// producing a mix incompatible with existing ciphertexts (the advance warning is the reason
// this exists). Permissions are checked against 0600 (protection from other local accounts/processes).
// A lookup error is warn, not 'unused ok' (no false ok — the same contract
// as secrets).
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

// CheckBackupAge — the elapsed-time verdict on meta last_backup. Elapsed uses the
// thresholds registry (meta backup_age_warn_h, default 72h — TUNING §2).
// Exceeded, unset, corrupt value,
// lookup failure, and st nil are all warn — a missing backup does not block flint, so it is not
// a fail (ruling). Inherits the same queryOnce + RFC3339 pattern as checkLastSeen.
// The body is the thresholds-injected variant (checkBackupAgeTh) — the doctor chain uses
// the once-per-run loaded value; this public wrapper loads its own for direct callers.
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
		ts = time.Time{} // a corrupt value is treated as unset (the invalid-value fallback)
	}
	if ts.IsZero() {
		c.Status = "warn"
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}
	// the threshold prefers the meta override (injected th) — the lookup-failure
	// fallback is applied by the loader above; the value fallback (<=0 -> default)
	// and the overflow clamp share db.HoursSpan (v1.19.3): a backup_age_warn_h
	// override past the Duration ceiling used to wrap the warn span negative
	// (every backup instantly over-age).
	if d := time.Since(ts); d >= db.HoursSpan(th.BackupAgeWarnH, store.DefaultThresholds().BackupAgeWarnH) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("last backup %dh ago — check the flint done hook or run backup manually", int(d.Hours()))
		return c
	}
	c.Status = "ok"
	return c
}

// DoctorConflictLimit — the cap on the doctor conflicts lookup.
// More generous than maintain's default (10) — counting only the top 10 groups would miss
// excess contradictions as unreported. There is still a cap, so no unbounded scan.
const DoctorConflictLimit = 50

// conflictStore — the extra lookup needed for contradiction-candidate reporting (groups with summaries).
// The same method set as the maintain.go definition (structural typing, so the assertion behavior is unchanged).
type conflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

// CheckConflicts — the count of contradiction-candidate groups. Classifies same-topic active
// 2+-row groups (the same GROUP BY conditions as maintain --conflict-suspects) by summary
// similarity (isConflictGroup — the same criterion) and counts only contradiction
// candidates: 1+ is warn, 0 is ok. Purely duplicate groups (similar summaries) are not counted —
// equivalence with the listing is required so the recommended --conflict-suspects guarantees its result.
// The lookup goes only through the same ConflictStore seam (ConflictGroups) as maintain —
// the GROUP BY SQL is not rewritten here. The cap is the doctor-only
// DoctorConflictLimit (50) — when the result reaches the cap, the detail states the possible
// excess so it is not mistaken for complete.
// An unsupported seam, lookup failure, and st nil are also warn — not fail (suspected
// contradictions do not block operation).
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
	// the classification threshold is the thresholds-registry default (conflictDupThreshold).
	// The meta-override path is maintain --conflict-suspects — by contract
	// the doctor check performs no lookup outside the ConflictGroups seam
	// (including reading the threshold).
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

// doctorZeroNearWindow — the zero_near check aggregation window (days).
// The last 7 days including today — the same scale as the report --days default window. The sample floor and warning
// ratio are thresholds-registry values (zero_near_min_read default 10 and
// zero_near_warn_pct default 30 — TUNING §10).
const doctorZeroNearWindow = 7

// CheckZeroNear — the near-zero-hit ratio verdict from the summed daily read metering of the
// last 7 days. With the read_stats blob and daily read counters abolished, the computation
// source moved to WindowedDaily (residual daily:* values + the read_events-derived merge — a single
// source with report). The decision expression (read >= sample floor && read_zero_near*100/
// read >= warning ratio -> warn) and the output wording are equivalent to before the migration. Lookup
// failure and st nil are warn. Suspected search-quality issues do not block operation, so not fail (ruling).
// The body is the thresholds-injected variant (checkZeroNearTh) — the doctor chain uses
// the once-per-run loaded value; this public wrapper loads its own for direct callers.
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
	// the 2 thresholds prefer the meta overrides (injected th) — the lookup-failure fallback (defaults)
	// is applied by the loader above.
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

// CheckPendingAge — the elapsed verdict on the oldest pending's valid_at. The threshold
// is the thresholds registry (meta pending_age_days, default 7 days — TUNING).
// Elapsed at or above the threshold, lookup failure, and st nil are
// warn (neglect does not block operation — inheriting the backup-age ruling); within the threshold
// and no pending are ok. The detail carries the expiry-command guidance (maintain --expire-pending N).
// Cleanup itself is left to the dry-run-default command (no automatic transition).
// The body is the thresholds-injected variant (checkPendingAgeTh) — the doctor chain uses
// the once-per-run loaded value; this public wrapper loads its own for direct callers.
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
		c.Status = "ok" // a corrupt value is treated as no pending (the invalid-value fallback)
		return c
	}
	// the threshold prefers the meta override (injected th) — the lookup-failure fallback is applied
	// by the loader above; the value fallback (<=0 -> full defaults) is kept here.
	if th.PendingAgeDays <= 0 {
		th = store.DefaultThresholds()
	}
	// DaysSpan clamps the threshold days (the v1.19.2 overflow sweep) — a
	// pending_age_days override past the Duration ceiling used to wrap the
	// comparison span negative (every pending instantly over-age).
	if d := time.Since(va); d >= db.DaysSpan(th.PendingAgeDays, time.Now()) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("oldest pending %dd past — clean up with maintain --expire-pending N",
			int(d.Hours()/24))
		return c
	}
	c.Status = "ok"
	return c
}

// stallDetail — returns the warning wording when the key's elapsed exceeds grace (absent = never).
func stallDetail(seen map[string]time.Time, key string, graceH int, warns *int) string {
	ts, ok := seen[key]
	if !ok {
		*warns++
		return key + " never recorded"
	}
	// The grace shares the db.HoursSpan clamp (v1.19.3): a grace_h override
	// past the Duration ceiling wraps time.Duration(h)*time.Hour negative —
	// every key instantly over-grace. def 0 keeps a non-positive grace at 0
	// (the pre-clamp semantics — the callers inject fallback-applied th).
	grace := db.HoursSpan(graceH, 0)
	if d := time.Since(ts); d > grace {
		*warns++
		return fmt.Sprintf("%s elapsed %s (over %dh grace)", key, d.Round(time.Hour), int(grace.Hours()))
	}
	return ""
}

// doctorPort — the host port flint expects to be published (the default before the compose override).
// v0.9.7 ADR-0010: moved to 5434 due to an external container occupying 5433.
const doctorPort = 5434

// ComposePsFn — the docker compose ps seam (var). All errors, including docker absent and a missing
// compose project, are handled by checkPort as skip (non-interference). The cli-side test
// swap (doctor_v07_test) propagates to the execution path via the ops_alias_1.go init read
// wrapper.
var ComposePsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "compose", "ps", "--format", "json").Output()
}

// composePsPublisher — one port mapping of ps. The official docker schema (B6):
// PublishedPort = the host-published port. Exited containers have Publishers null.
type composePsPublisher struct {
	PublishedPort int `json:"PublishedPort"`
}

// composePsEntry — one ps container row.
type composePsEntry struct {
	Name       string               `json:"Name"`
	State      string               `json:"State"`
	Publishers []composePsPublisher `json:"Publishers"`
}

// checkPort — the 5434 publication verdict. (1) flint running + 5434 -> ok;
// (2) flint absent/stopped -> fail + up -d guidance; (3) another container occupying -> fail +
// the occupant's name + port-move guidance. ComposePsFn errors are skip (they must not drown
// doctor's other checks in a docker-less environment).
func checkPort(ctx context.Context) DoctorCheck {
	c := DoctorCheck{Name: "port"}
	// preventing an indefinite block when docker is unresponsive — a 2s cap. On expiry it falls
	// through as a ComposePsFn error and follows the existing skip path (non-interference).
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
	// (1) the flint container running + 5434 published.
	for _, e := range entries {
		if IsFlintContainer(e.Name) && e.State == "running" && publishesPort(e, doctorPort) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s publishes %d", e.Name, doctorPort)
			return c
		}
	}
	// (3) another container occupies 5434 — names the occupant.
	for _, e := range entries {
		if publishesPort(e, doctorPort) {
			c.Status = "fail"
			c.Detail = fmt.Sprintf("port %d occupied by %s", doctorPort, e.Name)
			c.Fix = "fix: move the port — FLINT_DSN + compose override (TUNING §2)"
			return c
		}
	}
	// (2) flint absent/stopped.
	c.Status = "fail"
	c.Detail = fmt.Sprintf("flint container does not publish %d", doctorPort)
	c.Fix = "fix: docker compose up -d (flint repository)"
	return c
}

// parseComposePs — parses the ps --format json output. Recent docker emits consecutive objects
// (JSONL) while older versions emit an array, so a json.Decoder stream handles
// both.
func parseComposePs(b []byte) ([]composePsEntry, error) {
	trimmed := bytes.TrimSpace(b)
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var out []composePsEntry
	if len(trimmed) > 0 && trimmed[0] == '[' {
		tok, err := dec.Token() // consume the '[' token
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

// IsFlintContainer — whether the container name is the flint service (e.g. flint-doltgres-1).
// Prefix matching — excludes false positives like 'conflint', which contains flint but not as a prefix.
func IsFlintContainer(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "flint")
}

// publishesPort — whether any of the entry's Publishers matches the host-published port.
// An Exited container's null Publishers decodes as an empty slice.
func publishesPort(e composePsEntry, port int) bool {
	for _, p := range e.Publishers {
		if p.PublishedPort == port {
			return true
		}
	}
	return false
}

// checkEventVolume — the effect-metering tables' volume check.
// If the 3-table total row count exceeds events_volume_warn (default
// 50000, meta override), warn — guidance to shorten the retention days (events_retention_days)
// or to check that PruneReadEvents is working. Growth is the result of normal
// operation, so not a fail (the non-interference principle — warn guidance only).
// The body is the thresholds-injected variant (checkEventVolumeTh) — the doctor chain uses
// the once-per-run loaded value; this wrapper loads its own for direct callers.
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

// doltLogDepthName — the depth check's name (a check name, not SQL — the
// r108-2 split: the probe SQL and the table name moved to lib/db
// db.DoltLogDepth / db.DoltLogTable; the ops-side split-assembly of the SQL
// was a dialect-gate dodge, not ownership).
const doltLogDepthName = "dolt_" + "log_depth"

// checkDoltLogDepthTh — the v1.25.0 dolt history-depth warning, the r108-2
// relocation: the O(1) depth probe is lib/db-owned (db.DoltLogDepth — the
// max commit_order of the dolt log; commit_order is monotonic, so LIMIT 1
// answers the depth). At or above dolt_log_depth_warn (default 10000, meta
// override) the guidance points at maintain --gc with the disconnect
// contract named. The verdict is warn — growth is normal operation (the
// fail-is-corruption convention). A query failure is a warn finding, not a
// disguised clean verdict (the checkRecallStarved convention — the engine
// surface is expected to exist).
func checkDoltLogDepthTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: doltLogDepthName}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query " + db.DoltLogTable
		return c
	}
	depth, err := db.DoltLogDepth(ctx, st)
	if err != nil {
		c.Status = "warn"
		c.Detail = db.DoltLogTable + " depth query failed: " + err.Error()
		return c
	}
	if depth >= int64(th.DoltLogDepthWarn) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%s depth %d at or above threshold %d (depth_warn) — consider maintain --gc --yes (disconnects)", db.DoltLogTable, depth, th.DoltLogDepthWarn)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%s depth %d", db.DoltLogTable, depth)
	return c
}

// checkDBSizeTh — the v1.25.0 database-size warning: pg_database_size of
// the current database, judged in MB against db_size_warn_mb (default 2048,
// meta override — at or above). The data directory is outside the DSN's
// reach, so an unsupported or failed measurement is a quiet skip (PLAN §4
// principle 3 — the fallback-directory probe was ruled out); warn is the
// grade (backup-size guidance, not corruption).
func checkDBSizeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "db_size"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query the database size"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT pg_database_size(current_database())`, nil)
	if err != nil {
		// r109-4: Doltgres does not implement pg_database_size — on it this
		// check is a standing quiet skip (documented in TUNING §2); the
		// threshold governs engines that do provide the function.
		c.Status = "skip"
		c.Detail = "pg_database_size unavailable — check skipped (Doltgres does not implement the function — a standing skip there)"
		return c
	}
	bytes := int64(0)
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &bytes)
	}
	mb := int(bytes >> 20)
	if mb >= th.DbSizeWarnMB {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("database size %dMB at or above threshold %dMB (db_size_warn_mb)", mb, th.DbSizeWarnMB)
		c.Fix = "fix: review backup retention — RUNBOOK"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("database size %dMB", mb)
	return c
}

// EngineStatsFn — the docker stats seam (var, the ComposePsFn convention).
// One no-stream sample of every running container (docker stats is the
// whole surface here — no DB path). All errors, including docker absent
// and an unresponsive daemon, become quiet skips at the callers
// (non-interference — a footprint reading must never stall or fail a run).
var EngineStatsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "json").Output()
}

// dockerStatsEntry — one docker stats row (only the footprint fields).
type dockerStatsEntry struct {
	Name     string `json:"Name"`
	MemUsage string `json:"MemUsage"`
}

// memUsageUnits — the docker stats MemUsage unit suffixes to MB factors.
// The binary KiB family is the docker CLI display unit; the decimal family
// is accepted the same way. MB here is MiB-based (the db_size >> 20
// convention — one notion of MB across the engine footprint surface).
var memUsageUnits = map[string]float64{
	"B": 1.0 / (1 << 20), "KiB": 1.0 / (1 << 10), "MiB": 1, "GiB": 1 << 10, "TiB": 1 << 20,
	"kB": 1e3 / (1 << 20), "KB": 1e3 / (1 << 20),
	"MB": 1e6 / (1 << 20), "GB": 1e9 / (1 << 20), "TB": 1e12 / (1 << 20),
}

// parseMemUsageMB — "50.5MiB / 3.84GiB" (used / total) → the used side in
// MB. ok=false on any unparsable shape (a quiet skip — never a disguised 0).
func parseMemUsageMB(s string) (int, bool) {
	used, _, _ := strings.Cut(s, "/")
	var f float64
	var unit string
	if n, _ := fmt.Sscanf(strings.TrimSpace(used), "%f%s", &f, &unit); n != 2 {
		return 0, false
	}
	factor, ok := memUsageUnits[unit]
	if !ok || f < 0 {
		return 0, false
	}
	return int(f * factor), true
}

// EngineMemMB — the flint engine container's working-set memory (MB) via
// the EngineStatsFn seam. The 5s cap follows the checkUpdate convention
// (r109-5: docker stats --no-stream takes a sampling window of its own — a
// loaded daemon regularly blew the old 2s cap, so the footprint read was a
// standing timeout-skip; 5s covers the window while still bounding an
// unresponsive docker from stalling doctor/status); the JSONL rows are
// decoded one by one and the first flint-named row (IsFlintContainer) is
// answered. ok=false covers every failure: seam error, unparsable output,
// and no flint container row — the callers stay quiet (PLAN §4 principle 3).
func EngineMemMB(ctx context.Context) (int, bool) {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := EngineStatsFn(tctx)
	if err != nil {
		return 0, false
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b)))
	for {
		var e dockerStatsEntry
		if err := dec.Decode(&e); err != nil {
			return 0, false // EOF (no flint row) or unparsable — the same quiet skip
		}
		if IsFlintContainer(e.Name) {
			return parseMemUsageMB(e.MemUsage)
		}
	}
}

// checkEngineFootprint — the v1.25.0 engine container footprint display:
// the flint engine's working-set memory from docker stats --no-stream (the
// reclaimable-cache-excluded working set — the idle-footprint metric).
// Display only (M2): ok carries the value in the detail, and every failure
// mode (docker absent, no flint container, unparsable output) is a quiet
// skip — no threshold, no warn/fail verdict, no new storage.
func checkEngineFootprint(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "engine-footprint"}
	mb, ok := EngineMemMB(ctx)
	if !ok {
		c.Status = "skip"
		c.Detail = "docker stats unavailable — check skipped"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("engine working set %dMB", mb)
	return c
}

// checkUpdate — a lag warning versus the latest release for the current build.
// doctor is a user-run path, so it performs the network check directly (a 5s
// timeout). Lag is a warn guidance only (not fail — updating follows the explicit-invocation contract).
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

// checkRecallStarved — observation of the cumulative missing-recall counter. Even if the minimal
// profile auto-concludes the marker, this counter is profile-independent —
// doctor becomes the feedback loop's final observation window. The counter itself is a notice
// (warn); since the reset guidance key is a non-registry key with no flint tuning reset concept,
// it becomes a deletion guidance.
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
		c.Status = "warn" // does not disguise a lookup failure as a clean finding (review 3).
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

// meteringFreshnessSources — the 5 event-table recency sources of the
// metering-freshness check (schema v13 A4). recall_events carries no ts
// column — its time column is recalled_at (measured against the schema
// v12 definition; a ts query on that table errors and would be skipped,
// silently blinding the source — hence the explicit column here).
var meteringFreshnessSources = []struct {
	label string
	sql   string
}{
	{"read_events", `SELECT max(ts) FROM read_events`},
	{"recall_events", `SELECT max(recalled_at) FROM recall_events`},
	{"hot_events", `SELECT max(ts) FROM hot_events`},
	{"knowledge_reads", `SELECT max(ts) FROM knowledge_reads`},
	{"session_starts", `SELECT max(ts) FROM session_starts`},
}

// meteringFreshnessGateKeys — the 2 gate-stamp meta keys joining the 7
// judgment sources (the T3/T4 24h-gate stamps — a stale stamp means the
// snapshot·rollup circuits have not confirmed for that long).
var meteringFreshnessGateKeys = []string{"last_threshold_snapshot_at", "last_rollup_at"}

// checkMeteringFreshnessTh — the metering-health freshness verdict
// (schema v13 A4): each of the 7 sources (5 table maxes + 2 gate stamps)
// judged against metering_freshness_warn_h (default 336h = 14 days,
// aligned with the events retention horizon — a source older than the
// horizon means its history has fully rolled over without one new
// event). Never-recorded sources count as infinitely stale (an empty
// table or an unstamped gate is the same warning — the stallDetail
// convention). A per-source lookup failure skips only that source
// quietly (PLAN §4 principle 3 — a failed probe is not evidence of
// staleness); when every source fails, the verdict defers (ok). Stale
// metering does not block operation — warn only.
func checkMeteringFreshnessTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "metering-freshness"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query metering freshness"
		return c
	}
	// The warn-hours clamp (v1.19.1) now shares the db.HoursSpan choke point
	// (v1.19.3): a warn_h override past the Duration ceiling (~2562047h)
	// wraps time.Duration(warnH)*time.Hour negative — every source would
	// report stale (or none, by sign) off a corrupted limit. HoursSpan applies
	// the 87600h = 10 years ceiling (far past any meaningful freshness
	// horizon, exactly representable) plus the <=0 default fallback.
	limit := db.HoursSpan(th.MeteringFreshnessWarnH, store.DefaultThresholds().MeteringFreshnessWarnH)
	warnH := int(limit.Hours())
	var stale []string
	judged := 0
	now := time.Now()
	over := func(label string, ts time.Time) {
		judged++
		// A future-dated source counts as stale (v1.19.1): its negative
		// since-span would otherwise mask the check for the whole forward
		// span — a future stamp is not freshness, it is a clock defect.
		if ts.After(now) {
			stale = append(stale, fmt.Sprintf("%s future", label))
			return
		}
		if d := now.Sub(ts); d > limit {
			stale = append(stale, fmt.Sprintf("%s %dh", label, int(d.Hours())))
		}
	}
	for _, s := range meteringFreshnessSources {
		rows, err := wire.QueryOnce(ctx, st, s.sql, nil)
		if err != nil {
			continue // the failed probe skips quietly — not stale evidence
		}
		if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
			judged++
			stale = append(stale, s.label+" (never)")
			continue
		}
		if t, ok := CellToTime(rows[0][0]); ok {
			over(s.label, t)
		}
	}
	// The gate keys travel as an IN literal (the checkLastSeenTh
	// convention — the key set is a fixed package constant, no parameters).
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_threshold_snapshot_at','last_rollup_at')`, nil); err == nil {
		stamps := map[string]time.Time{}
		// a present-but-unparseable value (a leftover claim token, a corrupted
		// stamp) is a distinct verdict from never-recorded (v1.19.2): an
		// observation exists, it just cannot be judged — shown as (corrupt),
		// never silently folded into (never).
		corrupt := map[string]bool{}
		for _, r := range rows {
			if len(r) != 2 || r[0] == nil || r[1] == nil {
				continue
			}
			if ts, perr := time.Parse(time.RFC3339, fmt.Sprint(r[1])); perr == nil {
				stamps[fmt.Sprint(r[0])] = ts
			} else {
				corrupt[fmt.Sprint(r[0])] = true
			}
		}
		for _, k := range meteringFreshnessGateKeys {
			if ts, ok := stamps[k]; ok {
				over(k, ts)
				continue
			}
			judged++
			if corrupt[k] {
				stale = append(stale, k+" (corrupt)")
				continue
			}
			stale = append(stale, k+" (never)")
		}
	}
	if judged == 0 {
		c.Status = "ok"
		c.Detail = "metering freshness unavailable — verdict deferred"
		return c
	}
	if len(stale) > 0 {
		c.Status = "warn"
		// the detail reports the clamped warnH (v1.19.2) — the pre-clamp override
		// value printed a limit the check never actually applied.
		c.Detail = fmt.Sprintf("%d of %d sources over %dh: %s",
			len(stale), judged, warnH, strings.Join(stale, ", "))
		c.Fix = "fix: run a flint read (the metering path) — the stale sources then refresh"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d sources within %dh", judged, warnH)
	return c
}

// checkSecrets — a post-hoc secret scan of held knowledge.
// The write-time scan (write ScanSecrets) misses evasion and later intake —
// doctor re-scans all knowledge and warns on findings (outputting the pattern name + id, not the
// body — the CLAUDE.md personal-data principle). Session summaries are included (the most PII).
// The body is the thresholds-injected variant (checkSecretsTh) — the doctor chain
// shares the once-per-run loaded value (doctorThresholds), and this wrapper loads its own
// for direct callers (inheriting the checkMetaHygiene F6 pattern).
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
	// reviews 1/2/6: (soft-)invalidated rows are excluded (invalidation itself is this check's
	// resolution path); the scanned columns are the same set as at write time (topic, summary, tags,
	// entity, domain, origin — see sanitizePayload); memory is row-by-row streaming
	// (the Store.Query cursor — avoiding full-table materialization).
	// The threshold is read from the injected th (once-per-run load;
	// on failure the WriteThresholds fallback applies).
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
		// cap reached — stating explicitly that the scan is partial (preventing a false
		// 'not found' — never falsely reporting 'nothing detected').
		c.Status = "warn"
		c.Detail = fmt.Sprintf("row limit %d reached — scan not run (large DB)", th.SecretsScanRows)
		c.Fix = "fix: fall back to write-time warnings — adjust the limit via the secrets_scan_row_limit key"
		return c
	}
	// review-6 ruling: Store.Query is [][]any (full materialization), so true streaming would need
	// an interface extension — excessive at personal scale (thousands of rows). The column-set
	// extension, duplicate prevention, and invalidation exclusion achieve the intent (closing the
	// blind spot), and memory stays at the same level as doctor's other full-table queries
	found := 0
	seen := map[string]bool{} // review 3: prevent double-counting id+pattern
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
	// a post-hoc scan of the sessions table — summary is the most PII-dense
	// field, recovering an intent the comment had but the code did not.
	// On lookup failure or reaching the cap, it does not discard already-found knowledge findings
	// and only states the gap — and never issues a false 'scanned sessions too,
	// nothing found' ok. LIMIT uses the same cap as the knowledge path (preventing unbounded memory growth).
	srows, serr := wire.QueryOnce(ctx, st, `
		SELECT id, agent, summary FROM sessions
		LIMIT $1`, []any{th.SecretsScanRows})
	gap := "" // the scan-gap annotation (reported alongside findings when any)
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
	// a post-hoc scan of projects.name — restore and legacy intake do not
	// pass the write-time scan (the same blind spot as the session path). A finding key is
	// pattern#project:id, keeping the no-body-output contract.
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
	// exposing the allowlist application state — a means to detect typos and unauthorized
	// registrations. Resolving a session-origin finding (session:N) is not knowledge
	// invalidation — session summaries only have the re-close (done) update and the operational-DB
	// cleanup path. The branch prevents the guidance from inducing invalidation of unrelated knowledge rows.
	c.Detail = fmt.Sprintf("%d secret candidates (%s) %s· allowlist %d entries",
		found, strings.Join(disp, ", "), gapSuffix(gap), store.SecretsAllowlistCount())
	if sessionFound || projectFound {
		c.Fix = "fix: for session/project findings (session:N, project:N), re-finalize the agent (done) or clean the sessions/projects rows in the operating DB — for knowledge findings, maintain --invalidate --id N --reason secret"
	} else {
		c.Fix = "fix: flint maintain --invalidate --id N --reason secret (rescan skips invalidated rows)"
	}
	return c
}

// sortedKeys — sorts map keys (deterministic output).
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gapSuffix — normalizes the detail suffix for the scan-gap annotation (empty keeps "— ...").
func gapSuffix(gap string) string {
	if gap == "" {
		return "— clears after invalidating the targets "
	}
	return gap + " "
}
