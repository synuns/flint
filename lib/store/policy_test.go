package store

import (
	"reflect"
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
