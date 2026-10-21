package store

import (
	"reflect"
	"testing"
)

// TestCheckVocabularyUnknownTag — 표 밖 tag마다 경고 1개(빈 슬라이스=통과).
func TestCheckVocabularyUnknownTag(t *testing.T) {
	got := CheckVocabulary([]string{"playbook", "xxx"}, "main")
	want := []string{"unknown tag: xxx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}

// TestCheckVocabularyDomainViolation — domain 표(main·infra·preference) 밖 값 경고.
func TestCheckVocabularyDomainViolation(t *testing.T) {
	got := CheckVocabulary([]string{"adr"}, "ops")
	want := []string{"unknown domain: ops"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}

// TestCheckVocabularyAllKnown — 어휘표 전 값 통과(경고 없음).
func TestCheckVocabularyAllKnown(t *testing.T) {
	got := CheckVocabulary(
		[]string{"adr", "correction", "failure", "env", "process", "contract", "playbook"},
		"infra",
	)
	if len(got) != 0 {
		t.Errorf("CheckVocabulary = %v, want 빈 슬라이스", got)
	}
}

// TestCheckVocabularyCaseNormalize — 대소문자 정규화 후 비교(Review Focus: encoding).
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

// TestCheckVocabularyMultipleWarnings — 표 밖 값 여러 개·중복은 값마다 1개.
func TestCheckVocabularyMultipleWarnings(t *testing.T) {
	got := CheckVocabulary([]string{"aaa", "playbook", "bbb"}, "main")
	want := []string{"unknown tag: aaa", "unknown tag: bbb"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckVocabulary = %v, want %v", got, want)
	}
}
