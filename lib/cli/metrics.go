// metrics.go — hot 출력 길이 계측(프로세스 내 통계 + meta hot_stats 영속 누적).
// 단발 CLI가 매 실행 값을 보존하도록 종료 시 persist, 시작 시 load한다.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// HotStats — calls(baseline+보관 샘플 수)·p95(근사: 오름차순 정렬 인덱스
// ceil(0.95*n)-1)·max. baseline은 LoadHotStats가 로드한 직전 실행 누적분.
func HotStats() (calls int, p95 int, max int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	calls = hotBase.Calls + len(hotPending())
	if calls == 0 {
		return 0, 0, 0
	}
	lens := append(append([]int(nil), hotBase.Lens...), hotPending()...)
	sort.Ints(lens)
	if len(lens) == 0 {
		return calls, 0, 0
	}
	return calls, hotP95(lens), lens[len(lens)-1]
}

// hotPending — 아직 baseline에 합산되지 않은(미저장) in-memory 샘플.
// 호출부는 hotMu를 잡고 있어야 한다.
func hotPending() []int {
	if hotPersisted <= 0 {
		return hotLens
	}
	if hotPersisted >= len(hotLens) {
		return nil
	}
	return hotLens[hotPersisted:]
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

// hotPersisted — hotLens 선두에서 이미 baseline에 합산된(저장된) 샘플 수.
// persist 후에도 in-memory 샘플은 남기되 이중 계산에서는 제외한다.
var hotPersisted int

// LoadHotStats — 시작 시 meta hot_stats 로드. 누적 calls·p95 반환,
// 키 부재·오류·JSON 파손 시 기본값 폴백(ok=false, baseline 초기화).
// hotPersisted는 건드리지 않는다 — 이미 저장된 in-memory 샘플의 이중
// 계산 제외는 유지된다(프로세스 분리 실행에서는 어차피 0).
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
// 직전 실행 baseline(LoadHotStats 로드분) + 미저장 in-memory 샘플 합산.
// lens는 p95 대표값 용도로 상한 hotMaxSamples까지 최근 샘플 보관.
// 저장 성공 시 합산분을 baseline으로 이관해도 in-memory 샘플은 남긴다
// (이중 계산은 hotPersisted 오프셋으로 제외 — RecordHotLen 직후 조회 대응).
func PersistHotStats(setKV func(string, string) error) error {
	hotMu.Lock()
	cur := append([]int(nil), hotPending()...)
	snapLen := len(hotLens)
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
	if err := setKV("hot_stats", string(b)); err != nil {
		return err
	}
	hotMu.Lock()
	hotBase = st
	if hotPersisted < snapLen {
		hotPersisted = snapLen
	}
	hotMu.Unlock()
	return nil
}

// hotSum — 샘플 길이 합.
func hotSum(lens []int) int {
	s := 0
	for _, n := range lens {
		s += n
	}
	return s
}

// loadHotStatsFromMeta — 명령 시작 시 meta hot_stats 로드(wiring).
// 조회 실패·부재는 LoadHotStats 폴백과 동일(경고 없이 baseline 0).
func loadHotStatsFromMeta(ctx context.Context, st Store) {
	LoadHotStats(func(key string) (string, error) {
		rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
		if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
			return "", err
		}
		return fmt.Sprint(rows[0][0]), nil
	})
}

// readStatsJSON — meta read_stats 영속 포맷(T56 제로히트 계측).
type readStatsJSON struct {
	Calls int `json:"calls"`
	Zero  int `json:"zero"`
}

// LoadReadStats — meta read_stats 로드. 키 부재·오류·JSON 파손 시 기본값
// 폴백(ok=false, 0/0).
func LoadReadStats(getKV func(string) (string, error)) (calls int, zero int, ok bool) {
	v, err := getKV("read_stats")
	if err != nil || v == "" {
		return 0, 0, false
	}
	var st readStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return 0, 0, false // 파손 → 기본값 폴백
	}
	return st.Calls, st.Zero, true
}

// BumpReadStats — read 호출 1회 누적. 기존 read_stats는 getKV로 읽어
// 합산 후 setKV로 저장(hot_stats 패턴). 파손·부재는 0에서 재기산한다.
func BumpReadStats(zeroHits bool, getKV func(string) (string, error), setKV func(string, string) error) error {
	calls, zero, _ := LoadReadStats(getKV) // 파손·부재 폴백 후 재누적
	st := readStatsJSON{Calls: calls + 1}
	if zeroHits {
		st.Zero = zero + 1
	} else {
		st.Zero = zero
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return setKV("read_stats", string(b))
}

// readZeroPct — zero 비율(정수 백분율). calls 0이면 0(나눗셈 가드).
func readZeroPct(calls, zero int) int {
	if calls <= 0 {
		return 0
	}
	return zero * 100 / calls
}

// loadReadStatsFromMeta — status 노출용 meta read_stats 로드(wiring).
// 조회 실패·부재·파손은 LoadReadStats 폴백과 동일(0/0).
func loadReadStatsFromMeta(ctx context.Context, st Store) (calls int, zero int) {
	calls, zero, _ = LoadReadStats(func(key string) (string, error) {
		rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
		if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
			return "", err
		}
		return fmt.Sprint(rows[0][0]), nil
	})
	return calls, zero
}

// bumpReadStatsToMeta — read 계측 wiring: zero 히트 여부를 meta read_stats에
// 누적. 실패는 stderr 1행 이하로 조용히 무시(비간섭 §13).
func bumpReadStatsToMeta(ctx context.Context, st Store, zeroHits bool) {
	err := BumpReadStats(zeroHits,
		func(key string) (string, error) {
			rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
			if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
				return "", err
			}
			return fmt.Sprint(rows[0][0]), nil
		},
		func(key, value string) error {
			_, err := queryOnce(ctx, st,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, []any{key, value})
			return err
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[flint] read stats persist: %v\n", err)
	}
}

// persistHotStatsToMeta — 명령 종료 직전 meta hot_stats 누적 저장(wiring).
// 실패는 stderr 1행 이하로 조용히 무시(비간섭 §13 — 계측이 본말을 바꾸지 않는다).
func persistHotStatsToMeta(ctx context.Context, st Store) {
	err := PersistHotStats(func(key, value string) error {
		_, err := queryOnce(ctx, st,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, []any{key, value})
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[flint] hot stats persist: %v\n", err)
	}
}
