package vault

import "strings"

type Frontmatter struct {
	Fields map[string]string
	Tags   []string
}

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
		return fm, content
	}
	fm.Fields = map[string]string{}
	lastKey := ""
	for _, raw := range lines[1:end] {
		line := strings.TrimSpace(trimCR(raw))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "- ") && lastKey != "" {
			if v := strings.TrimSpace(line[2:]); v != "" {
				fm.Tags = append(fm.Tags, unquote(v))
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
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

func parseTagValue(val string) []string {
	if val == "" {
		return nil
	}
	if q, ok := stripMatchingQuotes(val); ok {
		return []string{q}
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

func unquote(s string) string {
	if q, ok := stripMatchingQuotes(s); ok {
		return q
	}
	return s
}

func stripMatchingQuotes(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return s, false
}

func trimCR(s string) string {
	return strings.TrimSuffix(s, "\r")
}
