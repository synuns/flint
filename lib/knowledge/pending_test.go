package knowledge

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
)

func naiveMatch(key string, actives [][]any) []int {
	prefix := render.TruncateRunes(key, 12)
	if prefix == "" {
		return nil
	}
	var out []int
	for _, r := range actives {
		if len(r) < 2 || r[0] == nil {
			continue
		}
		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(r[0]), &id); n == 1 &&
			render.TruncateRunes(topicSimilarityKey(fmt.Sprint(r[1])), 12) == prefix {
			out = append(out, id)
		}
	}
	return out
}

func TestPendingPrefixIndexEquivalence(t *testing.T) {
	actives := [][]any{
		{int32(4), "표 파이프 이스케이프 규칙(합성 데모) 기존 활성"},
		{int32(5), "session[claude] 표 파이프 이스케이프 규칙(합성 데모) 신규"},
		{int32(6), "세션[gpt] 표 파이프 이스케이프 규칙(합성 데모) 국문"},
		{int32(7), "전혀 다른 주제의 활성 지식"},
		{int32(8), ""},
		{nil, "nil id 행"},
		{"not-a-number", "id 파싱 실패 행"},
		{int32(9)},
		{int32(10), nil},
		{int32(11), "session[claude]"},
	}
	keys := []string{
		"표 파이프 이스케이프 규칙(합성 데모) 신규 후보",
		"session[claude] 표 파이프 이스케이프 규칙(합성 데모) 후보",
		"세션[gpt] 표 파이프 이스케이프 규칙(합성 데모) 후보",
		"전혀 다른 주제의 활성 지식 후보",
		"",
		"짧",
		"<nil>",
		"session[claude]",
		strings.Repeat("경계", 7),
	}

	boundary := append(actives, []any{int32(12), strings.Repeat("경계", 7) + "여기서부터다름"},
		[]any{int32(13), strings.Repeat("경계", 6) + "다른뒤"})
	for _, tt := range []struct {
		name    string
		actives [][]any
	}{
		{"기본 셋", actives},
		{"절단 경계", boundary},
		{"빈 active", nil},
	} {
		idx := buildTopicPrefixIndex(tt.actives)
		for _, k := range keys {
			got := idx.match(k)
			want := naiveMatch(k, tt.actives)
			if len(got) != len(want) {
				t.Errorf("%s key=%q ids=%v, 스캔 기준=%v", tt.name, k, got, want)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%s key=%q ids[%d]=%d, 스캔 기준=%d", tt.name, k, i, got[i], want[i])
				}
			}
		}
	}
}

func TestPendingVerdictIdentical(t *testing.T) {
	th := store.Thresholds{PendingAgeDays: 7, PendingPromoteDays: 3, PendingPromoteMinRunes: 40}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	actives := [][]any{
		{int32(4), "배치 삽입 성능 개선(합성 데모)"},
		{int32(5), "session[claude] 배치 삽입 성능 개선(합성 데모) 후속"},
		{int32(6), "무관 주제"},
	}
	old, recent := now.Add(-20*24*time.Hour), now.Add(-24*time.Hour)
	items := []pendingItem{
		{ID: 21, Topic: "배치 삽입 성능 개선(합성 데모) 후보", Summary: "충분히 긴 합성 요약 본문 — 승격 아님 병합 우선 검증", ValidAt: recent},
		{ID: 22, Topic: "신규 주제의 최근 행", Summary: "본문이 40룬을 넘는 최근 행 — 승격 판정이 나와야 하는 합성 요약 문장이다", ValidAt: recent},
		{ID: 23, Topic: "오래된 주제", Summary: "만료 우선 — 병합보다 expire가 앞선다", ValidAt: old},
		{ID: 24, Topic: "짧은 최근", Summary: "짧다", ValidAt: recent},
		{ID: 25, Topic: "session[claude] 배치 삽입 성능 개선(합성 데모)", Summary: "세션 접두 제거 후 병합 근사 검증용 합성 행", ValidAt: recent},
	}
	idx := buildTopicPrefixIndex(actives)
	for _, it := range items {
		got := verdictFor(it, th, now, idx)
		want := naiveVerdictFor(it, th, now, actives)
		if got != want {
			t.Errorf("id=%d 판정 불일치:\n got=%+v\nwant=%+v", it.ID, got, want)
		}
	}
}

func naiveVerdictFor(it pendingItem, th store.Thresholds, now time.Time, actives [][]any) pendingVerdict {
	age := now.Sub(it.ValidAt)
	if d := th.PendingAgeDays; d > 0 && age > time.Duration(d)*24*time.Hour {
		return pendingVerdict{"expire", fmt.Sprintf("%dd elapsed (threshold %dd) — expire review: maintain --invalidate --id %d --reason review", int(age.Hours()/24), d, it.ID)}
	}
	if sims := naiveMatch(topicSimilarityKey(it.Topic), actives); len(sims) > 0 {
		return pendingVerdict{"merge", fmt.Sprintf("similar active %d (id %s) — review approve --id %d --supersede OLD",
			len(sims), joinInts(sims), it.ID)}
	}
	if age <= time.Duration(th.PendingPromoteDays)*24*time.Hour && len([]rune(it.Summary)) >= th.PendingPromoteMinRunes {
		return pendingVerdict{"promote", "recent write with sufficient body — approve --id " + fmt.Sprint(it.ID)}
	}
	return pendingVerdict{"hold", "short body or under observation — hold until next review"}
}

func TestPendingPrefixIndexSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("short 모드 제외 — 시간 비교 벤치")
	}
	const nActive, nPending = 4000, 400
	actives := make([][]any, nActive)
	topics := make([]string, nPending)
	for i := 0; i < nActive; i++ {
		actives[i] = []any{int32(i + 1), fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터", i)}
	}
	for i := range topics {
		topics[i] = fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터 후보", i%nActive)
	}
	naiveStart := time.Now()
	naiveHits := 0
	for _, k := range topics {
		naiveHits += len(naiveMatch(k, actives))
	}
	naiveDur := time.Since(naiveStart)
	idxStart := time.Now()
	idx := buildTopicPrefixIndex(actives)
	idxHits := 0
	for _, k := range topics {
		idxHits += len(idx.match(k))
	}
	idxDur := time.Since(idxStart)
	if naiveHits == 0 || idxHits != naiveHits {
		t.Fatalf("판정 동일성 깨짐 — 스캔=%d 인덱스=%d", naiveHits, idxHits)
	}
	if idxDur >= naiveDur {
		t.Errorf("인덱스 경로가 더 느림 — 스캔=%v 인덱스=%v", naiveDur, idxDur)
	}
	t.Logf("합성 %d×%d: 스캔=%v 인덱스=%v (%.0fx)", nActive, nPending, naiveDur, idxDur, float64(naiveDur)/float64(idxDur))
}

func BenchmarkPendingMatchNaive(b *testing.B) {
	actives := make([][]any, 5000)
	for i := range actives {
		actives[i] = []any{int32(i + 1), fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터", i)}
	}
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터 후보", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		naiveMatch(keys[i%len(keys)], actives)
	}
}

func BenchmarkPendingMatchIndexBuild(b *testing.B) {
	actives := make([][]any, 5000)
	for i := range actives {
		actives[i] = []any{int32(i + 1), fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터", i)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buildTopicPrefixIndex(actives)
	}
}

func BenchmarkPendingMatchIndexLookup(b *testing.B) {
	actives := make([][]any, 5000)
	for i := range actives {
		actives[i] = []any{int32(i + 1), fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터", i)}
	}
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = fmt.Sprintf("합성 활성 지식 주제 %06d — 데모 데이터 후보", i)
	}
	idx := buildTopicPrefixIndex(actives)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.match(keys[i%len(keys)])
	}
}
