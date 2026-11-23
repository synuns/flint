package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var importCandidates = []string{
	"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md",
}

type importSection struct {
	Heading, Text string
}

type fenceTracker struct {
	open   bool
	marker byte
	length int
}

func fenceDelim(line string) (byte, int, bool) {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return 0, 0, false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n >= 3 {
		return c, n, true
	}
	return 0, 0, false
}

func (f *fenceTracker) update(line string) bool {
	c, n, isDelim := fenceDelim(line)
	if !isDelim {
		return false
	}
	if !f.open {
		f.open = true
		f.marker = c
		f.length = n
		return true
	}
	if c == f.marker && n >= f.length {
		f.open = false
		return true
	}
	return false
}

func danglingFenceIdx(lines []string, skip map[int]bool) int {
	var f fenceTracker
	openIdx := -1
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if _, _, isDelim := fenceDelim(line); isDelim && !f.open {
			openIdx = i
		}
		f.update(line)
	}
	if f.open {
		return openIdx
	}
	return -1
}

func neutralizeDanglingFences(lines []string) map[int]bool {
	skip := map[int]bool{}
	for {
		idx := danglingFenceIdx(lines, skip)
		if idx < 0 {
			return skip
		}
		skip[idx] = true
	}
}

func parseMarkdownSections(body string) []importSection {
	lines := strings.Split(body, "\n")
	skip := neutralizeDanglingFences(lines)
	var out []importSection
	var cur importSection
	var fence fenceTracker
	for i, line := range lines {
		isDelim := false
		if !skip[i] {
			isDelim = fence.update(line)
		}
		if isDelim || fence.open {
			cur.Text += line + "\n"
			continue
		}
		if strings.HasPrefix(line, "## ") {
			if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
				out = append(out, cur)
			}
			cur = importSection{Heading: strings.TrimSpace(line[3:])}
			continue
		}
		cur.Text += line + "\n"
	}
	if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
		out = append(out, cur)
	}
	return out
}

func ValidateFlagValue(field, val string) error {
	if strings.HasPrefix(val, "-") {
		return &store.SchemaError{Field: field, Expect: "value, not a flag"}
	}
	return nil
}

func importMigrationExt(args []string) (format, file string, ok bool) {
	positional, path := 0, ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if a == "--format" || strings.HasPrefix(a, "--format=") {
				return "", "", false
			}
			continue
		}
		positional++
		path = a
	}
	if positional != 1 {
		return "", "", false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jsonl":
		return "jsonl", path, true
	case ".pam":
		return "pam", path, true
	}
	return "", "", false
}

func RunImport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	if format, file, ok := importMigrationExt(args); ok {
		fmt.Fprintf(os.Stderr, "[flint] import: %s ends with .%s — routing to migration (flint import --format %s)\n", file, format, format)
		return RunImportMigrate(ctx, st, []string{"--format", format, file}, stdin, stdout)
	}
	commit := false
	var pos []string
	for _, a := range args {
		if a == "--commit" {
			commit = true
		} else if strings.HasPrefix(a, "-") {

			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.import"), wire.MaskToken(a))))
			return 3
		} else {
			pos = append(pos, a)
		}
	}
	var files []string
	var unreadable []string
	if len(pos) > 0 {
		files = pos
	} else {
		for _, c := range importCandidates {
			if _, err := os.Stat(c); err == nil {
				files = append(files, c)
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, `{"status":"none","detail":"no import candidate files (CLAUDE.md, AGENTS.md, ...)"}`)
		return 0
	}
	type item struct{ File, Heading, Summary string }
	var items []item
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			unreadable = append(unreadable, f)
			continue
		}
		for _, sec := range parseMarkdownSections(stripImportNoise(string(b))) {
			summary := strings.TrimSpace(sec.Text)
			if len([]rune(summary)) < 12 {
				continue
			}
			items = append(items, item{f, sec.Heading, render.TruncateRunes(summary, 500)})
		}
	}

	unreadableJSON := "[]"
	if len(unreadable) > 0 {
		if enc, err := json.Marshal(unreadable); err == nil {
			unreadableJSON = string(enc)
		}
	}
	if len(items) == 0 {
		if len(unreadable) > 0 {
			fmt.Fprintln(stdout, `{"status":"error","detail":"unreadable","files":`+unreadableJSON+`}`)
			return 1
		}

		fmt.Fprintln(stdout, `{"status":"empty","detail":"no convertible sections (## heading required)","hint":"--format jsonl|pam for migration"}`)
		return 0
	}
	if !commit {
		fmt.Fprintf(stdout, `{"status":"dry-run","files":%d,"sections":%d,"unreadable":%s,"next":"store with --commit, then promote via flint approve"}`+"\n",
			len(files), len(items), unreadableJSON)
		return 0
	}

	var batch []map[string]any
	seen := map[string]bool{}
	dedupErrs := 0
	for _, it := range items {
		topic := store.SanitizeText(render.TruncateRunes("rule["+it.Heading+"]", 80))
		origin := store.SanitizeText("import:" + filepath.Clean(it.File))
		rec := map[string]any{
			"kind":       "knowledge",
			"topic":      topic,
			"summary":    store.SanitizeText(it.Summary),
			"tags":       []string{"process"},
			"domain":     "main",
			"confidence": 0.5,
			"status":     "pending",
			"origin":     origin,
		}
		rec = store.Normalize("knowledge", rec)
		rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
		if h, _ := rec["content_hash"].(string); h != "" {
			if seen[h] {
				continue
			}
			seen[h] = true
			exists, err := st.ExistsHash(ctx, h)
			if err != nil {

				fmt.Fprintf(os.Stderr, "[flint] import %s: dedup lookup failed — skip: %v\n", it.File, err)
				dedupErrs++
				continue
			}
			if exists {
				continue
			}
		}

		warnSecrets("import "+it.File, it.Summary)
		batch = append(batch, rec)
	}
	if len(batch) == 0 && dedupErrs > 0 {

		fmt.Fprintf(stdout, `{"status":"error","detail":"dedup lookup failed for %d items","unreadable":%s}`+"\n", dedupErrs, unreadableJSON)
		return 1
	}
	if len(batch) == 0 {
		BumpCurationCounter(ctx, st, "import_skipped", len(items))
		fmt.Fprintln(stdout, `{"status":"imported","inserted":0,"skipped":`+fmt.Sprint(len(items))+`,"unreadable":`+unreadableJSON+`}`)
		return 0
	}
	if _, err := st.InsertBatch(ctx, batch); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	BumpCurationCounter(ctx, st, "import_added", len(batch))
	skipped := len(items) - len(batch)
	if dedupErrs > 0 {
		skipped -= dedupErrs
	}
	BumpCurationCounter(ctx, st, "import_skipped", skipped)
	if dedupErrs > 0 {
		fmt.Fprintf(stdout, `{"status":"imported","inserted":%d,"skipped":%d,"dedup_errors":%d,"unreadable":%s}`+"\n",
			len(batch), len(items)-len(batch)-dedupErrs, dedupErrs, unreadableJSON)
		return 0
	}
	fmt.Fprintf(stdout, `{"status":"imported","inserted":%d,"skipped":%d,"unreadable":%s}`+"\n",
		len(batch), len(items)-len(batch), unreadableJSON)
	return 0
}

func stripImportNoise(s string) string {
	lines := strings.Split(s, "\n")
	skip := neutralizeDanglingFences(lines)
	var b strings.Builder
	var fence fenceTracker
	inComment := false
	for i, line := range lines {
		if fence.open {
			if !skip[i] {
				fence.update(line)
			}
			b.WriteString(line + "\n")
			continue
		}
		if inComment {
			if i2 := strings.Index(line, "-->"); i2 >= 0 {
				inComment = false
				line = line[i2+3:]
				if strings.TrimSpace(line) == "" {
					continue
				}
			} else {
				continue
			}
		}

		for {
			i2 := strings.Index(line, "<!--")
			if i2 < 0 {
				break
			}
			rest := line[i2+4:]
			if j := strings.Index(rest, "-->"); j >= 0 {
				line = line[:i2] + rest[j+3:]
			} else {
				line = line[:i2]
				inComment = true
				break
			}
		}

		if !skip[i] {
			fence.update(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
