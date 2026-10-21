// metrics.go — hot 출력 길이 계측(프로세스 내 통계 + meta hot_stats 영속 누적).
// 단발 CLI가 매 실행 값을 보존하도록 종료 시 persist, 시작 시 load한다.
package cli

import (
	"encoding/json"
	"sort"
	"sync"
)

// hotMaxSamples — 보관 샘플 상한. 초과 시 오래된 샘플부터 버린다.
const hotMaxSamples = 500

// hotMu — hotLens 동시 접근 보호(MCP 다중 세션 포함).
var hotMu sync.Mutex

// hotLens — hot 출력 길이(rune 수) 샘플 슬라이스.
var hotLens []int

// RecordHotLen — hot 출력 직후 글자수를 기록한다.
func RecordHotLen(n int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = append(hotLens, n)
	if over := len(hotLens) - hotMaxSamples; over > 0 {
		hotLens = hotLens[over:]
	}
}

// HotStats — calls(보관 샘플 수)·p95(근사: 오름차순 정렬 인덱스 ceil(0.95*n)-1)·max.
func HotStats() (calls int, p95 int, max int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	n := len(hotLens)
	if n == 0 {
		return 0, 0, 0
	}
	sorted := make([]int, n)
	copy(sorted, hotLens)
	sort.Ints(sorted)
	return n, hotP95(sorted), sorted[n-1]
}

// hotP95 — 정렬된 샘플에서 p95 근사값(인덱스 ceil(0.95*n)-1).
func hotP95(sorted []int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (n*95 + 99) / 100 // ceil(0.95*n)
	if idx > n {
		idx = n
	}
	return sorted[idx-1]
}

// hotStatsJSON — meta hot_stats 영속 포맷(calls·sum·p95 대표 lens).
type hotStatsJSON struct {
	Calls int   `json:"calls"`
	Sum   int   `json:"sum"`
	Lens  []int `json:"lens"`
}

// hotBase — 시작 시 로드한 직전 실행까지의 누적값(미로드·파손 시 0).
var hotBase hotStatsJSON

// LoadHotStats — 시작 시 meta hot_stats 로드. 누적 calls·p95 반환,
// 키 부재·오류·JSON 파손 시 기본값 폴백(ok=false, baseline 초기화).
func LoadHotStats(getKV func(string) (string, error)) (calls int, p95 int, ok bool) {
	v, err := getKV("hot_stats")
	hotMu.Lock()
	defer hotMu.Unlock()
	hotBase = hotStatsJSON{}
	if err != nil || v == "" {
		return 0, 0, false
	}
	var st hotStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return 0, 0, false // 파손 → 기본값 폴백
	}
	hotBase = st
	lens := make([]int, len(st.Lens))
	copy(lens, st.Lens)
	sort.Ints(lens)
	return st.Calls, hotP95(lens), true
}

// PersistHotStats — 종료 시 meta hot_stats 누적 저장.
// 직전 실행 baseline(LoadHotStats 로드분) + 현 프로세스 in-memory 합산.
// lens는 p95 대표값 용도로 상한 hotMaxSamples까지 최근 샘플 보관.
func PersistHotStats(setKV func(string, string) error) error {
	hotMu.Lock()
	cur := make([]int, len(hotLens))
	copy(cur, hotLens)
	base := hotBase
	hotMu.Unlock()

	st := hotStatsJSON{Calls: base.Calls + len(cur), Sum: base.Sum + hotSum(cur)}
	st.Lens = append(append([]int(nil), base.Lens...), cur...)
	if over := len(st.Lens) - hotMaxSamples; over > 0 {
		st.Lens = st.Lens[over:]
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return setKV("hot_stats", string(b))
}

// hotSum — 샘플 길이 합.
func hotSum(lens []int) int {
	s := 0
	for _, n := range lens {
		s += n
	}
	return s
}
