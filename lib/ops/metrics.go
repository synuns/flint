// metrics.go — hot 출력 길이 계측(프로세스 내 통계 + meta hot_stats 영속 누적).
// 단발 CLI가 매 실행 값을 보존하도록 종료 시 persist, 시작 시 load한다.
// v0.40.0 T2: lib/cli에서 이동(동작·출력·exit 불변 — 식별자 대문자화).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
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
		// 절단분이 persisted 선두를 깎으면 오프셋도 함께 줄인다 —
		// 그대로 두면 절단 수만큼 pending이 누락되어 과소 보고된다
		// (v0.42.0 T5 — fr-v0400 기록 엣지). 절단이 persisted 경계를
		// 넘으면 남은 보관 샘플은 전량 미저장 → 오프셋 0.
		if over >= hotPersisted {
			hotPersisted = 0
		} else {
			hotPersisted -= over
		}
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
	return calls, HotP95(lens), lens[len(lens)-1]
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

// HotP95 — 정렬된 샘플에서 p95 근사값(인덱스 ceil(0.95*n)-1).
// 내보내기: cli report.go 효과 지표 산출이 소비(T3 이동 전 별칭 경유).
func HotP95(sorted []int) int {
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
	return st.Calls, HotP95(lens), true
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

// ResetMetrics — 프로세스 내 통계 초기화(테스트 격리 지원 — cli metrics_test
// resetMetrics 관례 승계). 영속 baseline도 함께 지운다.
func ResetMetrics() {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = hotLens[:0]
	hotBase = hotStatsJSON{}
	hotPersisted = 0
}

// HotLensSnapshot — 보관 샘플 스냅샷(테스트 단언 지원 — cli metrics_test
// TestHotRecordsRuneLen 관례 승계). 복사본 반환 — 직접 참조 없이 읽는다.
func HotLensSnapshot() []int {
	hotMu.Lock()
	defer hotMu.Unlock()
	return append([]int(nil), hotLens...)
}

// hotSum — 샘플 길이 합.
func hotSum(lens []int) int {
	s := 0
	for _, n := range lens {
		s += n
	}
	return s
}

// MetaKV — meta 테이블 getKV/setKV 클로저 쌍 조립. hot·read 계측 wiring
// 4곳에 복제되던 인라인 쿼리를 통일한다. getKV는 키 부재 시 ("", nil),
// 전송 오류는 err 그대로(호출부 폴백·와이프 방지 판단에 쓰인다).
// v0.34.3 T2: 질의 본체는 meta_kv 단일 진입(wire.GetMetaKVErr·wire.SetMetaKV)
// 에 위임 — 이 클로저는 오류 구분 계약만 유지한다. knowledge가 자체
// 동일 조립을 두는 것과 같은 패턴(동반 이동분 — cli는 별칭 경유).
func MetaKV(ctx context.Context, st wire.Store) (func(string) (string, error), func(string, string) error) {
	getKV := func(key string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return "", err
		}
		return v, nil
	}
	setKV := func(key, value string) error {
		return wire.SetMetaKV(ctx, st, key, value)
	}
	return getKV, setKV
}

// LoadHotStatsFromMeta — 명령 시작 시 meta hot_stats 로드(wiring).
// 조회 실패·부재는 LoadHotStats 폴백과 동일(경고 없이 baseline 0).
func LoadHotStatsFromMeta(ctx context.Context, st wire.Store) {
	getKV, _ := MetaKV(ctx, st)
	LoadHotStats(getKV)
}

// PctOfFn — 정수 백분율(버림) 단일 소스 seam. 기본값은 cli report.go pctOf와
// 등가 공식(ops 단독 실행 결함 방지), cli init이 pctOf를 주입해 운영은
// 단일 소스를 유지한다(T3 report 이동 후 직접 호출 수렴 — QuoteJSONFn 관례).
var PctOfFn = func(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// ReadZeroPct — zero 비율(정수 백분율). calls 0이면 0(나눗셈 가드).
// v0.19.0: 반올림 본체는 pctOf(단일 소스 — effect 지표와 버림 방식
// 일관)에 위임한다(PctOfFn seam 경유).
func ReadZeroPct(calls, zero int) int {
	return PctOfFn(zero, calls)
}

// LoadReadStatsFromMeta — status 노출용 read 계측 로드(wiring).
// v0.42.0 T2: meta read_stats blob 폐지 — read_events 파생 산출로 전환했다
// (함수명은 cli 별칭 호환 유지). 누적 범위는 events_retention_days 보존창.
// 조회 실패·테이블 부재·빈 창은 0/0(status를 깨지 않는다 — 구 폴백과 동일).
func LoadReadStatsFromMeta(ctx context.Context, st wire.Store) (calls int, zero int) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*), count(CASE WHEN zero THEN 1 END) FROM read_events`, nil)
	if err != nil {
		return 0, 0
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &calls, &zero)
	}
	return calls, zero
}

// PersistHotStatsToMeta — 명령 종료 직전 meta hot_stats 누적 저장(wiring).
// 실패는 stderr 1행 이하로 조용히 무시(비간섭 §13 — 계측이 본말을 바꾸지 않는다).
func PersistHotStatsToMeta(ctx context.Context, st wire.Store) {
	_, setKV := MetaKV(ctx, st)
	if err := PersistHotStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] hot stats persist: %v\n", err)
	}
}

// KnowledgeSummaryLens — knowledge 전체의 id→요약 길이(바이트) 맵.
// 토큰 회계의 평균 기록/요약 바이트(cost savings 하한·hit 주입 원가)와
// hot 주입 바이트 파생(DerivedHotStats)의 공용 원천. 조회 실패·매핑 불가
// 칸은 조용히 스킵(§13 — 계측 강등).
func KnowledgeSummaryLens(ctx context.Context, st wire.Store) map[int]int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, length(summary) FROM knowledge`, nil)
	if err != nil {
		return nil
	}
	out := map[int]int{}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		if n, ok := render.CellToInt(r[1]); ok {
			out[id] = n
		}
	}
	return out
}

// DerivedHotStats — v1.2.0 T3: hot 계측 파생 산출(spec §1.3 hot_stats blob
// 폐지의 소비 전환). 호출 수는 knowledge_reads kind='hot'의 호출 그룹 수
// (한 호출의 id 전수가 같은 ts·session으로 남는다 — DISTINCT ts, session),
// p95는 hot_events 주입 ids × knowledge 요약 길이의 사건별 합 분포
// (nearest-rank — HotP95와 동일 공식, 보존창은 PruneReadEvents가 관리).
// kreads·hot_events 어느 쪽도 데이터가 없는(업그레이드 직후) 동안은 구
// hot_stats blob을 승계 읽기 폴백으로 보고한다(전환 전 값 연속성 —
// 플랫 키 폴백과 같은 마이그레이션 관례). 조회 실패·파손은 0(§13).
func DerivedHotStats(ctx context.Context, st wire.Store) (calls, p95 int) {
	invocations := 0
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM (SELECT DISTINCT ts, session FROM knowledge_reads WHERE kind = 'hot') g`, nil); err == nil &&
		len(rows) > 0 && len(rows[0]) > 0 {
		if n, ok := render.CellToInt(rows[0][0]); ok {
			invocations = n
		}
	}
	var samples []int
	if evs, err := wire.QueryOnce(ctx, st, `SELECT ids FROM hot_events`, nil); err == nil {
		lens := KnowledgeSummaryLens(ctx, st)
		for _, r := range evs {
			if len(r) == 0 {
				continue
			}
			s, ok := r[0].(string)
			if !ok {
				continue
			}
			ids := knowledge.ParseIDsCSV(s)
			if len(ids) == 0 {
				continue
			}
			sum := 0
			for _, id := range ids {
				sum += lens[id]
			}
			samples = append(samples, sum)
		}
	}
	if invocations == 0 && len(samples) == 0 {
		return legacyHotStats(ctx, st)
	}
	sort.Ints(samples)
	return invocations, HotP95(samples)
}

// legacyHotStats — 구 hot_stats blob 승계 읽기(DerivedHotStats 폴백).
// collectStats(v1.2.0 이전)와 동일 질의·동일 산출 — 부재·파손은 0.
func legacyHotStats(ctx context.Context, st wire.Store) (calls, p95 int) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('hot_stats')`, nil)
	if err != nil {
		return 0, 0
	}
	for _, r := range rows {
		if len(r) < 2 || fmt.Sprint(r[0]) != "hot_stats" {
			continue // 동일 질의에 섞여 온 다른 관측 키 무시(구 collectStats 계약)
		}
		var hs struct {
			Calls int   `json:"calls"`
			Lens  []int `json:"lens"`
		}
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &hs) != nil {
			continue // 파손 → 0 유지(구 경로 계약 승계)
		}
		lens := append([]int(nil), hs.Lens...)
		sort.Ints(lens)
		return hs.Calls, HotP95(lens)
	}
	return 0, 0
}

// ReadBytesP95Of — read 표면 출력 바이트 p95(nearest-rank·표본<5는 0 —
// spec §2.3). 표본은 read_events bytes 컬럼의 기록 행(bytes>0 — 미확장
// legacy 행 0 제외), 보존창 전체(PruneReadEvents가 관리). 조회 실패는 0.
func ReadBytesP95Of(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT bytes FROM read_events WHERE bytes > 0`, nil)
	if err != nil {
		return 0
	}
	var samples []int
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if n, ok := render.CellToInt(r[0]); ok && n > 0 {
			samples = append(samples, n)
		}
	}
	return p95NearestRank(samples)
}
