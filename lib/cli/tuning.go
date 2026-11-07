// tuning.go — v0.23.0(D③): `flint tuning` — TUNING §2 임계값 키의
// 조회·설정·리셋 인터페이스. 이전까지는 psql로 meta를 직접 갱신해야
// 했다(관리 진입장벽). 계약: 한 줄 JSON, exit 0/1/3(3=키·값 오류).
// 값 검증은 실경로(loadThresholds→ParseThresholds)로 — 파싱 폴백이
// 일어나면(값이 반영 안 되면) 거부한다(문서와 단일 소스).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/store"
)

func init() { Register("tuning", runTuning) }

// tuningKeyInfo — 노출 키 메타(값 직렬화는 필드별 Get).
type tuningKeyInfo struct {
	Key      string
	Default  string
	Consumer string // 소비 경로 표시(v0.24.0 R4b — TUNING 표와 상호 참조)
	Get      func(store.Thresholds) string
}

// tuningKeys — TUNING §2 전체 키(가나다 아닌 선언 순 = 문서 순서와
// 상관없이 결정적 정렬은 아래 list에서 수행).
func tuningKeys() []tuningKeyInfo {
	d := store.DefaultThresholds()
	i := func(v int) string { return strconv.Itoa(v) }
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return []tuningKeyInfo{
		{"pending_max", i(d.PendingMax), "maintain --check", func(t store.Thresholds) string { return i(t.PendingMax) }},
		{"dup_susp_max", i(d.DupSuspMax), "maintain --check", func(t store.Thresholds) string { return i(t.DupSuspMax) }},
		{"conf_floor", f(d.ConfFloor), "write 판정", func(t store.Thresholds) string { return f(t.ConfFloor) }},
		{"stale_sessions", i(d.StaleSessions), "done", func(t store.Thresholds) string { return i(t.StaleSessions) }},
		{"stale_days", i(d.StaleDays), "maintain --stale·report", func(t store.Thresholds) string { return i(t.StaleDays) }},
		{"stall_hot_h", i(d.StallHotH), "doctor·maintain", func(t store.Thresholds) string { return i(t.StallHotH) }},
		{"stall_write_h", i(d.StallWriteH), "doctor·maintain", func(t store.Thresholds) string { return i(t.StallWriteH) }},
		{"suggest_min_score", f(d.SuggestMinScore), "read 제안", func(t store.Thresholds) string { return f(t.SuggestMinScore) }},
		{"suggest_scan_cap", i(d.SuggestScanCap), "read 제안", func(t store.Thresholds) string { return i(t.SuggestScanCap) }},
		{"conflict_dup_threshold", f(d.ConflictDupThreshold), "maintain 분류", func(t store.Thresholds) string { return f(t.ConflictDupThreshold) }},
		{"backup_age_warn_h", i(d.BackupAgeWarnH), "doctor backup-age", func(t store.Thresholds) string { return i(t.BackupAgeWarnH) }},
		{"zero_near_min_read", i(d.ZeroNearMinRead), "doctor zero-near", func(t store.Thresholds) string { return i(t.ZeroNearMinRead) }},
		{"zero_near_warn_pct", i(d.ZeroNearWarnPct), "doctor zero-near", func(t store.Thresholds) string { return i(t.ZeroNearWarnPct) }},
		{"pending_age_days", i(d.PendingAgeDays), "doctor pending-age", func(t store.Thresholds) string { return i(t.PendingAgeDays) }},
		{"recall_warn_writes", i(d.RecallWarnWrites), "done 회상 경고", func(t store.Thresholds) string { return i(t.RecallWarnWrites) }},
		{"dead_days", i(d.DeadDays), "report effect", func(t store.Thresholds) string { return i(t.DeadDays) }},
		{"pair_window_minutes", i(d.PairWindowMin), "report effect", func(t store.Thresholds) string { return i(t.PairWindowMin) }},
		{"chain_window_hours", i(d.ChainWindowH), "report effect", func(t store.Thresholds) string { return i(t.ChainWindowH) }},
		{"events_retention_days", i(d.EventsRetentionD), "계측 정리", func(t store.Thresholds) string { return i(t.EventsRetentionD) }},
		{"hot_events_retention_days", i(d.HotRetentionD), "계측 정리", func(t store.Thresholds) string { return i(t.HotRetentionD) }},
		{"events_volume_warn", i(d.EventsVolumeWarn), "doctor event-volume", func(t store.Thresholds) string { return i(t.EventsVolumeWarn) }},
		{"drill_max_days", i(d.DrillMaxDays), "maintain --check", func(t store.Thresholds) string { return i(t.DrillMaxDays) }},
		{"daily_retention_days", i(d.DailyRetentionD), "보존·report/status 창", func(t store.Thresholds) string { return i(t.DailyRetentionD) }},
	}
}

// tuningMetaKey — thresholds 레지스트리 밖 meta 관리 키(v0.24.0 R1d:
// 관리 경로 단일화 — hot_budget·backup_keep·exposure_profile 등).
type tuningMetaKey struct {
	Key      string
	Default  string
	Validate func(val string) error
}

// tuningMetaKeys — 관리면 확장 키. 검증은 각 타입 규칙(정수·열거).
func tuningMetaKeys() []tuningMetaKey {
	intPositive := func(key string) func(string) error {
		return func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n <= 0 {
				return fmt.Errorf("%s: 양의 정수 필요", key)
			}
			return nil
		}
	}
	return []tuningMetaKey{
		{Key: "hot_budget", Default: "2000", Validate: intPositive("hot_budget")},
		{Key: "backup_keep", Default: "14", Validate: intPositive("backup_keep")},
		{Key: "backup_interval_h", Default: "24", Validate: intPositive("backup_interval_h")},
		{Key: "exposure_profile", Default: "normal", Validate: func(v string) error {
			if v != "normal" && v != "minimal" {
				return fmt.Errorf("exposure_profile: normal|minimal 필요")
			}
			return nil
		}},
		// v0.24.0 R4a: 개인 어휘(빈 허용=미설정, 콤마 목록).
		{Key: "vocab_tags", Default: "", Validate: vocabListValidator("vocab_tags")},
		{Key: "vocab_domains", Default: "", Validate: vocabListValidator("vocab_domains")},
	}
}

// vocabListValidator — 콤마 목록(빈 허용, 원소는 정규화 가능 문자열).
func vocabListValidator(key string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return nil
		}
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "" {
				return fmt.Errorf("%s: 빈 원소 금지(콤마 목록)", key)
			}
		}
		return nil
	}
}

// tuningKeyMap — 키 → 메타(빠른 조회).
func tuningKeyMap() map[string]tuningKeyInfo {
	out := make(map[string]tuningKeyInfo, 22)
	for _, k := range tuningKeys() {
		out[k.Key] = k
	}
	return out
}

// tuningKeyList — 결정적 정렬 키 목록.
func tuningKeyList() []string {
	out := make([]string, 0, 22)
	for _, k := range tuningKeys() {
		out = append(out, k.Key)
	}
	sort.Strings(out)
	return out
}

// runTuning — 하위명령 디스패치. 인자 없음 = list.
func runTuning(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	cmd := "list"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "list":
		return tuningList(ctx, st, stdout)
	case "get":
		if len(args) != 2 {
			return tuningUsage(stdout, "get <key>")
		}
		return tuningGet(ctx, st, args[1], stdout)
	case "set":
		if len(args) != 3 {
			return tuningUsage(stdout, "set <key> <value>")
		}
		return tuningSet(ctx, st, args[1], args[2], stdout)
	case "reset":
		if len(args) == 2 && args[1] == "--all" {
			return tuningResetAll(ctx, st, stdout)
		}
		if len(args) != 2 {
			return tuningUsage(stdout, "reset <key> | reset --all")
		}
		return tuningReset(ctx, st, args[1], stdout)
	}
	return tuningUsage(stdout, "list | get <key> | set <key> <value> | reset <key> | reset --all")
}

func tuningUsage(stdout io.Writer, usage string) int {
	fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "tuning", Expect: usage}))
	return 3
}

// tuningCurrent — 현재 값(오버라이드 여부 판별용 원시 메타 값 포함).
func tuningCurrent(ctx context.Context, st Store) (store.Thresholds, map[string]string, error) {
	// v0.23.0 리뷰 ⑦: 단일 쿼리 — 같은 키 집합을 한 번 읽어 원시 맵과
	// 파싱 결과를 모두 도출한다(loadThresholds와 이중 조회 제거).
	raw := map[string]string{}
	all := append(append([]string{}, thresholdsKeys()...), "hot_budget", "backup_keep", "backup_interval_h", "exposure_profile", "vocab_tags", "vocab_domains")
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('`+strings.Join(all, "','")+`')`, nil)
	if err != nil {
		return store.Thresholds{}, raw, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			k := fmt.Sprint(r[0])
			raw[k] = fmt.Sprint(r[1])
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	th, perr := store.ParseThresholds(kv)
	if perr != nil {
		return th, raw, perr
	}
	th = applyThresholdsFallback(th)
	return th, raw, nil
}

// tuningList — 전체 키의 현재값·기본값·소스 한 줄 JSON.
func tuningList(ctx context.Context, st Store, stdout io.Writer) int {
	th, raw, err := tuningCurrent(ctx, st)
	if err != nil { // v0.23.0 리뷰 ①: 영값 출력이 아니라 오류로.
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	km := tuningKeyMap()
	type row struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Default  string `json:"default"`
		Consumer string `json:"consumer,omitempty"`
		Override bool   `json:"override"`
	}
	out := make([]row, 0, 22)
	for _, k := range tuningKeyList() {
		info := km[k]
		value := info.Get(th)
		_, overridden := raw[k]
		out = append(out, row{Key: k, Value: value, Default: info.Default, Consumer: info.Consumer, Override: overridden})
	}
	// v0.24.0 R1d: thresholds 밖 meta 관리 키 병합 노출.
	for _, mk := range tuningMetaKeys() {
		value, overridden := mk.Default, false
		if v, ok := raw[mk.Key]; ok {
			value, overridden = v, true
		}
		out = append(out, row{Key: mk.Key, Value: value, Default: mk.Default, Override: overridden})
	}
	b, err := json.Marshal(map[string]any{"keys": out})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// tuningGet — 단일 키 상태.
func tuningGet(ctx context.Context, st Store, key string, stdout io.Writer) int {
	info, ok := tuningKeyMap()[key]
	if !ok {
		return tuningUnknownKey(stdout, key)
	}
	th, raw, err := tuningCurrent(ctx, st)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	_, overridden := raw[key] // ⑨: list와 동일하게 bool.
	b, _ := json.Marshal(map[string]any{
		"key": key, "value": info.Get(th), "default": info.Default, "override": overridden,
	})
	fmt.Fprintln(stdout, string(b))
	return 0
}

// tuningSet — 값 검증(실경로 파싱으로 반영 확인) 후 meta upsert.
func tuningSet(ctx context.Context, st Store, key, val string, stdout io.Writer) int {
	// v0.24.0 R1d: thresholds 밖 meta키 — 자체 검증 후 upsert.
	for _, mk := range tuningMetaKeys() {
		if mk.Key == key {
			if err := mk.Validate(val); err != nil {
				fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: key, Expect: err.Error()}))
				return 3
			}
			return tuningMetaUpsert(ctx, st, key, val, stdout)
		}
	}
	info, ok := tuningKeyMap()[key]
	if !ok {
		return tuningUnknownKey(stdout, key)
	}
	// 검증 ①: 후보 값을 파싱해 필드에 그대로 반영되는지(폴백 아님).
	cand, err := store.ParseThresholds([][2]any{{key, val}})
	if err == nil && info.Get(cand) != val {
		err = fmt.Errorf("파싱 폴백 — 무효 값")
	}
	if err != nil || info.Get(cand) != val {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{
			Field:  key,
			Expect: "유효 값(기본 " + info.Default + ", TUNING §2 참조)",
		}))
		return 3
	}
	// 검증 ②(v0.23.0 리뷰 ②): 폴백 키는 0이 "미설정" 의미라 저장해도
	// 소비자가 기본값으로 되돌린다 — 표현 불가 값 사전 거부.
	if val == "0" && zeroMeansUnset(key) {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{
			Field:  key,
			Expect: "0 불가(미설정 의미) — 기본 복귀는 reset",
		}))
		return 3
	}
	if _, err := queryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		[]any{key, val}); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"set","key":`+quoteJSON(key)+`,"value":`+quoteJSON(val)+"}")
	return 0
}

// tuningReset — 오버라이드 삭제(기본값 복귀).
func tuningReset(ctx context.Context, st Store, key string, stdout io.Writer) int {
	_, inTh := tuningKeyMap()[key]
	inMeta := false
	for _, mk := range tuningMetaKeys() {
		if mk.Key == key {
			inMeta = true
		}
	}
	if !inTh && !inMeta {
		return tuningUnknownKey(stdout, key)
	}
	if _, err := queryOnce(ctx, st,
		`DELETE FROM meta WHERE key = $1`, []any{key}); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"reset","key":`+quoteJSON(key)+"}")
	return 0
}

func tuningUnknownKey(stdout io.Writer, key string) int {
	fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "tuning", Expect: "알려진 키: " + strings.Join(tuningKeyList()[:5], ", ") + ", …"}))
	return 3
}

// zeroMeansUnset — loadThresholds가 0을 미설정으로 폴백하는 키(v0.5
// 4키 제외 — 0이 실값으로 유효). 리뷰 ②의 표현 불가 집합.
func zeroMeansUnset(key string) bool {
	switch key {
	case "pending_max", "dup_susp_max", "conf_floor", "stale_sessions":
		return false
	}
	return true
}

// tuningMetaUpsert — 검증 완료 값의 meta upsert 공통(§2·meta키 동일 경로).
func tuningMetaUpsert(ctx context.Context, st Store, key, val string, stdout io.Writer) int {
	if _, err := queryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		[]any{key, val}); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"set","key":`+quoteJSON(key)+`,"value":`+quoteJSON(val)+"}")
	return 0
}

// tuningResetAll — 전체 오버라이드 일괄 삭제(v0.24.0 R4b: 22+왕복 → 1회).
// thresholds 23키 + meta 관리 6키 전부 — 리셋된 키 목록을 합산 JSON으로.
func tuningResetAll(ctx context.Context, st Store, stdout io.Writer) int {
	all := append(append([]string{}, thresholdsKeys()...), "hot_budget", "backup_keep", "backup_interval_h", "exposure_profile", "vocab_tags", "vocab_domains")
	qmarks := make([]string, len(all))
	args := make([]any, len(all))
	for i, k := range all {
		qmarks[i] = "$" + strconv.Itoa(i+1)
		args[i] = k
	}
	if _, err := queryOnce(ctx, st,
		`DELETE FROM meta WHERE key IN (`+strings.Join(qmarks, ",")+`)`, args); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"reset-all","keys":`+strconv.Itoa(len(all))+"}")
	return 0
}
