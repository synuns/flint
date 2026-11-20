// import.go — onboarding import: converts existing rule files (CLAUDE.md·
// AGENTS.md etc.) into pending knowledge — inheriting the investigation
// design (heading-unit splitting·confidence 0.5 → the approve path·
// content_hash dedup). dry-run by default·--commit to confirm (inheriting
// the maintain --expire-pending convention).
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// importCandidates — discovery priority (investigation ruling: per Claude
// Code /import).
var importCandidates = []string{
	"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md",
	// .cursorrules removed: as a plain-bullet rule file it breaks the
	// promise that ## heading splitting applies — add it together with a
	// bullet-unit splitting extension when supporting it.
}

// importSection — one result of heading-unit splitting.
type importSection struct {
	Heading, Text string
}

// fenceTracker — the code fence state machine (both ``` and ~~~, CommonMark
// fenced code blocks). A closing marker must be the same character as the
// opener to close. The closing fence must be at least as long as the opener
// (CommonMark — a ``` line inside a ```` block is code content, not a
// closing fence).
type fenceTracker struct {
	open   bool
	marker byte // '`' or '~'
	length int  // opening fence length (closing fence lower bound)
}

// fenceDelim — if the line is a fence delimiter (3+ prefix of the same
// character), returns that character and length.
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

// update — updates the fence state with the line. Returning true = this
// line is a delimiter (not content) — excluded from heading detection·
// comment removal.
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
	if c == f.marker && n >= f.length { // closing fence (same character·at least the opening length)
		f.open = false
		return true
	}
	return false // a different character·shorter fence is content inside the open fence
}

// danglingFenceIdx — the starting line index of a fence still open at EOF.
// Prevents silent knowledge loss where an unterminated fence (decorative
// ~~~·a missing close) swallows all remaining document headings: computes
// the neutralization set that returns the starting line to body content.
// skip is an already-neutralized line.
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

// neutralizeDanglingFences — the set of starting line indexes of
// unterminated fences. Recomputes until the document is balanced (all
// fences closed).
func neutralizeDanglingFences(lines []string) map[int]bool {
	skip := map[int]bool{}
	for {
		idx := danglingFenceIdx(lines, skip)
		if idx < 0 {
			return skip
		}
		skip[idx] = true // this line is body content, not a fence start
	}
}

// parseMarkdownSections — heading-unit splitting (on ## — standard library
// only). A "## " inside a code fence is not a heading. ~~~ fences are also
// recognized (CommonMark) — preventing a misjudged heading inside a tilde
// fence from accruing as spurious knowledge. An unterminated fence
// neutralizes its starting line back to body (preserving the remaining
// document headings).
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
			cur.Text += line + "\n" // delimiters·fence interiors are preserved in the body
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

// ValidateFlagValue — the shared guard for value-bearing flags (v1.0.2 R2:
// a typo where the value swallows the following flag — `--out --encrypt` →
// path "--encrypt" — becomes an invalid argument). The same pattern as
// validateSessionLabel: a '-'-prefixed value is a SchemaError; the caller
// prints it then exits 3. The shared entry used also by lib/ops (backup
// --out·restore --file·report --out) — placed here because knowledge is a
// lower layer than ops.
func ValidateFlagValue(field, val string) error {
	if strings.HasPrefix(val, "-") {
		return &store.SchemaError{Field: field, Expect: "value, not a flag"}
	}
	return nil
}

// importMigrationExt — the extension-based migration routing decision for
// arguments without --format. If there is exactly one positional argument
// ending in .jsonl/.pam, returns (migration format, path, true). An
// explicit --format does not participate in routing (RunImportRouted has
// already branched — defense against double routing on direct calls);
// 0 or 2+ positionals·other extensions take the existing rule-conversion
// path.
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

// RunImport — `flint import [path] [--commit]`. No arguments = search
// candidates in the current directory. dry-run summary by default·--commit
// confirms accrual. v0.27.3: unrecognized flags are a usage error (exit 3),
// not a path — removing the blind spot where a mistyped flag was quietly
// discarded as an unreadable file.
func RunImport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// Without --format + a path ending in .jsonl/.pam, enter migration (a
	// one-line stderr notice — removing the blind spot where re-planting an
	// export result quietly yielded empty results via rule conversion). An
	// explicit --format·other extensions take the existing path.
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
			// An unrecognized flag is a usage error — removing the blind
			// spot where a mistyped flag was quietly discarded as an
			// unreadable file. The invalid-argument contract is exit 3
			// (doctor·tuning·diff alignment — resolving the comment·code
			// mismatch).
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint import [path] [--commit] | flint import --format jsonl|pam [FILE]", a)))
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
			unreadable = append(unreadable, f) // no hiding the cause
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
	// Even when mixed (readable+unreadable), report the unreadable files —
	// removing the false positive where partial loss looked like 'all
	// complete'. File names are JSON-encoded (preventing breakage from
	// quotes·backslashes in paths).
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
		// empty gets an additive hint key — guidance for when a
		// .jsonl/.pam migration file is pointed at rule conversion.
		fmt.Fprintln(stdout, `{"status":"empty","detail":"no convertible sections (## heading required)","hint":"--format jsonl|pam for migration"}`)
		return 0
	}
	if !commit {
		fmt.Fprintf(stdout, `{"status":"dry-run","files":%d,"sections":%d,"unreadable":%s,"next":"store with --commit, then promote via flint approve"}`+"\n",
			len(files), len(items), unreadableJSON)
		return 0
	}
	// Predict dedup then a single batch commit (the write-path pattern —
	// avoiding N transactions). origin is normalized by filepath.Clean —
	// fixing the idempotence break where path-notation differences
	// (./CLAUDE.md·a//b.md etc.) produced different hashes and double
	// accrual. Identical file names in different directories are different
	// rules, so they do not collide (the cross-collision of basename
	// normalization).
	var batch []map[string]any
	seen := map[string]bool{} // blocking in-batch duplicates (e.g. the same file given twice)
	dedupErrs := 0
	for _, it := range items {
		topic := store.SanitizeText(render.TruncateRunes("rule["+it.Heading+"]", 80))
		origin := store.SanitizeText("import:" + filepath.Clean(it.File))
		rec := map[string]any{
			"kind":       "knowledge",
			"topic":      topic,
			"summary":    store.SanitizeText(it.Summary),
			"tags":       []string{"process"}, // the string is dropped in normalizeTags
			"domain":     "main",
			"confidence": 0.5,
			"status":     "pending",
			"origin":     origin,
		}
		rec = store.Normalize("knowledge", rec)
		rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
		if h, _ := rec["content_hash"].(string); h != "" {
			if seen[h] {
				continue // an identical hash within the batch (e.g. the same file given twice)
			}
			seen[h] = true
			exists, err := st.ExistsHash(ctx, h)
			if err != nil {
				// A lookup error must not be treated as not-existing (the
				// write contract) — skip the item + a warning, partial
				// accrual. A total failure is reported as failure below.
				fmt.Fprintf(os.Stderr, "[flint] import %s: dedup lookup failed — skip: %v\n", it.File, err)
				dedupErrs++
				continue
			}
			if exists {
				continue // dedup — re-import idempotence
			}
		}
		// Storage-time secret scanning — aligned with the write-path
		// contract (warnings only, dual defense with the doctor's
		// after-the-fact scan). Warn only on dedup passers (avoiding
		// re-warning noise for permanently skipped items).
		warnSecrets("import "+it.File, it.Summary)
		batch = append(batch, rec)
	}
	if len(batch) == 0 && dedupErrs > 0 {
		// A total lookup failure is an outage, not 'all duplicates' — no
		// false success (aligned with the write contract).
		fmt.Fprintf(stdout, `{"status":"error","detail":"dedup lookup failed for %d items","unreadable":%s}`+"\n", dedupErrs, unreadableJSON)
		return 1
	}
	if len(batch) == 0 {
		BumpCurationCounter(ctx, st, "import_skipped", len(items)) // all skipped
		fmt.Fprintln(stdout, `{"status":"imported","inserted":0,"skipped":`+fmt.Sprint(len(items))+`,"unreadable":`+unreadableJSON+`}`)
		return 0
	}
	if _, err := st.InsertBatch(ctx, batch); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// Review counters — accrued·skipped row counts (the same arithmetic as
	// the output's inserted/skipped. Dedup lookup-error items are failures,
	// not skips, so excluded).
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

// stripImportNoise — removes HTML comments. Fence interiors are preserved.
//
//	① The defect where a ``` line inside a comment block opened a fence and
//	   swallowed the rest of the document — comment handling runs before
//	   fences; fences examine only the lines remaining after comment
//	   removal.
//	② The defect where a same-line `<!-- comment --> trailing text` not
//	   ending in --> left inComment lingering and silently deleted the
//	   whole rest of the document — open/close handled together.
//	③ Mid-line inline comment residue — in `before <!-- note --> after`,
//	   only the comment part is removed and the surrounding text is
//	   preserved.
func stripImportNoise(s string) string {
	lines := strings.Split(s, "\n")
	skip := neutralizeDanglingFences(lines)
	var b strings.Builder
	var fence fenceTracker
	inComment := false
	for i, line := range lines {
		if fence.open {
			if !skip[i] {
				fence.update(line) // closing fence
			}
			b.WriteString(line + "\n") // fence interior is content — preserved verbatim
			continue
		}
		if inComment {
			if i2 := strings.Index(line, "-->"); i2 >= 0 {
				inComment = false
				line = line[i2+3:] // text after the terminator is preserved
				if strings.TrimSpace(line) == "" {
					continue
				}
			} else {
				continue // comment continues — drop the line
			}
		}
		// Remove comment pairs — all of them if several share a line. An
		// opener without a terminator continues onto the following lines.
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
		// Fence-start detection uses the post-comment-removal line (①). An
		// unterminated fence start line (skip) is body content, not a
		// fence.
		if !skip[i] {
			fence.update(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
