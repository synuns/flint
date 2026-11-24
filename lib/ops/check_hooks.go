package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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
