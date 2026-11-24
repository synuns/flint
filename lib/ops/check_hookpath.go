package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var HookPathCommandsFn = func() ([]string, error) {
	home := ClaudeHome()
	if home == "" {
		return nil, errors.New("cannot locate hook settings home")
	}
	b, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		return nil, err
	}
	return hookCommandStrings(string(b)), nil
}

var HookPathLookupFn = func(name string) (string, error) {
	return exec.LookPath(name)
}

var HookPathStatFn = func(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

var errHookBinNotExecutable = errors.New("binary exists but is not executable")

func hookCommandStrings(body string) []string {
	var doc map[string]any
	if json.Unmarshal([]byte(body), &doc) != nil {
		return nil
	}
	var out []string
	collectHookCommands(doc, &out)
	return out
}

func collectHookCommands(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if cmd, ok := t["command"].(string); ok && flintHookSubRe.MatchString(cmd) {
			*out = append(*out, cmd)
		}
		for _, child := range t {
			collectHookCommands(child, out)
		}
	case []any:
		for _, child := range t {
			collectHookCommands(child, out)
		}
	}
}

var hookSubWords = map[string]bool{
	"session-start": true, "session-end": true, "hot": true,
	"done": true, "nudge": true, "compact": true,
}

func hookPathTokens(cmd string) []string {
	fields := strings.Fields(cmd)
	var out []string
	for i, f := range fields {
		tok := strings.Trim(f, "\"'")
		base := tok
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if base != "flint" && !strings.HasPrefix(base, "flint.") {
			continue
		}
		if i+1 < len(fields) && hookSubWords[strings.Trim(fields[i+1], "\"'")] {
			out = append(out, tok)
		}
	}
	return out
}

func resolveHookPathToken(tok string) (string, error) {
	if !strings.Contains(tok, "/") {
		return HookPathLookupFn(tok)
	}
	fi, err := HookPathStatFn(tok)
	if err != nil {
		return "", errors.New("binary not found")
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return "", errHookBinNotExecutable
	}
	return tok, nil
}

func hookReadErrKind(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	if strings.ContainsAny(msg, `/\`) {
		return ""
	}
	return msg
}

func CheckHookPath() DoctorCheck {
	c := DoctorCheck{Name: "hook-path"}
	cmds, err := HookPathCommandsFn()
	if err != nil {
		c.Status = "fail"
		c.Detail = "hook settings unreadable — hook command resolution cannot be verified"
		if kind := hookReadErrKind(err); kind != "" {
			c.Detail = "hook settings unreadable (" + kind + ") — hook command resolution cannot be verified"
		}
		c.Fix = "fix: install the plugin (/plugin marketplace add synuns/flint → /plugin install flint@flint) or register flint hooks in settings.json — see flint-plugin/hooks/hooks.json"
		return c
	}
	total, unresolved := 0, 0
	why := ""
	for _, cmd := range cmds {
		for _, tok := range hookPathTokens(cmd) {
			total++
			if _, rerr := resolveHookPathToken(tok); rerr != nil {
				unresolved++
				if why == "" {
					why = rerr.Error()
				}
			}
		}
	}
	if unresolved > 0 {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("%d of %d flint hook command(s) unresolved — %s", unresolved, total, why)
		c.Fix = "fix: reinstall the plugin (/plugin install flint@flint) or correct the flint binary path in settings.json hook commands"
		return c
	}
	c.Status = "pass"
	if total == 0 {
		c.Detail = "no flint hook commands found — registration checked separately by hook-file"
	} else {
		c.Detail = fmt.Sprintf("%d flint hook command(s) resolve to executables", total)
	}
	return c
}
