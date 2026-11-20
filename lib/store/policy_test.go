package store

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestCheckVocabularyUnknownTag — 1 warning per out-of-table tag (an empty slice = pass).
func TestCheckVocabularyUnknownTag(t *testing.T) {
	got := CheckVocabulary([]string{"playbook", "xxx"}, "main")
	want := []string{"unknown tag: xxx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}

// TestCheckVocabularyDomainViolation — warns on values outside the domain table (main, infra, preference).
func TestCheckVocabularyDomainViolation(t *testing.T) {
	got := CheckVocabulary([]string{"adr"}, "ops")
	want := []string{"unknown domain: ops"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}

// TestCheckVocabularyAllKnown — every value in the vocabulary tables passes (no warnings).
func TestCheckVocabularyAllKnown(t *testing.T) {
	got := CheckVocabulary(
		[]string{"adr", "correction", "failure", "env", "process", "contract", "playbook"},
		"infra",
	)
	if len(got) != 0 {
		t.Errorf("CheckVocabulary = %v, want 빈 슬라이스", got)
	}
}

// TestCheckVocabularyCaseNormalize — compared after case normalization (encoding).
func TestCheckVocabularyCaseNormalize(t *testing.T) {
	got := CheckVocabulary([]string{"Playbook", " ADR "}, "Main")
	if len(got) != 0 {
		t.Errorf("CheckVocabulary = %v, want 빈 슬라이스(정규화 통과)", got)
	}
	got = CheckVocabulary([]string{"XXX"}, "MAIN")
	want := []string{"unknown tag: xxx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v(경고도 정규화 형태)", got, want)
	}
}

// TestCheckVocabularyMultipleWarnings — multiple and repeated out-of-table values give 1 per value.
func TestCheckVocabularyMultipleWarnings(t *testing.T) {
	got := CheckVocabulary([]string{"aaa", "playbook", "bbb"}, "main")
	want := []string{"unknown tag: aaa (similar: adr)", "unknown tag: bbb"} // including approximation suggestions
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}

// TestCheckVocabularyVolTagsRegistered — the v1.13.0 volatility tier tags pass
// the vocabulary check (case-normalized like every other tag).
func TestCheckVocabularyVolTagsRegistered(t *testing.T) {
	got := CheckVocabulary([]string{"vol:immutable", "vol:conditional", "vol:volatile", "Vol:Immutable"}, "main")
	if len(got) != 0 {
		t.Errorf("CheckVocabulary = %v, want 빈 슬라이스", got)
	}
}

// TestVocabularyBaseSetUnchanged — the base tag set is exactly the 7 CONTENT §4
// tags plus the 3 vol:* tier tags (no accidental additions or removals).
func TestVocabularyBaseSetUnchanged(t *testing.T) {
	want := map[string]bool{
		"adr": true, "correction": true, "failure": true, "env": true,
		"process": true, "contract": true, "playbook": true,
		"vol:immutable": true, "vol:conditional": true, "vol:volatile": true,
	}
	if !reflect.DeepEqual(knownTags, want) {
		t.Errorf("knownTags = %v, want %v", knownTags, want)
	}
}

// TestCheckVocabularyColonTokenRejected — a user tag token containing a colon
// is REJECTED (not a plain unknown-tag warning): the colon format is reserved
// for dictionary-registered vol:* tags. A "vol:immutable" string input that
// matches the dictionary passes; near-misses (extra rune, inner spaces) and
// unrelated colon tokens are rejected with the distinct "rejected tag:" class.
func TestCheckVocabularyColonTokenRejected(t *testing.T) {
	cases := []struct {
		name string
		tags []string
	}{
		{"near-miss of dictionary tag", []string{"vol:immutablex"}},
		{"unrelated colon token", []string{"foo:bar"}},
		{"inner-space variant", []string{"vol : immutable"}},
	}
	for _, tc := range cases {
		got := CheckVocabulary(tc.tags, "main")
		if len(got) != 1 || !strings.HasPrefix(got[0], "rejected tag: "+normalizeVocab(tc.tags[0])) {
			t.Errorf("%s: CheckVocabulary = %v, want 1 'rejected tag: %s' 메시지", tc.name, got, normalizeVocab(tc.tags[0]))
		}
	}
	// Dictionary-registered colon tag vs near-miss distinction in one batch:
	// "vol:immutable" (registered) passes, "vol:immutable2" is rejected, and a
	// colon-free unknown tag keeps the plain warning class.
	got := CheckVocabulary([]string{"vol:immutable", "vol:immutable2", "xxx"}, "main")
	if len(got) != 2 || !strings.HasPrefix(got[0], "rejected tag: vol:immutable2") || got[1] != "unknown tag: xxx" {
		t.Errorf("CheckVocabulary = %v, want [rejected vol:immutable2, unknown xxx]", got)
	}
}

// TestVocabularyMatchesContentDoc — homogeneity guard: every base tag in
// policy.go appears in CONTENT.md §4, and every backticked colon token
// documented in §4 is a registered tag (the doc and the dictionary cannot drift).
func TestVocabularyMatchesContentDoc(t *testing.T) {
	b, err := os.ReadFile("../../docs/CONTENT.md")
	if err != nil {
		t.Fatalf("CONTENT.md 읽기 실패: %v", err)
	}
	doc := string(b)
	start := strings.Index(doc, "## 4.")
	end := strings.Index(doc, "## 5.")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("CONTENT.md §4 절을 찾을 수 없음 (start=%d end=%d)", start, end)
	}
	sec := doc[start:end]
	for tag := range knownTags {
		if !strings.Contains(sec, tag) {
			t.Errorf("사전 태그 %s가 CONTENT.md §4에 없음 (동질 위반)", tag)
		}
	}
	// Reverse direction: backticked colon tokens in §4 must be registered.
	for _, line := range strings.Split(sec, "\n") {
		for _, tok := range splitBackticked(line) {
			if strings.Contains(tok, ":") && !knownTags[tok] {
				t.Errorf("CONTENT.md §4의 콜론 태그 %s가 사전에 미등록", tok)
			}
		}
	}
}

// splitBackticked — extracts `...` tokens from a line (doc guard helper).
func splitBackticked(line string) []string {
	var out []string
	for {
		i := strings.Index(line, "`")
		if i < 0 {
			return out
		}
		rest := line[i+1:]
		j := strings.Index(rest, "`")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		line = rest[j+1:]
	}
}
