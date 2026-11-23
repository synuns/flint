package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/synuns/flint/lib/wire"
)

func SyncRules(srcDir string, destDir string) (copied int, err error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		srcPath := filepath.Join(srcDir, e.Name())
		body, err := os.ReadFile(srcPath)
		if err != nil {
			return copied, err
		}
		destPath := filepath.Join(destDir, e.Name())
		if prev, rerr := os.ReadFile(destPath); rerr == nil && string(prev) == string(body) {
			continue
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return copied, err
		}
		if err := os.WriteFile(destPath, body, 0o644); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

func SkillSrcDir(binPath string) string {
	var cands []string
	if binPath != "" {
		cands = append(cands, filepath.Join(filepath.Dir(binPath), "flint-plugin", "skills", "flint"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "flint-plugin", "skills", "flint"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}

func CheckSkillFile(srcDir, destDir string) (DoctorCheck, bool) {
	if srcDir == "" {
		return DoctorCheck{}, false
	}
	src, err := os.ReadFile(filepath.Join(srcDir, "SKILL.md"))
	if err != nil {
		return DoctorCheck{}, false
	}
	c := DoctorCheck{Name: "skill-file"}
	destPath := filepath.Join(destDir, "SKILL.md")
	dest, rerr := os.ReadFile(destPath)
	if rerr != nil {
		c.Status = "warn"
		c.Detail = destPath + " not found"
		c.Fix = "fix: flint setup --update-rules — skill sync"
		return c, true
	}
	if string(src) != string(dest) {
		c.Status = "warn"
		c.Detail = destPath + " differs from source"
		c.Fix = "fix: flint setup --update-rules — skill sync"
		return c, true
	}
	c.Status = "ok"
	c.Detail = destPath
	return c, true
}

func RunUpdateRules(stdout io.Writer, srcOverride string) int {
	src := srcOverride
	if src == "" {
		src = SkillSrcDir(FlintBinPathFn())
	}
	if src == "" {
		fmt.Fprintln(stdout, wire.ErrLine(errors.New("update-rules: source flint-plugin/skills/flint not found — specify it with --src")))
		return 1
	}
	home := ClaudeHome()
	if home == "" {
		fmt.Fprintln(stdout, wire.ErrLine(errors.New("update-rules: set FLINT_CLAUDE_HOME or HOME to locate the sync destination")))
		return 1
	}
	dest := filepath.Join(home, "skills", "flint")
	n, err := SyncRules(src, dest)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("update-rules sync failed: %w", err)))
		return 1
	}
	b, err := json.Marshal(map[string]any{"status": "synced", "copied": n, "dest": dest})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
