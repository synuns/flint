// suggest_v0131_test.go — v0.13.1 F6: Dice 계수 공개 래퍼 2종(집합·
// 다중집합) 계약. cli(maintain 모순 분류) 재사용 전용 — suggest 동작 불변.
package query

import "testing"

// TestBigramDicePublicVariants — 집합판 BigramDice는 내부 bigramDice 위임,
// 다중집합판 BigramDiceMultiset는 반복 bigram을 출현 수로 센다. 두 측정이
// 갈리는 입력("아아아아"/"아아" — 집합 1·다중집합 0.5)으로 의미 차이를
// 고정한다(한쪽으로 통합되면 기존 분류·제안 중 하나가 깨진다).
func TestBigramDicePublicVariants(t *testing.T) {
	if got := BigramDice("백업복구", "백업복구"); got != 1 {
		t.Errorf("집합판 동일 문자열 = %v, want 1", got)
	}
	if BigramDice("flint v1", "flint v2") != bigramDice("flint v1", "flint v2") {
		t.Error("BigramDice가 내부 bigramDice와 불일치(위임 아님)")
	}
	if got, want := BigramDice("아아아아", "아아"), 1.0; got != want {
		t.Errorf("집합판 반복 bigram = %v, want %v(반복 접힘)", got, want)
	}
	if got, want := BigramDiceMultiset("아아아아", "아아"), 0.5; got != want {
		t.Errorf("다중집합판 반복 bigram = %v, want %v(출현 수 반영)", got, want)
	}
	if got := BigramDiceMultiset("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다"); got != 1 {
		t.Errorf("다중집합판 동일 문자열 = %v, want 1", got)
	}
	if got := BigramDiceMultiset("", ""); got != 1 {
		t.Errorf("다중집합판 빈/빈 = %v, want 1(동일 취급)", got)
	}
	if got := BigramDiceMultiset("가", "가나다"); got != 0 {
		t.Errorf("다중집합판 짧은/긴 = %v, want 0(비교 불가=이질)", got)
	}
}
