// frontmatter.go — the vault note frontmatter parser: the document-leading
// `---` block only (a `---` later in the document is body content). Own
// minimal parser per the v1.21.0 ruling — no YAML dependency (go.mod keeps
// none). Accepts the three spec value forms — flow lists `[a, b]`, block
// lists (`- a` lines under a bare key), and quoted scalars `"[[x]]"` — plus
// the empty-frontmatter boundary. CRLF line endings are tolerated
// (per-line \r trimmed).
package vault

import "strings"

// Frontmatter — the parsed leading block: every scalar field (quotes
// stripped) plus the tags list.
type Frontmatter struct {
	Fields map[string]string
	Tags   []string
}

// ParseFrontmatter — splits the leading `---` block (if present) from the
// body. An unterminated block is NOT frontmatter (the whole input is
// returned as body — a decorative `---` first line must not swallow the
// note).
func ParseFrontmatter(content string) (Frontmatter, string) {
	var fm Frontmatter
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || trimCR(lines[0]) != "---" {
		return fm, content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if trimCR(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fm, content // unterminated — body, not frontmatter
	}
	fm.Fields = map[string]string{}
	lastKey := ""
	for _, raw := range lines[1:end] {
		line := strings.TrimSpace(trimCR(raw))
		if line == "" {
			continue
		}
		// Block-list item: "- a" under the last bare key.
		if strings.HasPrefix(line, "- ") && lastKey != "" {
			if v := strings.TrimSpace(line[2:]); v != "" {
				fm.Tags = append(fm.Tags, unquote(v))
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue // not a key: value line — ignored
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if key == "tags" {
			fm.Tags = parseTagValue(val)
			lastKey = "tags"
			continue
		}
		fm.Fields[key] = unquote(val)
		lastKey = ""
	}
	return fm, strings.Join(lines[end+1:], "\n")
}

// parseTagValue — one tags value: quoted scalar (single tag, wikilinks
// preserved), flow list [a, b], or a plain comma-separated scalar.
func parseTagValue(val string) []string {
	if val == "" {
		return nil
	}
	if q, ok := stripMatchingQuotes(val); ok {
		return []string{q} // a quoted scalar is ONE tag ("[[x]]" stays whole)
	}
	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		val = val[1 : len(val)-1]
	}
	var out []string
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, unquote(part))
	}
	return out
}

// unquote — strips one surrounding quoted pair, if present.
func unquote(s string) string {
	if q, ok := stripMatchingQuotes(s); ok {
		return q
	}
	return s
}

// stripMatchingQuotes — ("x", true) for a symmetrically quoted string.
func stripMatchingQuotes(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return s, false
}

// trimCR — removes one trailing carriage return (CRLF tolerance).
func trimCR(s string) string {
	return strings.TrimSuffix(s, "\r")
}
