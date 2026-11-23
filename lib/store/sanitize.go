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

func markerFoldAt(runes []rune, i int) bool {
	const marker = "[flint]"
	for j := 0; j < len(marker); j++ {
		if i+j >= len(runes) || unicode.ToLower(runes[i+j]) != rune(marker[j]) {
			return false
		}
	}
	return true
}

var secretPatterns = []struct {
	name     string
	re       *regexp.Regexp
	specific bool
}{
	{"github-token", regexp.MustCompile(`ghp_[0-9A-Za-z]{20,}`), false},
	{"openai-key", regexp.MustCompile(`sk-[0-9A-Za-z_-]{20,}`), false},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), false},
	{"pem-block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), false},
	{"slack-token", regexp.MustCompile(`xoxb-[0-9A-Za-z-]{10,}`), false},

	{"anthropic-key", regexp.MustCompile(`sk-ant-[0-9A-Za-z_-]{20,}`), true},
	{"gitlab-pat", regexp.MustCompile(`glpat-[0-9A-Za-z_\-]{20,}`), false},
	{"jwt", regexp.MustCompile(`eyJ[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}`), false},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), false},

	{"openrouter-key", regexp.MustCompile(`sk-or-[0-9A-Za-z-]{20,}`), true},
	{"github-app-token", regexp.MustCompile(`gh[osr]_[0-9A-Za-z]{36,}`), false},
	{"groq-key", regexp.MustCompile(`gsk_[0-9A-Za-z]{30,}`), false},
	{"xai-key", regexp.MustCompile(`xai-[0-9A-Za-z]{30,}`), false},
}

func ScanSecrets(s string) []string {
	secretsAllowlistOnce.Do(LoadSecretsAllowlist)
	var found []string
	seen := map[string]bool{}

	s = maskAllowlisted(s)

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
			target = s
		}
		if p.re.MatchString(target) && !seen[p.name] {
			found = append(found, p.name)
			seen[p.name] = true
		}
	}
	return found
}

func MaskSecrets(s string) string {
	type keep struct{ sentinel, orig string }
	var keeps []keep
	for i, a := range secretsAllowlist {
		if a == "" || !strings.Contains(s, a) {
			continue
		}
		sent := "\x00FLINTMASKKEEP" + strconv.Itoa(i) + "\x00"
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

var (
	secretsAllowlistOnce sync.Once
	secretsAllowlist     []string
)

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
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			secretsAllowlist = append(secretsAllowlist, t)
		}
	}
}

func ResetSecretsAllowlistForTest(entries []string) {
	secretsAllowlistOnce = sync.Once{}
	secretsAllowlist = entries
}

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

var markerRes = regexp.MustCompile(`\[(?:REDACTED:[^\]]*|ALLOWED)\]`)

func SecretsAllowlistCount() int {
	return len(secretsAllowlist)
}
