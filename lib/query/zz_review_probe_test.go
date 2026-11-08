package query

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

// 리뷰 검증용 임시 테스트 — 커밋하지 않는다.

func TestZZProbeNFDPatterns(t *testing.T) {
	// (a) NFD 분해형 토큰 — containsHangul 통과 여부
	nfdTok := norm.NFD.String("백업") // 자모 분해 U+1100...
	t.Logf("NFD 토큰 containsHangul=%v", containsHangul(nfdTok))
	sets := jamoTokenSets([]string{nfdTok})
	t.Logf("NFD 토큰 jamoTokenSets 개수=%d (NFC 토큰은 1이어야)", len(sets))
	// hay는 NFC("백업") — 분해형 bigram과 매칭되는가
	if len(sets) > 0 {
		n := jamoOverlap(sets, "백업 정책", "")
		t.Logf("NFD 토큰 ↔ NFC hay jamoOverlap=%d", n)
	}

	// (b) 1음절 토큰 bigram 수
	s1 := jamoBigramSet("백") // 초중종 3자모
	s2 := jamoBigramSet("바") // 초중 2자모
	t.Logf("1음절(종성있음) bigram=%d, (종성없음) bigram=%d", len(s1), len(s2))
	if len(s1) == 0 || len(s2) == 0 {
		t.Error("1음절 토큰 bigram 0개 — 신호 미기여")
	}

	// (c) 호환 자모 단독 토큰(U+3131 'ㄱ')
	compat := "ㄱ" // U+3131
	t.Logf("호환자모 토큰 containsHangul=%v, nfkc=%q", containsHangul(compat), nfkc(compat))
	setsC := jamoTokenSets([]string{compat})
	t.Logf("호환자모 jamoTokenSets 개수=%d", len(setsC))

	// (d) 혼합 토큰 — 한글+영어
	setsM := jamoTokenSets([]string{"db백업"})
	t.Logf("혼합 토큰 세트 bigram=%d", len(setsM[0]))

	// (e) topicMatchCount 빈 토큰 — tokenizeQuery가 빈 토큰을 안 만들지만 직접 호출 시
	t.Logf("빈 토큰 topicMatchCount=%d (Contains(t,'')=true → n 증가)", topicMatchCount([]string{""}, "아무거나"))
}

func TestZZProbeRRFTieRanks(t *testing.T) {
	saved := rrfK
	defer func() { rrfK = saved }()

	// 동률 경쟁 랭킹 확인 — 신호값 [5,5,3]: 두 동률 행은 같은 1/(k+1), 셋째는 1/(k+3)
	rrfK = 60
	v := [][]int{{5, 0, 0}, {5, 0, 0}, {3, 0, 0}}
	sc := rrfFuse(v)
	t.Logf("동률 스코어: %v — 행0=행1 ? %v", sc, sc[0] == sc[1])
	want01, want2 := 1.0/61.0, 1.0/63.0
	if sc[0] != want01 || sc[2] != want2 {
		t.Errorf("동률 랭킹 수치 불일치: got %v want [%f %f %f]", sc, want01, want01, want2)
	}

	// k=0.5 주입 스케일
	rrfK = 0.5
	sc2 := rrfFuse([][]int{{2, 0, 0}, {1, 0, 0}})
	t.Logf("k=0.5 스코어: %v", sc2)

	// 0값 미기여 — 모든 신호 0이면 스코어 전부 0
	sc3 := rrfFuse([][]int{{0, 0, 0, 0}, {0, 0, 0, 0}})
	if sc3[0] != 0 || sc3[1] != 0 {
		t.Errorf("전 0 스코어 = %v, want [0 0]", sc3)
	}

	// SetRRFK 경계: 0·음수는 무시
	rrfK = 60
	SetRRFK(0)
	if rrfK != 60 {
		t.Errorf("SetRRFK(0)이 수용됨: rrfK=%v", rrfK)
	}
	SetRRFK(-5)
	if rrfK != 60 {
		t.Errorf("SetRRFK(-5)가 수용됨: rrfK=%v", rrfK)
	}
}

func TestZZProbeSignalCorrelation(t *testing.T) {
	// topic 정합 토큰은 matchCount에도 항상 포함되는가 — 부분집합 관계 수치 확인
	tokens := []string{"백업", "절차"}
	// 행A: 두 토큰 모두 topic에 있음
	// 행B: 두 토큰 모두 summary에만 있음
	valsA := []int{fallbackMatchCount(tokens, "백업 절차 개요", "메모"), readCountOfStub(), jamoOverlap(jamoTokenSets(tokens), "백업 절차 개요", "메모"), topicMatchCount(tokens, "백업 절차 개요")}
	valsB := []int{fallbackMatchCount(tokens, "메모", "백업 절차 개요"), readCountOfStub(), jamoOverlap(jamoTokenSets(tokens), "메모", "백업 절차 개요"), topicMatchCount(tokens, "메모")}
	t.Logf("행A(topic 정합): match=%d topic=%d", valsA[0], valsA[3])
	t.Logf("행B(summary만 정합): match=%d topic=%d", valsB[0], valsB[3])
	// match 신호에서 A==B(동률)지만 topic 신호에서만 갈린다 — 2배 가중 구조
}

func readCountOfStub() int { return 0 }
