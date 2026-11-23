package store

import (
	"fmt"
	"sort"
	"strings"
)

var (
	knownTags = map[string]bool{
		"adr":        true,
		"correction": true,
		"failure":    true,
		"env":        true,
		"process":    true,
		"contract":   true,
		"playbook":   true,

		"vol:immutable":   true,
		"vol:conditional": true,
		"vol:volatile":    true,
	}
	knownDomains = map[string]bool{
		"main":       true,
		"infra":      true,
		"preference": true,

		"decision": true,
	}
)

func normalizeVocab(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func CheckVocabulary(tags []string, domain string) []string {
	var warnings []string
	for _, tag := range tags {
		n := normalizeVocab(tag)
		if strings.Contains(n, ":") && !knownTag(n) {
			warnings = append(warnings, fmt.Sprintf(
				"rejected tag: %s (colon tokens are dictionary-registered only: vol:immutable, vol:conditional, vol:volatile)", n))
			continue
		}
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

	for _, k := range keys {
		if (strings.HasPrefix(k, n) || strings.HasPrefix(n, k)) && add(k) {
			return out
		}
	}

	for _, k := range keys {
		if editDistance(n, k) <= 2 && add(k) {
			return out
		}
	}
	return out
}

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

func SuggestTag(in string) []string {
	return suggestFrom(in, unionVocab(knownTags, vocabExtraTags))
}

func SuggestDomain(in string) []string {
	return suggestFrom(in, unionVocab(knownDomains, vocabExtraDomains))
}

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

var (
	vocabExtraTags    = map[string]bool{}
	vocabExtraDomains = map[string]bool{}
)

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

func knownTag(s string) bool { return knownTags[s] || vocabExtraTags[s] }

func knownDomain(s string) bool { return knownDomains[s] || vocabExtraDomains[s] }
