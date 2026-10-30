// suggest_v0100_test.go — v0.10.0 T1: did-you-mean 순수 함수 고정
// (nfkc·bigramDice·SuggestSimilar — 스펙 §3 R1·R2).
package query

import (
	"reflect"
	"testing"
)

func TestNfkcComposesNFD(t *testing.T) {
	decomposed := "한" // "한" NFD(자모 3)
	if got := nfkc(decomposed); got != "한" {
		t.Errorf("nfkc(NFD 한) = %q, want 한", got)
	}
	if got := nfkc("flint 백업"); got != "flint 백업" {
		t.Errorf("NFC 불변 위반: %q", got)
	}
}

func TestBigramDice(t *testing.T) {
	if got := bigramDice("백업복구", "백업복구"); got != 1 {
		t.Errorf("동일 문자열 = %v, want 1", got)
	}
	if got := bigramDice("백업", "복구"); got != 0 {
		t.Errorf("공유 bigram 0 = %v, want 0", got)
	}
	if got := bigramDice("flint", "flint"); got != 1 {
		t.Errorf("동일 라틴 = %v, want 1", got)
	}
}

func TestSuggestSimilarTopN(t *testing.T) {
	rows := [][]any{
		{int32(3), "flint 백업 절차", "덤프 복구"},
		{int32(4), "환영인사", "안부 인사"},
	}
	got := SuggestSimilar("백업 복구 절차", rows, 3)
	want := []Suggestion{{ID: 3, Topic: "flint 백업 절차"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestSimilar = %v, want %v", got, want)
	}
}
