package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func init() {
	knowledge.RecordEchoScanFn = RecordEchoScan
	query.RecordSuggestScanFn = RecordSuggestScan
}

const hotMaxSamples = 500

var hotMu sync.Mutex

var hotLens []int

func RecordHotLen(n int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = append(hotLens, n)
	if over := len(hotLens) - hotMaxSamples; over > 0 {
		hotLens = hotLens[over:]

		if over >= hotPersisted {
			hotPersisted = 0
		} else {
			hotPersisted -= over
		}
	}
}

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

func hotPending() []int {
	if hotPersisted <= 0 {
		return hotLens
	}
	if hotPersisted >= len(hotLens) {
		return nil
	}
	return hotLens[hotPersisted:]
}

func HotP95(sorted []int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (n*95 + 99) / 100
	if idx > n {
		idx = n
	}
	return sorted[idx-1]
}

type hotStatsJSON struct {
	Calls int   `json:"calls"`
	Sum   int   `json:"sum"`
	Lens  []int `json:"lens"`
}

var hotBase hotStatsJSON

var hotPersisted int

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
		return 0, 0, false
	}
	hotBase = st
	lens := make([]int, len(st.Lens))
	copy(lens, st.Lens)
	sort.Ints(lens)
	return st.Calls, HotP95(lens), true
}

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

func ResetMetrics() {
	hotMu.Lock()
	hotLens = hotLens[:0]
	hotBase = hotStatsJSON{}
	hotPersisted = 0
	hotMu.Unlock()
	scanMu.Lock()
	scanEcho, scanSuggest = scanAcc{}, scanAcc{}
	scanLoaded = false
	scanMu.Unlock()
}

func HotLensSnapshot() []int {
	hotMu.Lock()
	defer hotMu.Unlock()
	return append([]int(nil), hotLens...)
}

func hotSum(lens []int) int {
	s := 0
	for _, n := range lens {
		s += n
	}
	return s
}

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

func LoadHotStatsFromMeta(ctx context.Context, st wire.Store) {
	getKV, _ := MetaKV(ctx, st)
	LoadHotStats(getKV)
}

var PctOfFn = func(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

func ReadZeroPct(calls, zero int) int {
	return PctOfFn(zero, calls)
}

func LoadReadStatsFromMeta(ctx context.Context, st wire.Store) (calls int, zero int) {
	rows, err := db.ReadEventsCount(ctx, st)
	if err != nil {
		return 0, 0
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &calls, &zero)
	}
	return calls, zero
}

func PersistHotStatsToMeta(ctx context.Context, st wire.Store) {
	getKV, setKV := MetaKV(ctx, st)
	if err := PersistHotStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] hot stats persist: %v\n", err)
	}
	persistScanStatsToMeta(getKV, setKV)
}

const scanMaxSamples = 500

type ScanKindStats struct {
	Calls int
	P95   int
}

type scanKindJSON struct {
	Calls   int   `json:"calls"`
	Samples []int `json:"samples"`
}

type scanStatsJSON struct {
	Echo    scanKindJSON `json:"echo"`
	Suggest scanKindJSON `json:"suggest"`
}

type scanAcc struct {
	base      scanKindJSON
	samples   []int
	persisted int
}

var scanMu sync.Mutex

var (
	scanEcho    scanAcc
	scanSuggest scanAcc
	scanLoaded  bool
)

func RecordEchoScan(d time.Duration) { recordScan(&scanEcho, d) }

func RecordSuggestScan(d time.Duration) { recordScan(&scanSuggest, d) }

func recordScan(acc *scanAcc, d time.Duration) {
	scanMu.Lock()
	defer scanMu.Unlock()
	acc.samples = append(acc.samples, int(d.Microseconds()))
	if over := len(acc.samples) - scanMaxSamples; over > 0 {
		acc.samples = acc.samples[over:]
		if over >= acc.persisted {
			acc.persisted = 0
		} else {
			acc.persisted -= over
		}
	}
}

func ScanStats() (echo, suggest ScanKindStats) {
	scanMu.Lock()
	defer scanMu.Unlock()
	return scanKindStatsOf(&scanEcho), scanKindStatsOf(&scanSuggest)
}

func scanKindStatsOf(acc *scanAcc) ScanKindStats {
	pend := scanPending(acc)
	calls := acc.base.Calls + len(pend)
	if calls == 0 {
		return ScanKindStats{}
	}
	all := append(append([]int(nil), acc.base.Samples...), pend...)
	sort.Ints(all)
	return ScanKindStats{Calls: calls, P95: HotP95(all)}
}

func scanPending(acc *scanAcc) []int {
	if acc.persisted <= 0 {
		return acc.samples
	}
	if acc.persisted >= len(acc.samples) {
		return nil
	}
	return acc.samples[acc.persisted:]
}

func LoadScanStats(getKV func(string) (string, error)) bool {
	v, err := getKV("scan_stats")
	scanMu.Lock()
	defer scanMu.Unlock()
	scanEcho.base, scanSuggest.base = scanKindJSON{}, scanKindJSON{}
	if err != nil || v == "" {
		return false
	}
	var st scanStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return false
	}
	scanEcho.base, scanSuggest.base = sanitizeScanKind(st.Echo), sanitizeScanKind(st.Suggest)
	return true
}

func sanitizeScanKind(k scanKindJSON) scanKindJSON {
	if k.Calls < 0 {
		k.Calls = 0
	}
	if len(k.Samples) > 0 {
		kept := k.Samples[:0]
		for _, s := range k.Samples {
			if s >= 0 {
				kept = append(kept, s)
			}
		}
		k.Samples = kept
	}
	return k
}

func PersistScanStats(setKV func(string, string) error) error {
	scanMu.Lock()
	echo := scanPersistKind(&scanEcho)
	suggest := scanPersistKind(&scanSuggest)
	scanMu.Unlock()

	st := scanStatsJSON{Echo: echo.st, Suggest: suggest.st}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := setKV("scan_stats", string(b)); err != nil {
		return err
	}
	scanMu.Lock()
	if echo.snapLen > scanEcho.persisted {
		scanEcho.persisted = echo.snapLen
	}
	if suggest.snapLen > scanSuggest.persisted {
		scanSuggest.persisted = suggest.snapLen
	}
	scanEcho.base, scanSuggest.base = echo.st, suggest.st
	scanMu.Unlock()
	return nil
}

type scanSnapshot struct {
	st      scanKindJSON
	snapLen int
}

func scanPersistKind(acc *scanAcc) scanSnapshot {
	cur := append([]int(nil), scanPending(acc)...)
	out := scanSnapshot{snapLen: len(acc.samples)}
	out.st = scanKindJSON{Calls: acc.base.Calls + len(cur)}
	out.st.Samples = append(append([]int(nil), acc.base.Samples...), cur...)
	if over := len(out.st.Samples) - scanMaxSamples; over > 0 {
		out.st.Samples = out.st.Samples[over:]
	}
	return out
}

func persistScanStatsToMeta(getKV func(string) (string, error), setKV func(string, string) error) {
	scanMu.Lock()
	loaded := scanLoaded
	scanLoaded = true
	scanMu.Unlock()
	if !loaded {
		LoadScanStats(getKV)
	}
	if err := PersistScanStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] scan stats persist: %v\n", err)
	}
}

func ScanStatsDirty() bool {
	scanMu.Lock()
	defer scanMu.Unlock()
	return len(scanPending(&scanEcho)) > 0 || len(scanPending(&scanSuggest)) > 0
}

var scanFlushBackoff = []time.Duration{25 * time.Millisecond, 100 * time.Millisecond, 400 * time.Millisecond}

func PersistScanStatsToMeta(ctx context.Context, st wire.Store) {
	if !ScanStatsDirty() {
		return
	}
	getKV, setKV := MetaKV(ctx, st)
	scanMu.Lock()
	loaded := scanLoaded
	scanLoaded = true
	scanMu.Unlock()
	if !loaded {
		LoadScanStats(getKV)
	}
	for i := 0; ; i++ {
		if err := PersistScanStats(setKV); err == nil {
			return
		} else if errors.Is(err, store.ErrServerDown) || i >= len(scanFlushBackoff) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(scanFlushBackoff[i]):
		}
	}
}

var ScanStatsFromMetaFn = ScanStatsFromMeta

func ScanStatsFromMeta(getKV func(string) (string, error)) (echo, suggest ScanKindStats) {
	LoadScanStats(getKV)
	return ScanStats()
}

func KnowledgeSummaryLens(ctx context.Context, st wire.Store) map[int]int {
	rows, err := db.KnowledgeCount(ctx, st)
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

func legacyHotStats(ctx context.Context, st wire.Store) (calls, p95 int) {
	rows, err := db.MetaLookup(ctx, st)
	if err != nil {
		return 0, 0
	}
	for _, r := range rows {
		if len(r) < 2 || fmt.Sprint(r[0]) != "hot_stats" {
			continue
		}
		var hs struct {
			Calls int   `json:"calls"`
			Lens  []int `json:"lens"`
		}
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &hs) != nil {
			continue
		}
		lens := append([]int(nil), hs.Lens...)
		sort.Ints(lens)
		return hs.Calls, HotP95(lens)
	}
	return 0, 0
}

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
