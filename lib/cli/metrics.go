// metrics.go — hot 출력 길이 계측(프로세스 내 통계, 메모리).
// 튜닝은 관측 세션 단위이므로 영속화하지 않는다.
package cli

import (
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
	idx := (n*95 + 99) / 100 // ceil(0.95*n)
	if idx > n {
		idx = n
	}
	return n, sorted[idx-1], sorted[n-1]
}
