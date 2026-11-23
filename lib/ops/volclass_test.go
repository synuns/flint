package ops

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func TestSuggestVolClassMapping(t *testing.T) {
	cases := []struct {
		name  string
		topic string
		tags  string
		want  string
	}{

		{"ADR 토픽", "t4-ADR-0009 브랜딩", "", VolClassImmutable},
		{"adr 태그", "t4-일반", ":adr:", VolClassImmutable},
		{"decision 태그", "t4-일반", ":decision:", VolClassImmutable},
		{"결정 토픽", "t4-배포 결정 기록", "", VolClassImmutable},
		{"역사 토픽", "t4-프로젝트 역사", "", VolClassImmutable},
		{"history 토픽(대소문자)", "t4-Project History", "", VolClassImmutable},

		{"버전 토픽", "t4-node 버전 정책", "", VolClassVolatile},
		{"경로 토픽", "t4-설치 경로", "", VolClassVolatile},
		{"명령 토픽", "t4-빌드 명령", "", VolClassVolatile},
		{"도구 토픽", "t4-도구 상태", "", VolClassVolatile},
		{"version 태그", "t4-일반", ":version:", VolClassVolatile},
		{"path 토픽(대소문자)", "t4-Library Path", "", VolClassVolatile},

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

func TestSuggestVolClassDictionaryUnchanged(t *testing.T) {
	dict := []string{VolClassImmutable, VolClassVolatile, VolClassConditional}
	if w := store.CheckVocabulary(dict, ""); len(w) != 0 {
		t.Fatalf("사전 3종 승인 위반: %v", w)
	}
	if w := store.CheckVocabulary([]string{"vol:provisional"}, ""); len(w) == 0 ||
		!strings.Contains(w[0], "rejected tag") {
		t.Fatalf("콜론 가드 약화: %v", w)
	}

	for _, in := range []struct{ topic, tags string }{
		{"t4-ADR 확정", ""}, {"t4-버전", ""}, {"t4-신규", ":env:"},
	} {
		if got := SuggestVolClass(in.topic, in.tags); !contains(dict, got) {
			t.Errorf("제안 %q이 사전 밖 토큰 (입력 %q/%q)", got, in.topic, in.tags)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
