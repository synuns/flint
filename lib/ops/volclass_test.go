// volclass_test.go — the v1.22.0 T4 deterministic volatility-suggestion
// mapping contracts: (1) keyword classes (finalized decision/ADR/history →
// vol:immutable, version/path/command/tool → vol:volatile, the rest →
// vol:conditional), (2) the multi-match priority immutable > volatile >
// conditional, (3) the existing vol:* tag detection (the report shows the
// approved tag instead of a suggestion), and (4) the dictionary seal — the
// suggestion path adds NO dictionary entries (the approval regime stays).
package ops

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// TestSuggestVolClassMapping — the deterministic keyword mapping: each class
// matches via topic OR tags, case-insensitively, in English and Korean; an
// unmatched row falls to vol:conditional (the default tier).
func TestSuggestVolClassMapping(t *testing.T) {
	cases := []struct {
		name  string
		topic string
		tags  string
		want  string
	}{
		// immutable — finalized decision·ADR·history class.
		{"ADR 토픽", "t4-ADR-0009 브랜딩", "", VolClassImmutable},
		{"adr 태그", "t4-일반", ":adr:", VolClassImmutable},
		{"decision 태그", "t4-일반", ":decision:", VolClassImmutable},
		{"결정 토픽", "t4-배포 결정 기록", "", VolClassImmutable},
		{"역사 토픽", "t4-프로젝트 역사", "", VolClassImmutable},
		{"history 토픽(대소문자)", "t4-Project History", "", VolClassImmutable},
		// volatile — version·path·command·tool state class.
		{"버전 토픽", "t4-node 버전 정책", "", VolClassVolatile},
		{"경로 토픽", "t4-설치 경로", "", VolClassVolatile},
		{"명령 토픽", "t4-빌드 명령", "", VolClassVolatile},
		{"도구 토픽", "t4-도구 상태", "", VolClassVolatile},
		{"version 태그", "t4-일반", ":version:", VolClassVolatile},
		{"path 토픽(대소문자)", "t4-Library Path", "", VolClassVolatile},
		// conditional — no keyword match (the default tier).
		{"미매칭 토픽", "t4-신규", "", VolClassConditional},
		{"미매칭 태그만", "t4-일반", ":env:", VolClassConditional},
		{"빈 입력", "", "", VolClassConditional},
	}
	for _, c := range cases {
		if got := SuggestVolClass(c.topic, c.tags); got != c.want {
			t.Errorf("%s: SuggestVolClass(%q, %q) = %q, want %q", c.name, c.topic, c.tags, got, c.want)
		}
	}
}

// TestSuggestVolClassMultiMatchPriority — when keywords of several classes
// match, the fixed priority applies: immutable > volatile > conditional (an
// immutable-graded row is never downgraded by a coincidental volatile keyword).
func TestSuggestVolClassMultiMatchPriority(t *testing.T) {
	if got := SuggestVolClass("t4-ADR 버전 기록", ":version:"); got != VolClassImmutable {
		t.Errorf("immutable>volatile: = %q, want %q(ADR·버전 동시 매칭)", got, VolClassImmutable)
	}
	if got := SuggestVolClass("t4-결정 경로 명령", ""); got != VolClassImmutable {
		t.Errorf("immutable>volatile(한글): = %q, want %q", got, VolClassImmutable)
	}
	if got := SuggestVolClass("t4-버전 경로 명령 도구", ""); got != VolClassVolatile {
		t.Errorf("volatile>conditional: = %q, want %q", got, VolClassVolatile)
	}
}

// TestExistingVolTag — the vol:* dictionary tags present in a tags cell (the
// approval-regime marker): the first dictionary tag in the fixed dict order,
// "" when the row carries none. A :vol:immutable: row never reaches the report
// population (the server-side exclusion) but detection still covers it.
func TestExistingVolTag(t *testing.T) {
	cases := []struct {
		tags string
		want string
	}{
		{":adr::vol:conditional:", VolClassConditional},
		{":vol:volatile:", VolClassVolatile},
		{":vol:immutable:", VolClassImmutable},
		{":adr:", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExistingVolTag(c.tags); got != c.want {
			t.Errorf("ExistingVolTag(%q) = %q, want %q", c.tags, got, c.want)
		}
	}
}

// TestSuggestVolClassDictionaryUnchanged — the suggestion path is
// proposal-only: every suggested value is one of the 3 registered dictionary
// tags (store.CheckVocabulary accepts exactly those), and the colon guard
// still rejects a non-dictionary vol:* token — T4 adds no dictionary entries
// (the approval regime ruling).
func TestSuggestVolClassDictionaryUnchanged(t *testing.T) {
	dict := []string{VolClassImmutable, VolClassVolatile, VolClassConditional}
	if w := store.CheckVocabulary(dict, ""); len(w) != 0 {
		t.Fatalf("사전 3종 승인 위반: %v", w)
	}
	if w := store.CheckVocabulary([]string{"vol:provisional"}, ""); len(w) == 0 ||
		!strings.Contains(w[0], "rejected tag") {
		t.Fatalf("콜론 가드 약화: %v", w)
	}
	// every suggestion stays within the dictionary set.
	for _, in := range []struct{ topic, tags string }{
		{"t4-ADR 확정", ""}, {"t4-버전", ""}, {"t4-신규", ":env:"},
	} {
		if got := SuggestVolClass(in.topic, in.tags); !contains(dict, got) {
			t.Errorf("제안 %q이 사전 밖 토큰 (입력 %q/%q)", got, in.topic, in.tags)
		}
	}
}

// contains — a tiny slice membership helper (test-local).
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
