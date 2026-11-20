package store

import (
	"fmt"
	"sort"
	"strings"
)

// the CONTENT.md §4 vocabulary tables — 7 tags and 3 domains (extend the tables first when needed).
var (
	knownTags = map[string]bool{
		"adr":        true,
		"correction": true,
		"failure":    true,
		"env":        true,
		"process":    true,
		"contract":   true,
		"playbook":   true,
	}
	knownDomains = map[string]bool{
		"main":       true,
		"infra":      true,
		"preference": true,
		// the hot_view 'recent decisions' arm requires domain='decision' (views.sql),
		// which was absent from the vocabulary, causing an unknown-domain warning and an
		// idle arm — aligned with a 4th vocabulary item (a finalized decision is CONTENT §1 type 1, top-level).
		"decision": true,
	}
)

// normalizeVocab normalizes a vocabulary value to lowercase with spaces removed (absorbing case differences).
func normalizeVocab(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// CheckVocabulary verifies that tags and domain are within the CONTENT.md §4
// vocabulary tables, returning 1 warning string per out-of-table value (an
// empty slice = pass; not a store refusal). An empty domain is not checked (the optional-argument convention).
// e.g. CheckVocabulary(["playbook","xxx"],"main") -> ["unknown tag: xxx"]
func CheckVocabulary(tags []string, domain string) []string {
	var warnings []string
	for _, tag := range tags {
		n := normalizeVocab(tag)
		if !knownTag(n) {
			w := fmt.Sprintf("unknown tag: %s", n)
			if s := SuggestTag(n); len(s) > 0 {
				w += " (similar: " + strings.Join(s, ", ") + ")"
			}
			warnings = append(warnings, w)
		}
	}
	if d := normalizeVocab(domain); d != "" && !knownDomain(d) {
		w := fmt.Sprintf("unknown domain: %s", d)
		if s := SuggestDomain(d); len(s) > 0 {
			w += " (similar: " + strings.Join(s, ", ") + ")"
		}
		warnings = append(warnings, w)
	}
	return warnings
}

// suggestFrom — up to the 2 closest candidates to the input from the vocabulary set
// (prefix matches first, then edit distance <= 2 — typo-correction
// suggestions for out-of-vocabulary values).
func suggestFrom(in string, known map[string]bool) []string {
	n := normalizeVocab(in)
	if n == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(k string) bool {
		if seen[k] {
			return false
		}
		seen[k] = true
		out = append(out, k)
		return len(out) == 2
	}
	keys := make([]string, 0, len(known))
	for k := range known {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// (1) prefix matches (certain intent).
	for _, k := range keys {
		if (strings.HasPrefix(k, n) || strings.HasPrefix(n, k)) && add(k) {
			return out
		}
	}
	// (2) edit distance <= 2 approximation.
	for _, k := range keys {
		if editDistance(n, k) <= 2 && add(k) {
			return out
		}
	}
	return out
}

// editDistance — Levenshtein (lowercase, small vocabulary scale — a classic DP).
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// SuggestTag and SuggestDomain — correction candidates for out-of-vocabulary values.
func SuggestTag(in string) []string {
	return suggestFrom(in, unionVocab(knownTags, vocabExtraTags))
}

func SuggestDomain(in string) []string {
	return suggestFrom(in, unionVocab(knownDomains, vocabExtraDomains))
}

// unionVocab — the base set union the personal set (suggestions also come from the personal vocabulary).
func unionVocab(base, extra map[string]bool) map[string]bool {
	out := make(map[string]bool, len(base)+len(extra))
	for k := range base {
		out[k] = true
	}
	for k := range extra {
		out[k] = true
	}
	return out
}

// VocabExtras — the meta-override vocabulary (personal vocabulary lives in the
// operational DB meta, not the repo — unioned. Since growing the vocabulary is
// the common case, it does not replace the base set). SetVocabExtras is injected
// once at the start of the write path (a one-shot CLI process — no update race).
var (
	vocabExtraTags    = map[string]bool{}
	vocabExtraDomains = map[string]bool{}
)

// SetVocabExtras — injects the personal vocabulary (normalized, then unioned
// in). Each call replaces the existing extras (so resets and changes are
// reflected in the long-lived flint mcp process — append-only would leave them forever).
func SetVocabExtras(tags, domains []string) {
	vocabExtraTags = map[string]bool{}
	vocabExtraDomains = map[string]bool{}
	for _, t := range tags {
		if n := normalizeVocab(t); n != "" {
			vocabExtraTags[n] = true
		}
	}
	for _, d := range domains {
		if n := normalizeVocab(d); n != "" {
			vocabExtraDomains[n] = true
		}
	}
}

// knownTag — the base set union the personal set.
func knownTag(s string) bool { return knownTags[s] || vocabExtraTags[s] }

// knownDomain — the base set union the personal set.
func knownDomain(s string) bool { return knownDomains[s] || vocabExtraDomains[s] }
