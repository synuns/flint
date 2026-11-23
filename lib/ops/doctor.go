package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sort"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/engine"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var (
	CurrentVersionFn     func() string
	FetchLatestVersionFn func() (string, error)
	ValidVersionFn       func(v string) bool
	CompareVersionsFn    func(a, b string) int

	UpdateCacheInfoFn func() (string, string)

	FetchRegistryVersionFn func() (string, error)

	RegistryCacheInfoFn func() (string, string)
	EngineModeFn        func() (string, error)

	KnownMetaKeyFn func(k string) bool

	AnyToTimeFn func(v any) (time.Time, bool)

	IsConflictGroupFn      func(g db.ConflictGroup, dupThreshold float64) bool
	ConflictDupThresholdFn func() float64

	BackupEncryptOnFn func(v string) bool
	BackupKeyPathFn   func() string

	SkillSrcDirFn    func(binPath string) string
	CheckSkillFileFn func(srcDir, destDir string) (DoctorCheck, bool)
)

type CheckSpec struct {
	Name string
	Fn   func(ctx context.Context, st wire.Store) DoctorCheck

	ThFn func(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck

	Chain string

	OnStoreNil string
}

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

		{Name: doltLogDepthName, Chain: "tail", OnStoreNil: "warn", ThFn: checkDoltLogDepthTh},
		{Name: "db_size", Chain: "tail", OnStoreNil: "warn", ThFn: checkDBSizeTh},

		{Name: "dropped_dbs", Chain: "tail", OnStoreNil: "", ThFn: checkDroppedDBsTh},
		{Name: "stray_dbs", Chain: "tail", OnStoreNil: "", ThFn: checkStrayDBsTh},

		{Name: "engine-footprint", Chain: "tail", OnStoreNil: "", Fn: checkEngineFootprint},

		{Name: "engine", Chain: "tail", OnStoreNil: "", Fn: checkEngine},

		{Name: "versions", Chain: "tail", OnStoreNil: "", ThFn: checkVersionsTh},
	}
}

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

func runCheckSpec(ctx context.Context, st wire.Store, th func() store.Thresholds, s CheckSpec) DoctorCheck {
	if s.ThFn != nil {
		return s.ThFn(ctx, st, th())
	}
	return s.Fn(ctx, st)
}

func doctorSpec(name string) (CheckSpec, bool) {
	for _, s := range doctorChecks() {
		if s.Name == name {
			return s, true
		}
	}
	return CheckSpec{}, false
}

func doctorChainNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

func DoctorChecks() []string {
	return doctorChainNames("core")
}

func doctorTailChecks() []string {
	return doctorChainNames("tail")
}

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorOutput struct {
	Status string        `json:"status"`
	Checks []DoctorCheck `json:"checks"`
}

func RunDoctor(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--list-checks"},
			{Name: "--fix"},

			{Name: "--json"},
			{Name: "--check", Value: true},
		},
		Usage: i18n.T("usage.doctor"),
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	if res.Given("--list-checks") {

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
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", i18n.T("err.check_requires_name")+" — "+i18n.T("usage.doctor"))))
		return 3
	}
	if subset != "" {

		_, ok := doctorSpec(subset)
		if subset == "skill-file" || strings.HasPrefix(subset, "harness-") {
			ok = true
		}
		if !ok {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("doctor: unknown check %q — "+i18n.T("usage.doctor")+" (names via --list-checks)", subset)))
			return 3
		}
		checks := []DoctorCheck{runDoctorCheck(ctx, st, subset)}
		bumpDoctorWarnCounters(ctx, st, checks)
		return doctorReport(checks, fix, jsonOut, stdout)
	}

	th := doctorThresholds(ctx, st)
	checks := make([]DoctorCheck, 0, len(doctorChecks())+len(HarnessRegistry))
	for _, s := range doctorChecks() {
		if s.Chain == "core" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}

	checks = append(checks, harnessChecks()...)

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
	bumpDoctorWarnCounters(ctx, st, checks)
	return doctorReport(checks, fix, jsonOut, stdout)
}

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

func doctorReport(checks []DoctorCheck, fix bool, jsonOut bool, stdout io.Writer) int {

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
				checks[i] = checkHookFile()
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

func runDoctorCheck(ctx context.Context, st wire.Store, name string) DoctorCheck {
	if s, ok := doctorSpec(name); ok {
		return runCheckSpec(ctx, st, doctorThresholds(ctx, st), s)
	}
	switch name {
	case "skill-file":

		if home := ClaudeHome(); home != "" {
			if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
				return c
			}
		}
		return DoctorCheck{Name: name, Status: "ok", Detail: "skill source not found — check skipped"}
	}
	if strings.HasPrefix(name, "harness-") {

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

var flintWordRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])flint(?:[^A-Za-z0-9_-]|$)`)

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

var FlintBinPathFn = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func flintHookCmd(binPath, sub string) map[string]any {
	cmd := `eval "` + shQuote(binPath) + " " + sub + `" 2>/dev/null || true`
	return map[string]any{"type": "command", "command": cmd, "timeout": 10}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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

			withSession(`session-start --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"Stop": {
			plain(`session-end --session $(jq -r '.session_id // empty | @sh' 2>/dev/null)`),
		},
		"PostCompact": {plain("compact")},
	}
}

var flintHookSubRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_-])(?:\S*/)?flint(?:\.\S+)?'?\s+"?(session-start|session-end|hot|done|nudge|compact)(?:[\s"]|$)`)

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

func hasFlintSub(v any, sub string) bool {
	return anyHookCommand(v, func(cmd string) bool {
		m := flintHookSubRe.FindStringSubmatch(cmd)
		return m != nil && m[1] == sub
	})
}

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
		return false, "", nil
	}
	if existed {
		backupPath = path + ".flint-bak-" + time.Now().Format("20060102-150405")

		mode := os.FileMode(0o600)
		if fi, serr := os.Stat(path); serr == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(backupPath, b, mode); err != nil {
			return false, "", err
		}
	}

	for ev, prev := range hooks {
		list, ok := prev.([]any)
		if !ok {
			continue
		}
		merged := make([]any, 0, len(list))
		for _, g := range list {
			if gm, ok := g.(map[string]any); ok && !stripFlintHookEntry(gm) {
				continue
			}
			merged = append(merged, g)
		}
		hooks[ev] = merged
	}
	for ev, add := range flintHookDefs(binPath) {
		entries := add
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

type HarnessSpec struct {
	Name        string
	Rel         string
	EnvOverride string
	Project     bool

	MCPAdd []string
}

var HarnessRegistry = []HarnessSpec{

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

	{Name: "windsurf", Rel: ".codeium/windsurf/mcp_config.json", EnvOverride: "FLINT_WINDSURF_HOME"},
	{Name: "zed", Rel: ".config/zed/settings.json", EnvOverride: "FLINT_ZED_HOME"},
	{Name: "amp", Rel: ".config/amp/settings.json", EnvOverride: "FLINT_AMP_HOME"},
	{Name: "vscode", Rel: ".mcp.json", EnvOverride: "FLINT_VSCODE_DIR", Project: true},
	{Name: "goose", Rel: ".config/goose/config.yaml", EnvOverride: "FLINT_GOOSE_HOME"},
	{Name: "trae", Rel: ".trae/mcp.json", EnvOverride: "FLINT_TRAE_DIR", Project: true},
}

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

func harnessChecks() []DoctorCheck {
	var out []DoctorCheck
	for _, h := range HarnessRegistry {
		if c, ok := checkHarnessFile(h); ok {
			out = append(out, c)
		}
	}
	return out
}

func checkHarnessFile(h HarnessSpec) (DoctorCheck, bool) {
	path := h.path()
	if path == "" {
		return DoctorCheck{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return DoctorCheck{}, false
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

func checkLastSeenTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — check DB connection"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn"
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
			continue
		}
		seen[fmt.Sprint(r[0])] = ts
	}

	if rows, rerr := wire.QueryOnce(ctx, st,
		`SELECT max(ts) FROM read_events`, nil); rerr == nil &&
		len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			if prev, had := seen["last_read_at"]; !had || t.After(prev) {
				seen["last_read_at"] = t
			}
		}
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

			if d, ok := dailyKeyDate(k); ok && d < cutoff {
				staleDaily++
			}
		case KnownMetaKeyFn(k) || thKeys[k]:
		default:

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
		ts = time.Time{}
	}
	if ts.IsZero() {
		c.Status = "warn"
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}

	if d := time.Since(ts); d >= db.HoursSpan(th.BackupAgeWarnH, store.DefaultThresholds().BackupAgeWarnH) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("last backup %dh ago — check the flint done hook or run backup manually", int(d.Hours()))
		return c
	}
	c.Status = "ok"
	return c
}

const DoctorConflictLimit = 50

type conflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

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

const doctorZeroNearWindow = 7

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
		c.Status = "ok"
		return c
	}

	if th.PendingAgeDays <= 0 {
		th = store.DefaultThresholds()
	}

	if d := time.Since(va); d >= db.DaysSpan(th.PendingAgeDays, time.Now()) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("oldest pending %dd past — clean up with maintain --expire-pending N",
			int(d.Hours()/24))
		return c
	}
	c.Status = "ok"
	return c
}

func stallDetail(seen map[string]time.Time, key string, graceH int, warns *int) string {
	ts, ok := seen[key]
	if !ok {
		*warns++
		return key + " never recorded"
	}

	grace := db.HoursSpan(graceH, 0)
	if d := time.Since(ts); d > grace {
		*warns++
		return fmt.Sprintf("%s elapsed %s (over %dh grace)", key, d.Round(time.Hour), int(grace.Hours()))
	}
	return ""
}

const doctorPort = 5434

const enginePinnedImage = "dolthub/doltgresql@sha256:3a119bb6726a0be283cfda2a73fa9fbdff58df06c0b850f245cd1828f57281a6"

const flintProjectLabel = "com.docker.compose.project"

var ComposePsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "ps", "--format", "json").Output()
}

type composePsPublisher struct {
	PublishedPort int `json:"PublishedPort"`
}

type composePsEntry struct {
	Name       string               `json:"Name"`
	Names      string               `json:"Names"`
	State      string               `json:"State"`
	Image      string               `json:"Image"`
	Labels     string               `json:"Labels"`
	Ports      string               `json:"Ports"`
	Publishers []composePsPublisher `json:"Publishers"`
}

func (e composePsEntry) displayName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.Names
}

var hostPortRe = regexp.MustCompile(`:(\d+)->`)

func entryProject(e composePsEntry) string {
	for _, kv := range strings.Split(e.Labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.TrimSpace(k) == flintProjectLabel {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func checkPort(ctx context.Context) DoctorCheck {
	c := DoctorCheck{Name: "port"}

	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		c.Status = "skip"
		c.Detail = "docker ps failed: " + err.Error()
		return c
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		c.Status = "skip"
		c.Detail = "cannot parse docker ps output: " + perr.Error()
		return c
	}

	port := enginePort()
	for _, e := range entries {
		if entryProject(e) == "flint" && e.State == "running" && publishesPort(e, port) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s (project flint, managed) publishes %d", e.displayName(), port)
			return c
		}
	}

	for _, e := range entries {
		if publishesPort(e, port) {
			c.Status = "skip"
			c.Detail = fmt.Sprintf("port %d held by %s (project %q) — unknown, not flint-managed", port, e.displayName(), entryProject(e))
			return c
		}
	}

	if EngineModeFn != nil {
		if mode, merr := EngineModeFn(); merr == nil && mode == "native" {
			c.Status = "skip"
			c.Detail = "native engine mode — container port check not applicable"
			return c
		}
	}
	c.Status = "fail"
	c.Detail = fmt.Sprintf("no flint-managed container (label %s=flint) publishes %d — unknown", flintProjectLabel, port)
	c.Fix = "fix: flint setup (starts the pinned embedded-compose engine)"
	return c
}

func parseComposePs(b []byte) ([]composePsEntry, error) {
	trimmed := bytes.TrimSpace(b)
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var out []composePsEntry
	if len(trimmed) > 0 && trimmed[0] == '[' {
		tok, err := dec.Token()
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

func IsFlintContainer(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "flint")
}

func publishesPort(e composePsEntry, port int) bool {
	for _, p := range e.Publishers {
		if p.PublishedPort == port {
			return true
		}
	}
	for _, m := range hostPortRe.FindAllStringSubmatch(e.Ports, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n == port {
			return true
		}
	}
	return false
}

func enginePort() int {
	if u, err := url.Parse(wire.ResolveDSN()); err == nil {
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				return n
			}
		}
	}
	return doctorPort
}

func engineContainerName(ctx context.Context) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		return "", err
	}
	entries, err := parseComposePs(b)
	if err != nil {
		return "", err
	}
	port := enginePort()
	for _, e := range entries {
		if e.State == "running" && publishesPort(e, port) {
			return e.displayName(), nil
		}
	}
	return "", fmt.Errorf("no running container publishes %d", port)
}

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

const doltLogDepthName = "dolt_" + "log_depth"

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

func checkDroppedDBsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "dropped_dbs"}
	if st == nil {
		c.Status = "skip"
		c.Detail = "no store — check skipped"
		return c
	}
	names, ok := listDroppedDatabases(ctx, st)
	if !ok {
		c.Status = "skip"
		c.Detail = "dropped-database catalog unavailable — check skipped (a standing skip)"
		return c
	}
	if len(names) >= th.DroppedDBsWarn {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d dropped database(s) restorable — engine-wide space held (dropped_dbs_warn %d): %s",
			len(names), th.DroppedDBsWarn, strings.Join(names, ", "))
		c.Fix = "fix: flint maintain --purge-dropped --yes (irreversible — the dry-run list shows what is restorable)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d dropped database(s) restorable", len(names))
	return c
}

const strayDBsAllowKey = "stray_dbs_allow"

func strayDBsAllowList(ctx context.Context, st wire.Store) map[string]bool {
	out := map[string]bool{}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT value FROM meta WHERE key = $1`, []any{strayDBsAllowKey})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return out
	}
	for _, p := range strings.Split(fmt.Sprint(rows[0][0]), ",") {
		if n := strings.TrimSpace(p); n != "" {
			out[n] = true
		}
	}
	return out
}

func checkStrayDBsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "stray_dbs"}
	if st == nil {
		c.Status = "skip"
		c.Detail = "no store — check skipped"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT datname FROM pg_database`, nil)
	if err != nil {
		c.Status = "skip"
		c.Detail = "pg_database unavailable — check skipped"
		return c
	}
	allow := strayDBsAllowList(ctx, st)
	for _, b := range []string{"postgres", "template0", "template1"} {
		allow[b] = true
	}
	if u, uerr := url.Parse(PurgeDSNFn()); uerr == nil {
		if opsDB := strings.TrimPrefix(u.Path, "/"); opsDB != "" {
			allow[opsDB] = true
		}
	}
	var stray []string
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		name := strings.TrimSpace(fmt.Sprint(r[0]))
		if name == "" || allow[name] {
			continue
		}
		stray = append(stray, name)
	}
	sort.Strings(stray)
	if len(stray) > th.StrayDBsWarn {
		detail := fmt.Sprintf("%d stray database(s) beyond the allow list (stray_dbs_warn %d): %s",
			len(stray), th.StrayDBsWarn, strings.Join(stray, ", "))
		for _, name := range stray {
			if name == "flint_test" {
				detail += " — flint_test is a legacy shared test DB: DROP then purge"
				break
			}
		}
		c.Status = "warn"
		c.Detail = detail
		c.Fix = "fix: DROP the unwanted databases, then flint maintain --purge-dropped --yes; keep-listed names go to meta stray_dbs_allow (comma list)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d stray database(s) beyond the allow list", len(stray))
	return c
}

var EngineStatsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "json").Output()
}

type dockerStatsEntry struct {
	Name     string `json:"Name"`
	MemUsage string `json:"MemUsage"`
}

var memUsageUnits = map[string]float64{
	"B": 1.0 / (1 << 20), "KiB": 1.0 / (1 << 10), "MiB": 1, "GiB": 1 << 10, "TiB": 1 << 20,
	"kB": 1e3 / (1 << 20), "KB": 1e3 / (1 << 20),
	"MB": 1e6 / (1 << 20), "GB": 1e9 / (1 << 20), "TB": 1e12 / (1 << 20),
}

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

func EngineMemMB(ctx context.Context) (int, bool, error) {
	name, err := engineContainerName(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("engine container selection: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := EngineStatsFn(tctx)
	if err != nil {
		return 0, false, err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b)))
	for {
		var e dockerStatsEntry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, false, fmt.Errorf("docker stats has no row for container %q", name)
			}
			return 0, false, fmt.Errorf("docker stats decode: %w", err)
		}
		if e.Name == name {
			mb, ok := parseMemUsageMB(e.MemUsage)
			if !ok {
				return 0, false, fmt.Errorf("unparsable MemUsage %q for container %q", e.MemUsage, name)
			}
			return mb, true, nil
		}
	}
}

var RestartCountFn = func() (int, error) {
	name, err := engineContainerName(context.Background())
	if err != nil {
		return 0, err
	}
	out, err := exec.Command("docker", "inspect", "--format", "{{.RestartCount}}", name).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

var HandshakeFn = func(ctx context.Context, st wire.Store) (db.EngineInfo, error) {
	return db.Handshake(ctx, st)
}

var ManagedEngineImageFn = func(ctx context.Context) (string, bool) {
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		return "", false
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		return "", false
	}
	for _, e := range entries {
		if entryProject(e) == "flint" && e.State == "running" {
			return e.Image, true
		}
	}
	return "", false
}

func engineVersionPart(ctx context.Context, st wire.Store) (part string, warn string, fix string) {
	if st == nil {
		return "engine unknown (no store)", "", ""
	}
	info, err := HandshakeFn(ctx, st)
	if err != nil {
		return "engine unknown (handshake failed: " + err.Error() + ")", "", ""
	}
	part = "engine " + info.Kind + " " + info.Version
	if info.Kind == "" {
		part = "engine unknown kind " + info.Version
	}
	img, managed := ManagedEngineImageFn(ctx)
	if !managed {
		return part + " (external — version recorded only)", "", ""
	}
	if info.Kind != "doltgres" {
		return part + " (managed)", "managed engine is " + info.Kind + " " + info.Version + " — flint requires Doltgres",
			"fix: flint setup (starts the pinned Doltgres engine)"
	}
	if img != enginePinnedImage {
		return part + fmt.Sprintf(" (managed, image %s — not pinned)", img),
			fmt.Sprintf("managed engine image drift — running %s but flint pins %s", img, enginePinnedImage),
			"fix: flint teardown --yes --purge then flint setup (re-pins the engine image)"
	}
	return part + " (managed, pinned)", "", ""
}

func checkEngineFootprint(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "engine-footprint"}
	if engine.CurrentMode() == engine.ModeNative {
		mb, ok, err := NativeEngineRSSFn(ctx)
		if !ok {
			c.Status = "skip"
			c.Detail = "native rss unavailable — check skipped"
			if err != nil {
				c.Detail = "engine-footprint skipped — " + err.Error()
			}
			return c
		}
		c.Status = "ok"
		c.Detail = fmt.Sprintf("engine working set %dMB (native)", mb)
		return c
	}
	mb, ok, err := EngineMemMB(ctx)
	if !ok {
		c.Status = "skip"
		c.Detail = "docker stats unavailable — check skipped"
		if err != nil {
			c.Detail = "engine-footprint skipped — " + err.Error()
		}
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("engine working set %dMB", mb)
	if restarts, rerr := RestartCountFn(); rerr == nil {
		c.Detail += fmt.Sprintf(" · restarts %d", restarts)
	}
	return c
}

var EngineStatusFn = defaultEngineStatus

func defaultEngineStatus() (engine.ServiceStatus, error) {
	s, err := engine.Status()
	if errors.Is(err, engine.ErrNotWired) && engine.CurrentMode() == engine.ModeNative {
		engine.SetNativeService(engine.NewNativeService())
		s, err = engine.Status()
	}
	return s, err
}

var NativeEngineRSSFn = defaultNativeEngineRSS

func defaultNativeEngineRSS(ctx context.Context) (int, bool, error) {
	if engine.CurrentMode() != engine.ModeNative {
		return 0, false, nil
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(tctx, "ps", "-eo", "rss=,comm=").Output()
	if err != nil {
		return 0, false, err
	}
	best := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := filepath.Base(fields[len(fields)-1])
		if name != "doltgres" && !strings.Contains(name, "doltgres") {
			continue
		}
		if kb, aerr := strconv.Atoi(fields[0]); aerr == nil && kb > best {
			best = kb
		}
	}
	if best == 0 {
		return 0, false, nil
	}
	return best / 1024, true, nil
}

func checkEngine(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "engine"}
	mode := engine.CurrentMode()
	if mode != engine.ModeNative {
		c.Status = "skip"
		c.Detail = "native mode only — current mode " + mode
		return c
	}
	if EngineStatusFn == nil {
		c.Status = "skip"
		c.Detail = "engine status unwired — check skipped"
		return c
	}
	s, err := EngineStatusFn()
	if err != nil {
		c.Status = "fail"
		c.Detail = "engine status unavailable: " + err.Error()
		c.Fix = "fix: flint setup --engine native"
		return c
	}
	if !s.Registered {
		c.Status = "fail"
		c.Detail = "native engine service not registered"
		c.Fix = "fix: flint setup --engine native (registers the service manager entry)"
		return c
	}
	if !s.Running {
		c.Status = "fail"
		c.Detail = "native engine registered but not running"
		c.Fix = "fix: flint engine start"
		return c
	}
	if s.Version != engine.NativePinVersion {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("native engine %s differs from pinned %s", s.Version, engine.NativePinVersion)
		c.Fix = "fix: flint setup --engine native (updates the engine to the pinned version)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("native engine %s (pinned %s) registered and running", s.Version, engine.NativePinVersion)
	return c
}

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

func checkVersionsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "versions"}
	cur := CurrentVersionFn()
	self := cur
	if !ValidVersionFn(cur) {
		self = cur + " (dev — not finalized)"
	}
	var parts []string
	parts = append(parts, "self v"+self)

	enginePart, engineWarn, engineFix := engineVersionPart(ctx, st)
	parts = append(parts, enginePart)

	cachePart := "cache latest unknown"
	ageH := -1
	if UpdateCacheInfoFn != nil {
		if latest, checkedAt := UpdateCacheInfoFn(); latest != "" {
			cachePart = "cache latest v" + latest
			if t, terr := time.Parse(time.RFC3339, checkedAt); terr == nil {
				ageH = int(time.Since(t).Hours())
				cachePart += fmt.Sprintf(" (%dh old)", ageH)
			}
		}
	}
	parts = append(parts, cachePart)

	pathPart := "PATH flint not found"
	if p, lerr := exec.LookPath("flint"); lerr == nil {
		pathPart = "PATH " + p
	}
	parts = append(parts, pathPart)

	regPart := "registry unverified (offline)"
	if FetchRegistryVersionFn != nil {
		if v, rerr := FetchRegistryVersionFn(); rerr == nil && v != "" {
			regPart = "registry v" + v
		} else if RegistryCacheInfoFn != nil {
			if cv, checkedAt := RegistryCacheInfoFn(); cv != "" {
				regPart = "registry v" + cv + " (cached, unverified — offline"
				if t, terr := time.Parse(time.RFC3339, checkedAt); terr == nil {
					regPart += fmt.Sprintf(", %dh old", int(time.Since(t).Hours()))
				}
				regPart += ")"
			}
		}
	}
	parts = append(parts, regPart)
	c.Detail = strings.Join(parts, " · ")

	if engineWarn != "" {
		c.Status = "warn"
		c.Detail += " — " + engineWarn
		c.Fix = engineFix
		return c
	}
	if warnH := th.UpdateCacheAgeH; ageH >= 0 && warnH > 0 && ageH > warnH {
		c.Status = "warn"
		c.Detail += fmt.Sprintf(" — cache age %dh exceeds update_cache_age_h %dh", ageH, warnH)
		c.Fix = "fix: flint doctor --check update (refresh the version cache)"
		return c
	}
	c.Status = "ok"
	return c
}

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
		c.Status = "warn"
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

var meteringFreshnessGateKeys = []string{"last_threshold_snapshot_at", "last_rollup_at"}

func checkMeteringFreshnessTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "metering-freshness"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query metering freshness"
		return c
	}

	limit := db.HoursSpan(th.MeteringFreshnessWarnH, store.DefaultThresholds().MeteringFreshnessWarnH)
	warnH := int(limit.Hours())
	var stale []string
	judged := 0
	now := time.Now()
	over := func(label string, ts time.Time) {
		judged++

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
			continue
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

	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_threshold_snapshot_at','last_rollup_at')`, nil); err == nil {
		stamps := map[string]time.Time{}

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

		c.Detail = fmt.Sprintf("%d of %d sources over %dh: %s",
			len(stale), judged, warnH, strings.Join(stale, ", "))
		c.Fix = "fix: run a flint read (the metering path) — the stale sources then refresh"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d sources within %dh", judged, warnH)
	return c
}

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

		c.Status = "warn"
		c.Detail = fmt.Sprintf("row limit %d reached — scan not run (large DB)", th.SecretsScanRows)
		c.Fix = "fix: fall back to write-time warnings — adjust the limit via the secrets_scan_row_limit key"
		return c
	}

	found := 0
	seen := map[string]bool{}
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

	srows, serr := wire.QueryOnce(ctx, st, `
		SELECT id, agent, summary FROM sessions
		LIMIT $1`, []any{th.SecretsScanRows})
	gap := ""
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

	c.Detail = fmt.Sprintf("%d secret candidates (%s) %s· allowlist %d entries",
		found, strings.Join(disp, ", "), gapSuffix(gap), store.SecretsAllowlistCount())
	if sessionFound || projectFound {
		c.Fix = "fix: for session/project findings (session:N, project:N), re-finalize the agent (done) or clean the sessions/projects rows in the operating DB — for knowledge findings, maintain --invalidate --id N --reason secret"
	} else {
		c.Fix = "fix: flint maintain --invalidate --id N --reason secret (rescan skips invalidated rows)"
	}
	return c
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gapSuffix(gap string) string {
	if gap == "" {
		return "— clears after invalidating the targets "
	}
	return gap + " "
}
