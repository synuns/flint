// tuning.go — v0.23.0(D③): `flint tuning` — TUNING §2 임계값 키의
// 조회·설정·리셋 인터페이스. 이전까지는 psql로 meta를 직접 갱신해야
// 했다(관리 진입장벽). 계약: 한 줄 JSON, exit 0/1/3(3=키·값 오류).
// 값 검증은 실경로(loadThresholds→ParseThresholds)로 — 파싱 폴백이
// 일어나면(값이 반영 안 되면) 거부한다(문서와 단일 소스).
// v0.40.0 T1: lib/cli에서 이동(패키지문·내보내기만 변경 — 동작 불변).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// QuoteJSONFn — JSON 문자열 리터럴 인용 seam(cli backup.go quoteJSON —
// T3 이동 후 직접 호출 수렴).
var QuoteJSONFn func(s string) string

// TuningKeyInfo — 노출 키 메타(값 직렬화는 필드별 Get).
type TuningKeyInfo struct {
	Key      string
	Default  string
	Consumer string // 소비 경로 표시(v0.24.0 R4b — TUNING 표와 상호 참조)
	Get      func(store.Thresholds) string
}

// TuningKeys — TUNING §2 전체 키. v0.42.0 T1(§2): wire 등록표
// (ThresholdMetas)에서 파생된다 — Key·Default·Consumer(Desc)·Get(Field
// 리플렉션) 전부 등록표가 단일 소스이고, threshold_meta_test.go가
// 등록표·DefaultThresholds·ParseThresholds와의 동질성을 가드한다.
// 순서는 등록표 순(=이전 선언 순) — 결정적 정렬은 list에서 수행.
func TuningKeys() []TuningKeyInfo {
	metas := wire.ThresholdMetas()
	out := make([]TuningKeyInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, TuningKeyInfo{
			Key:      m.Key,
			Default:  thresholdFormatValue(m.Default),
			Consumer: m.Desc,
			Get:      thresholdFieldGet(m.Field),
		})
	}
	return out
}

// thresholdFormatValue — 등록표 기본값(any)의 표기 규칙(int·float64 —
// 이전 TuningKeys의 i()/f() 포맷 승계).
func thresholdFormatValue(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// thresholdFieldGet — Thresholds 필드명 → Get 클로저(리플렉션). 필드
// 부재 시 ""(프로덕션 등록표는 threshold_meta_test.go가 필드 유효성을
// 가드 — 미발생 경로).
func thresholdFieldGet(field string) func(store.Thresholds) string {
	return func(t store.Thresholds) string {
		fv := reflect.Indirect(reflect.ValueOf(t)).FieldByName(field)
		if !fv.IsValid() {
			return ""
		}
		return thresholdFormatValue(fv.Interface())
	}
}

// TuningMetaKey — thresholds 레지스트리 밖 meta 관리 키(v0.24.0 R1d:
// 관리 경로 단일화 — hot_budget·backup_keep·exposure_profile 등).
type TuningMetaKey struct {
	Key      string
	Default  string
	Validate func(val string) error
}

// TuningMetaKeys — 관리면 확장 키. 검증은 각 타입 규칙(정수·열거).
func TuningMetaKeys() []TuningMetaKey {
	intPositive := func(key string) func(string) error {
		return func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n <= 0 {
				return fmt.Errorf("%s: 양의 정수 필요", key)
			}
			return nil
		}
	}
	return []TuningMetaKey{
		{Key: "hot_budget", Default: strconv.Itoa(knowledge.DefaultHotBudget), Validate: intPositive("hot_budget")},
		{Key: "backup_keep", Default: strconv.Itoa(backup.DefaultBackupKeep), Validate: intPositive("backup_keep")},
		{Key: "backup_interval_h", Default: strconv.Itoa(backup.DefaultBackupIntervalH), Validate: intPositive("backup_interval_h")},
		{Key: "exposure_profile", Default: "normal", Validate: func(v string) error {
			if v != "normal" && v != "minimal" {
				return fmt.Errorf("exposure_profile: normal|minimal 필요")
			}
			return nil
		}},
		// v0.24.0 R4a: 개인 어휘(빈 허용=미설정, 콤마 목록).
		{Key: "vocab_tags", Default: "", Validate: vocabListValidator("vocab_tags")},
		{Key: "vocab_domains", Default: "", Validate: vocabListValidator("vocab_domains")},
		// v0.28.0(H②): 백업 암호화 옵트인 스위치(false=평문 유지 — 기존
		// 백업·restore 불변). true면 자동·수동 백업이 age 암호화된다.
		{Key: "backup_encrypt", Default: "false", Validate: func(v string) error {
			if v != "true" && v != "false" {
				return fmt.Errorf("backup_encrypt: true|false 필요")
			}
			return nil
		}},
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

// TuningKeyMap — 키 → 메타(빠른 조회).
func TuningKeyMap() map[string]TuningKeyInfo {
	out := make(map[string]TuningKeyInfo, len(wire.ThresholdsKeys())+len(TuningMetaKeys()))
	for _, k := range TuningKeys() {
		out[k.Key] = k
	}
	return out
}

// TuningKeyAll — v0.34.2 R1: thresholdsKeys(maintain.go)와
// TuningMetaKeys의 .Key 필드의 정렬 합집합(중복 제거). tuningCurrent·
// tuningResetAll이 하드코딩 6키 목록을 복제해 backup_encrypt(7번째
// meta 키)가 누락되던 드리프트의 단일 소스.
func TuningKeyAll() []string {
	all := append([]string{}, wire.ThresholdsKeys()...)
	for _, mk := range TuningMetaKeys() {
		all = append(all, mk.Key)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(all))
	for _, k := range all {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// tuningKeyList — 결정적 정렬 키 목록.
func tuningKeyList() []string {
	out := make([]string, 0, 22)
	for _, k := range TuningKeys() {
		out = append(out, k.Key)
	}
	sort.Strings(out)
	return out
}

// RunTuning — 하위명령 디스패치. 인자 없음 = list.
func RunTuning(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	cmd := "list"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "list":
		return TuningList(ctx, st, stdout)
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
			return TuningResetAll(ctx, st, stdout)
		}
		if len(args) != 2 {
			return tuningUsage(stdout, "reset <key> | reset --all")
		}
		return tuningReset(ctx, st, args[1], stdout)
	}
	return tuningUsage(stdout, "list | get <key> | set <key> <value> | reset <key> | reset --all")
}

func tuningUsage(stdout io.Writer, usage string) int {
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tuning", Expect: usage}))
	return 3
}

// TuningCurrent — 현재 값(오버라이드 여부 판별용 원시 메타 값 포함).
func TuningCurrent(ctx context.Context, st wire.Store) (store.Thresholds, map[string]string, error) {
	// v0.23.0 리뷰 ⑦: 단일 쿼리 — 같은 키 집합을 한 번 읽어 원시 맵과
	// 파싱 결과를 모두 도출한다(loadThresholds와 이중 조회 제거).
	raw := map[string]string{}
	all := TuningKeyAll()
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('`+strings.Join(all, "','")+`')`, nil)
	if err != nil {
		return store.Thresholds{}, raw, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			k := fmt.Sprint(r[0])
			// v1.0.1 감사(F4): raw 값은 list·get의 "value" 출력으로
			// 그대로 흐른다(vocab_*는 자유 문자열) — 살균 출구 경유.
			// 파싱(kv)에는 원본을 쓴다(수치 판정은 원문 기준).
			raw[k] = render.Text(fmt.Sprint(r[1]))
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	th, perr := store.ParseThresholds(kv)
	if perr != nil {
		return th, raw, perr
	}
	th = wire.ApplyThresholdsFallback(th)
	return th, raw, nil
}

// TuningList — 전체 키의 현재값·기본값·소스 한 줄 JSON.
func TuningList(ctx context.Context, st wire.Store, stdout io.Writer) int {
	th, raw, err := TuningCurrent(ctx, st)
	if err != nil { // v0.23.0 리뷰 ①: 영값 출력이 아니라 오류로.
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	km := TuningKeyMap()
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
	for _, mk := range TuningMetaKeys() {
		value, overridden := mk.Default, false
		if v, ok := raw[mk.Key]; ok {
			value, overridden = v, true
		}
		out = append(out, row{Key: mk.Key, Value: value, Default: mk.Default, Override: overridden})
	}
	b, err := json.Marshal(map[string]any{"keys": out})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// tuningGet — 단일 키 상태.
func tuningGet(ctx context.Context, st wire.Store, key string, stdout io.Writer) int {
	// v0.24.0 리뷰 ①: meta 관리 키도 get 대상(set/list/reset과 표면 일치).
	for _, mk := range TuningMetaKeys() {
		if mk.Key == key {
			th, raw, err := TuningCurrent(ctx, st)
			_ = th
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
			value, overridden := mk.Default, false
			if v, ok := raw[mk.Key]; ok {
				value, overridden = v, true
			}
			b, _ := json.Marshal(map[string]any{
				"key": key, "value": value, "default": mk.Default, "override": overridden,
			})
			fmt.Fprintln(stdout, string(b))
			return 0
		}
	}
	info, ok := TuningKeyMap()[key]
	if !ok {
		return tuningUnknownKey(stdout, key)
	}
	th, raw, err := TuningCurrent(ctx, st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
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
func tuningSet(ctx context.Context, st wire.Store, key, val string, stdout io.Writer) int {
	// v0.24.0 R1d: thresholds 밖 meta키 — 자체 검증 후 upsert.
	for _, mk := range TuningMetaKeys() {
		if mk.Key == key {
			if err := mk.Validate(val); err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: key, Expect: err.Error()}))
				return 3
			}
			return tuningMetaUpsert(ctx, st, key, val, stdout)
		}
	}
	info, ok := TuningKeyMap()[key]
	if !ok {
		return tuningUnknownKey(stdout, key)
	}
	// 검증 ①: 후보 값을 파싱해 필드에 그대로 반영되는지(폴백 아님).
	// v0.25.3(미커버 스윕 ③): 비교는 수치로 — '0.30'·'60.0'·'.5' 같은
	// 동등 표기가 문자열 비교로 거부되던 것을 수용(포맷 차이 ≠ 무효 값).
	cand, err := store.ParseThresholds([][2]any{{key, val}})
	reflected := err == nil && info.Get(cand) == val
	if !reflected && err == nil {
		if got, perr := strconv.ParseFloat(info.Get(cand), 64); perr == nil {
			if want, werr := strconv.ParseFloat(strings.TrimSpace(val), 64); werr == nil {
				reflected = got == want
			}
		}
	}
	if err == nil && !reflected {
		err = fmt.Errorf("파싱 폴백 — 무효 값")
	}
	if err != nil || !reflected {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "유효 값(기본 " + info.Default + ", TUNING §2 참조)",
		}))
		return 3
	}
	// 검증 ②(v0.23.0 리뷰 ②): 폴백 키는 0이 "미설정" 의미라 저장해도
	// 소비자가 기본값으로 되돌린다 — 표현 불가 값 사전 거부.
	if val == "0" && zeroMeansUnset(key) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "0 불가(미설정 의미) — 기본 복귀는 reset",
		}))
		return 3
	}
	if err := wire.SetMetaKV(ctx, st, key, val); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"set","key":`+QuoteJSONFn(key)+`,"value":`+QuoteJSONFn(val)+"}")
	return 0
}

// metaDeleteStore — meta 행 삭제의 단일 경로 capability(v0.41.0 T3).
// realStore는 db.Repo.MetaDelete 위임 — 쓰기 가드(writes_through_repo_test)
// 로 lib/{ops,knowledge} 소스의 Query/queryOnce 호출부 쓰기 토큰이 금지된다.
type metaDeleteStore interface {
	MetaDelete(ctx context.Context, keys []string) (int64, error)
}

// tuningReset — 오버라이드 삭제(기본값 복귀).
func tuningReset(ctx context.Context, st wire.Store, key string, stdout io.Writer) int {
	_, inTh := TuningKeyMap()[key]
	inMeta := false
	for _, mk := range TuningMetaKeys() {
		if mk.Key == key {
			inMeta = true
		}
	}
	if !inTh && !inMeta {
		return tuningUnknownKey(stdout, key)
	}
	// v0.41.0 T3: DELETE는 db.Repo.MetaDelete 단일 경로.
	md, ok := st.(metaDeleteStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"tuning reset: unsupported store"}`)
		return 1
	}
	if _, err := md.MetaDelete(ctx, []string{key}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"reset","key":`+QuoteJSONFn(key)+"}")
	return 0
}

func tuningUnknownKey(stdout io.Writer, key string) int {
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tuning", Expect: "알려진 키: " + strings.Join(tuningKeyList()[:5], ", ") + ", …"}))
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
// v0.34.3 T2: upsert 본체는 setMetaKV 단일 진입에 위임.
func tuningMetaUpsert(ctx context.Context, st wire.Store, key, val string, stdout io.Writer) int {
	if err := wire.SetMetaKV(ctx, st, key, val); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"set","key":`+QuoteJSONFn(key)+`,"value":`+QuoteJSONFn(val)+"}")
	return 0
}

// TuningResetAll — 전체 오버라이드 일괄 삭제(v0.24.0 R4b: 22+왕복 → 1회).
// thresholds 키 + meta 관리 키 전부(TuningKeyAll 단일 소스) — 리셋된
// 키 목록을 합산 JSON으로. v0.41.0 T3: DELETE는 db.Repo.MetaDelete 단일
// 경로(전량 삭제 계약 유지 — TuningKeyAll 전체를 1회 전달).
func TuningResetAll(ctx context.Context, st wire.Store, stdout io.Writer) int {
	all := TuningKeyAll()
	md, ok := st.(metaDeleteStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"tuning reset: unsupported store"}`)
		return 1
	}
	if _, err := md.MetaDelete(ctx, all); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"reset-all","keys":`+strconv.Itoa(len(all))+"}")
	return 0
}
