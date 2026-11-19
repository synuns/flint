package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// SanitizeText performs the text neutralization for memory-poisoning defense (ADR-0006).
// (1) Removes control characters (ANSI escapes, NUL, etc.) and format characters (U+200B ZWSP,
//
//	U+202E RLO, U+FEFF — invisible-forgeries that passed because IsControl checks only
//	Latin-1) — Unicode content is preserved.
//
// (2) Replaces "[flint]" markers inside content with spaces — case-insensitive
//
//	(blocking forged variants like "[FLINT]" and "[Flint]").
//
// It warns + sanitizes rather than refusing the store, and it is idempotent (reapplying yields the same result).
// e.g. SanitizeText("a\x00b[flint]c") -> "ab c"
func SanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return replaceMarkerFold(b.String())
}

// replaceMarkerFold — the case-insensitive replacement of the "[flint]" marker (being an
// ASCII marker, a rune scan preserves the original without length mismatches).
func replaceMarkerFold(s string) string {
	const marker = "[flint]"
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		if markerFoldAt(runes, i) {
			b.WriteString(" ")
			i += len(marker)
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

// markerFoldAt — whether runes[i:] starts with the case-insensitive marker.
func markerFoldAt(runes []rune, i int) bool {
	const marker = "[flint]"
	for j := 0; j < len(marker); j++ {
		if i+j >= len(runes) || unicode.ToLower(runes[i+j]) != rune(marker[j]) {
			return false
		}
	}
	return true
}

// secretPatterns — the secret-pattern catalog. Detection order = array order (deterministic).
var secretPatterns = []struct {
	name     string
	re       *regexp.Regexp
	specific bool // a super-pattern in an inclusion relation (masked first)
}{
	{"github-token", regexp.MustCompile(`ghp_[0-9A-Za-z]{20,}`), false},
	{"openai-key", regexp.MustCompile(`sk-[0-9A-Za-z_-]{20,}`), false},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), false},
	{"pem-block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), false},
	{"slack-token", regexp.MustCompile(`xoxb-[0-9A-Za-z-]{10,}`), false},
	// ported from gitleaks top rules (shared by the write-time scan and doctor's post-hoc scan).
	{"anthropic-key", regexp.MustCompile(`sk-ant-[0-9A-Za-z_-]{20,}`), true},
	{"gitlab-pat", regexp.MustCompile(`glpat-[0-9A-Za-z_\-]{20,}`), false},
	{"jwt", regexp.MustCompile(`eyJ[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}`), false},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), false},
	// AI-ecosystem top-token refresh (tracking gitleaks).
	// openrouter's sk-or- is contained in sk- (openai) — specific masking first.
	// github-app is gh[osr] (OAuth, server, refresh) — p (ghp_) is already
	// covered by the existing github-token and is excluded (preventing double reporting).
	{"openrouter-key", regexp.MustCompile(`sk-or-[0-9A-Za-z-]{20,}`), true},
	{"github-app-token", regexp.MustCompile(`gh[osr]_[0-9A-Za-z]{36,}`), false},
	{"groq-key", regexp.MustCompile(`gsk_[0-9A-Za-z]{30,}`), false},
	{"xai-key", regexp.MustCompile(`xai-[0-9A-Za-z]{30,}`), false},
}

// ScanSecrets returns the secret-candidate pattern names (an empty slice = pass).
// Prevents double reporting of a generic pattern (openai
// sk-) contained in a special pattern (anthropic sk-ant-) — the special pattern's
// matched spans are masked before the generic scan (the gitleaks priority convention,
// compensating for RE2's lack of lookahead). A repeated identical pattern counts once — keeping the warning array lean. Not a store refusal (warnings only).
// e.g. ScanSecrets("ghp_...") -> ["github-token"]
func ScanSecrets(s string) []string {
	secretsAllowlistOnce.Do(LoadSecretsAllowlist)
	var found []string
	seen := map[string]bool{}
	// user-allowlist entries are masked first (mitigating false positives —
	// documentation example tokens, etc.), then the special patterns.
	s = maskAllowlisted(s)
	// the special patterns' matched spans are masked first — preventing double
	// reporting from the inclusion relation (sk-ant- inside sk-) (the gitleaks priority convention).
	masked := s
	for _, p := range secretPatterns {
		if !p.specific {
			continue
		}
		masked = p.re.ReplaceAllString(masked, "[REDACTED]")
	}
	for _, p := range secretPatterns {
		target := masked
		if p.specific {
			target = s // special patterns check their own matches against the original
		}
		if p.re.MatchString(target) && !seen[p.name] {
			found = append(found, p.name)
			seen[p.name] = true
		}
	}
	return found
}

// MaskSecrets — returns a copy with the secret patterns' matched spans replaced by
// [REDACTED:<pattern>] (backup masking — diagnostic and sharing dumps).
// Allowlist entries keep their original text (a user-registered documentation
// example is not replaced — protected by a sentinel before substitution and
// restored after. Unlike maskAllowlisted's [ALLOWED] rewriting, this preserves
// the content). The special-then-generic order matches ScanSecrets (preventing double substitution from inclusion relations).
func MaskSecrets(s string) string {
	type keep struct{ sentinel, orig string }
	var keeps []keep
	for i, a := range secretsAllowlist {
		if a == "" || !strings.Contains(s, a) {
			continue
		}
		sent := "\x00FLINTMASKKEEP" + strconv.Itoa(i) + "\x00" // no collision with pattern shapes
		s = strings.ReplaceAll(s, a, sent)
		keeps = append(keeps, keep{sent, a})
	}
	for _, p := range secretPatterns {
		if p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, p := range secretPatterns {
		if !p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, k := range keeps {
		s = strings.ReplaceAll(s, k.sentinel, k.orig)
	}
	return s
}

// ── the secret-scan allowlist — false-positive mitigation.
// A user file outside the repo (~/.config/flint/secrets-allowlist.txt, one per
// line) registers strings that safely appear in knowledge and fields
// (documentation example tokens, project-specific prefixes, etc.) — registering
// them masks those substrings before scanning (inheriting the personal-markers
// convention — no personal criteria in the repo). The FLINT_SECRETS_ALLOWLIST env overrides the path (test isolation).
var (
	secretsAllowlistOnce sync.Once
	secretsAllowlist     []string
)

// LoadSecretsAllowlist — reads and stores the allowlist file (a one-shot process cache).
func LoadSecretsAllowlist() {
	path := os.Getenv("FLINT_SECRETS_ALLOWLIST")
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".config", "flint", "secrets-allowlist.txt")
		}
	}
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return // absent or unreadable means an empty list — silent (not metered; the scan contract holds)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			secretsAllowlist = append(secretsAllowlist, t)
		}
	}
}

// ResetSecretsAllowlistForTest — test isolation (unused in production).
func ResetSecretsAllowlistForTest(entries []string) {
	secretsAllowlistOnce = sync.Once{}
	secretsAllowlist = entries
}

// MaskSecretsStrict — masking for off-device use (bridges, sharing): masks both the
// pattern secrets and the allowlist entries (dump --mask preserves the original
// for diagnostics; off-device, registered values must not leave in plaintext
// either). Pattern masking runs first without sentinel protection — abolishing the
// combinatorial defect where a registered value that was a prefix of a longer real
// secret left the suffix leaked in plaintext (demonstrated). If a registered value is part of a secret, pattern masking wins.
func MaskSecretsStrict(s string) string {
	for _, p := range secretPatterns {
		if p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, p := range secretPatterns {
		if !p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	return maskAllowlisted(s)
}

// maskAllowlisted — returns a copy with the allowlist entries masked.
// [REDACTED:...] and [ALLOWED] markers are protected by sentinels first,
// excluded from substitution — even if a pattern-name substring ("token") is
// registered, the audit-trail markers are not rewritten (inheriting the MaskSecrets sentinel convention).
func maskAllowlisted(s string) string {
	type keep struct{ sentinel, orig string }
	var keeps []keep
	for i, m := range markerRes.FindAllString(s, -1) {
		if m == "" {
			continue
		}
		sent := "\x01FLINTMARKKEEP" + strconv.Itoa(i) + "\x01"
		s = strings.Replace(s, m, sent, 1)
		keeps = append(keeps, keep{sent, m})
	}
	// longer registered values first — when traversing {"doc",
	// "doc-token-abc"}, the shorter entry would be replaced first, leaving
	// "[ALLOWED]-token-abc" as residual plaintext (demonstrated). Descending sort makes the longer entries match first.
	sortedAllow := append([]string(nil), secretsAllowlist...)
	sort.Slice(sortedAllow, func(i, j int) bool { return len(sortedAllow[i]) > len(sortedAllow[j]) })
	for _, a := range sortedAllow {
		if a != "" {
			s = strings.ReplaceAll(s, a, "[ALLOWED]")
		}
	}
	for _, k := range keeps {
		s = strings.Replace(s, k.sentinel, k.orig, 1)
	}
	return s
}

// markerRes — the protected audit markers (REDACTED pattern names, ALLOWED).
var markerRes = regexp.MustCompile(`\[(?:REDACTED:[^\]]*|ALLOWED)\]`)

// SecretsAllowlistCount — the currently loaded allowlist entry count (doctor
// visibility — detecting typos and unauthorized registrations).
func SecretsAllowlistCount() int {
	return len(secretsAllowlist)
}
