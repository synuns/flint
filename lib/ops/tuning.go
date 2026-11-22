// tuning.go — `flint tuning` — the interface for querying, setting, and resetting the
// TUNING §2 threshold keys. Previously meta had to be updated directly via psql
// (a management barrier). Contract: one-line JSON, exit 0/1/3 (3 = key/value error).
// Value validation goes through the real path (loadThresholds -> ParseThresholds) — if a parse
// fallback occurs (the value is not reflected), it is rejected (a single source with the docs).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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

// QuoteJSONFn — the JSON string-literal quoting seam (cli backup.go's quoteJSON —
// converging direct calls).
var QuoteJSONFn func(s string) string

// TuningKeyInfo — exposed key metadata (value serialization is per-field Get).
type TuningKeyInfo struct {
	Key      string
	Default  string
	Consumer string // Consumer string // consumption-path indication (cross-referenced with the TUNING table)
	Get      func(store.Thresholds) string
}

// TuningKeys — all TUNING §2 keys. Derived from the wire registry
// (ThresholdMetas) — Key, Default, Consumer (Desc), and Get (field
// reflection) all have the registry as their single source, and threshold_meta_test.go guards
// consistency with the registry, DefaultThresholds, and ParseThresholds.
// The order is the registry order (= the previous declaration order) — deterministic sorting happens in list.
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

// thresholdFormatValue — the notation convention for registry defaults (any) (int, float64 —
// inheriting the i()/f() format of the previous TuningKeys; string since v1.21.0 — the
// vault_skip_dirs string-key family, passed through verbatim).
func thresholdFormatValue(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return ""
}

// thresholdFieldGet — Thresholds field name -> Get closure (reflection). A missing
// field yields "" (the production registry's field validity is guarded by
// threshold_meta_test.go — an unreachable path).
func thresholdFieldGet(field string) func(store.Thresholds) string {
	return func(t store.Thresholds) string {
		fv := reflect.Indirect(reflect.ValueOf(t)).FieldByName(field)
		if !fv.IsValid() {
			return ""
		}
		return thresholdFormatValue(fv.Interface())
	}
}

// TuningMetaKey — the meta management keys outside the thresholds registry (
// unifying the management surface — hot_budget, backup_keep, exposure_profile, etc.).
type TuningMetaKey struct {
	Key      string
	Default  string
	Validate func(val string) error
}

// TuningMetaKeys — extended management-surface keys. Validation is per-type rules (integer, enumeration).
func TuningMetaKeys() []TuningMetaKey {
	intPositive := func(key string) func(string) error {
		return func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n <= 0 {
				return fmt.Errorf("%s: must be a positive integer", key)
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
				return fmt.Errorf("exposure_profile: must be normal|minimal")
			}
			return nil
		}},
		// personal vocabulary (empty allowed = unset, comma list).
		{Key: "vocab_tags", Default: "", Validate: vocabListValidator("vocab_tags")},
		{Key: "vocab_domains", Default: "", Validate: vocabListValidator("vocab_domains")},
		// the backup-encryption opt-in switch (false = keep plaintext — existing
		// backups and restore unchanged). When true, automatic and manual backups are age-encrypted.
		{Key: "backup_encrypt", Default: "false", Validate: func(v string) error {
			if v != "true" && v != "false" {
				return fmt.Errorf("backup_encrypt: must be true|false")
			}
			return nil
		}},
		// the autonomous-tuning mode (v1.23.0 — the shadow-circuit master switch).
		// Default off: an unconfigured deployment pays zero shadow cost (the
		// opt-in ruling). shadow = predict-only rounds, never an applied override.
		{Key: "auto_mode", Default: "off", Validate: func(v string) error {
			if v != "off" && v != "shadow" {
				return fmt.Errorf("auto_mode: must be off|shadow")
			}
			return nil
		}},
	}
}

// vocabListValidator — a comma list (empty allowed; elements are normalizable strings).
func vocabListValidator(key string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return nil
		}
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "" {
				return fmt.Errorf("%s: empty items are not allowed (comma list)", key)
			}
		}
		return nil
	}
}

// TuningKeyMap — key -> metadata (fast lookup).
func TuningKeyMap() map[string]TuningKeyInfo {
	out := make(map[string]TuningKeyInfo, len(wire.ThresholdsKeys())+len(TuningMetaKeys()))
	for _, k := range TuningKeys() {
		out[k.Key] = k
	}
	return out
}

// TuningKeyAll — the sorted union (deduplicated) of the .Key fields of thresholdsKeys
// (maintain.go) and TuningMetaKeys. The single source curing the drift where tuningCurrent
// and tuningResetAll duplicated a hardcoded 6-key list and missed backup_encrypt
// (the 7th meta key).
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

// tuningKeyList — the deterministically sorted key list.
func tuningKeyList() []string {
	out := make([]string, 0, 22)
	for _, k := range TuningKeys() {
		out = append(out, k.Key)
	}
	sort.Strings(out)
	return out
}

// RunTuning — subcommand dispatch. No arguments = list.
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
	case "auto":
		if len(args) != 2 {
			return tuningUsage(stdout, "auto off|shadow")
		}
		return tuningAuto(ctx, st, args[1], stdout)
	case "what-if":
		if len(args) != 3 {
			return tuningUsage(stdout, "what-if <key> <value>")
		}
		return tuningWhatIf(ctx, st, args[1], args[2], stdout)
	case "status":
		if len(args) != 1 {
			return tuningUsage(stdout, "status")
		}
		return TuningStatus(ctx, st, stdout)
	}
	return tuningUsage(stdout, "list | get <key> | set <key> <value> | reset <key> | reset --all | auto off|shadow | what-if <key> <value> | status")
}

// tuningAuto — `tuning auto off|shadow`: sets meta auto_mode (the shadow-circuit
// master switch). Validation is the TuningMetaKeys enum (the single source —
// the same rule as `tuning set auto_mode`), the write is the SetMetaKV
// idempotent upsert, and switching to off keeps the streak/fired/gate stamps
// (a re-switch resumes — documented in TUNING).
func tuningAuto(ctx context.Context, st wire.Store, mode string, stdout io.Writer) int {
	for _, mk := range TuningMetaKeys() {
		if mk.Key != "auto_mode" {
			continue
		}
		if err := mk.Validate(mode); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "auto_mode", Expect: "off|shadow"}))
			return 3
		}
		return tuningMetaUpsert(ctx, st, "auto_mode", mode, stdout)
	}
	// unreachable — the auto_mode entry is part of TuningMetaKeys (guarded by tests).
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "auto_mode", Expect: "off|shadow"}))
	return 3
}

// TuningAutoKeyGate — the auto-surface key gate: a threshold key outside the
// registry auto whitelist on an auto path (what-if, the shadow trigger table)
// is an invalid-argument exit 3 — a key error, never a silent no-op. The
// whitelist is the registry Auto field (the single source).
func TuningAutoKeyGate(stdout io.Writer, key string) int {
	if wire.ThresholdAutoGrade(key) != "" {
		return 0
	}
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
		Field:  key,
		Expect: "not an auto-tuning key — whitelist: " + strings.Join(autoWhitelistKeys(), ", "),
	}))
	return 3
}

// autoWhitelistKeys — the sorted whitelist key list (the gate message).
func autoWhitelistKeys() []string {
	w := wire.ThresholdAutoWhitelist()
	out := make([]string, 0, len(w))
	for k := range w {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func tuningUsage(stdout io.Writer, usage string) int {
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tuning", Expect: usage}))
	return 3
}

// TuningCurrent — the current values (including the raw meta values for override detection).
func TuningCurrent(ctx context.Context, st wire.Store) (store.Thresholds, map[string]string, error) {
	// a single query — reads the same key set once and derives both the raw map
	// and the parsed result (removing a double lookup with loadThresholds).
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
			// the raw values flow verbatim into the list/get "value" output
			// (vocab_* are free strings) — via the sanitization outlet.
			// Parsing (kv) uses the originals (numeric judgment is on the raw text).
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

// TuningList — the current value, default, and source of every key as one-line JSON.
func TuningList(ctx context.Context, st wire.Store, stdout io.Writer) int {
	th, raw, err := TuningCurrent(ctx, st)
	if err != nil { // an error, not an empty output.
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
	// also exposing the merged meta management keys outside thresholds.
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

// tuningGet — a single key's status.
func tuningGet(ctx context.Context, st wire.Store, key string, stdout io.Writer) int {
	// meta management keys are also get targets (surface-consistent with set/list/reset).
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
	_, overridden := raw[key] // same as list: a bool.
	b, _ := json.Marshal(map[string]any{
		"key": key, "value": info.Get(th), "default": info.Default, "override": overridden,
	})
	fmt.Fprintln(stdout, string(b))
	return 0
}

// tuningSet — value validation (confirming reflection via the real-path parse) then meta upsert.
func tuningSet(ctx context.Context, st wire.Store, key, val string, stdout io.Writer) int {
	// a meta key outside thresholds — validated on its own, then upsert.
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
	// validation 1: the real-path parse-and-reflect check (shared with
	// what-if — tuningValueAccepted, the single value-validity source).
	if !tuningValueAccepted(info, val) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "a valid value (default " + info.Default + ", see TUNING §2)",
		}))
		return 3
	}
	// validation 2: for fallback keys, 0 means "unset", so even if saved the
	// consumer reverts to the default — rejecting unrepresentable values up front.
	if val == "0" && zeroMeansUnset(key) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "0 is not allowed (means unset) — use reset to restore defaults",
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

// metaDeleteStore — the single-path capability for deleting meta rows.
// realStore delegates to db.Repo.MetaDelete — the write guard
// (writes_through_repo_test) forbids write tokens at Query/queryOnce call sites in lib/{ops,knowledge} sources.
type metaDeleteStore interface {
	MetaDelete(ctx context.Context, keys []string) (int64, error)
}

// tuningReset — deletes an override (returning to the default).
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
	// the DELETE stays on the single db.Repo.MetaDelete path.
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
	fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tuning", Expect: "known keys: " + strings.Join(tuningKeyList()[:5], ", ") + ", …"}))
	return 3
}

// zeroMeansUnset — the keys for which loadThresholds falls back to 0 as unset (
// 4 keys excluded — 0 is a valid real value). The unrepresentable set.
func zeroMeansUnset(key string) bool {
	switch key {
	case "pending_max", "dup_susp_max", "conf_floor", "stale_sessions":
		return false
	}
	return true
}

// tuningMetaUpsert — the common meta upsert for validated values (the same path for §2 and meta keys).
// The upsert body delegates to the single setMetaKV entry.
func tuningMetaUpsert(ctx context.Context, st wire.Store, key, val string, stdout io.Writer) int {
	if err := wire.SetMetaKV(ctx, st, key, val); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"set","key":`+QuoteJSONFn(key)+`,"value":`+QuoteJSONFn(val)+"}")
	return 0
}

// TuningResetAll — bulk-deletes all overrides (22+ round trips -> 1).
// All thresholds keys + meta management keys (TuningKeyAll as the single source) — the
// deleted key list goes into the summary JSON. The DELETE stays on the single
// db.Repo.MetaDelete path (keeping the delete-everything contract — passing all of TuningKeyAll at once).
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

// tuningValueAccepted — the real-path value-validity check (validation 1
// of tuningSet, shared with what-if): the candidate parses through
// ParseThresholds and is reflected in the key's field as-is (no fallback).
// Compared numerically — accepting equivalent notations like '0.30',
// '60.0', and '.5' that string comparison used to reject (a format
// difference is not an invalid value).
func tuningValueAccepted(info TuningKeyInfo, val string) bool {
	cand, err := store.ParseThresholds([][2]any{{info.Key, val}})
	reflected := err == nil && info.Get(cand) == val
	if !reflected && err == nil {
		if got, perr := strconv.ParseFloat(info.Get(cand), 64); perr == nil {
			if want, werr := strconv.ParseFloat(strings.TrimSpace(val), 64); werr == nil {
				reflected = got == want
			}
		}
	}
	return reflected
}

// autoRuleForKey — the autotune code-registry rule adjusting key (the T3
// registry as the what-if query source — never a re-derived second query
// definition; the diagnosis engine stays untouched per the ruling).
func autoRuleForKey(key string) (AutoRule, bool) {
	for _, r := range AutoRules() {
		if r.Key == key {
			return r, true
		}
	}
	return AutoRule{}, false
}

// tuningCellInt — a scanned count cell as int (0 on a non-numeric — the
// aggregate row is the registry query's own contract shape).
func tuningCellInt(v any) int {
	if f, ok := tuningCellFloat(v); ok {
		return int(f)
	}
	return 0
}

// tuningCellFloat — a scanned numeric cell as float64.
func tuningCellFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// percentileNearestRank — nearest-rank percentile over ascending samples
// (ceil(p*n)-1 — the report.go p95 formula precedent). n=0 is 0 (the
// zero-denominator guard convention). Unlike the p95 reporting floor
// (min 5 samples), what-if prints the sample count beside the rank — a
// small-window rank stays descriptive instead of hidden.
func percentileNearestRank(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

// tuningWhatIf — `tuning what-if <key> <value>`: the C1 counterfactual
// policy-analysis surface (a human surface — the agent report surface
// stays single-JSON unexpanded). Reuses, in order: the auto key gate
// (TuningAutoKeyGate — a manual-only key is exit 3), the tuningSet value
// validation (tuningValueAccepted — the real-path parse), and the T3
// registry verdict query re-executed at the hypothetical bound (1-2
// queries: the aggregate + the zero-row near_score distribution — no
// second query definition to drift). Output is numbers and registry
// constants only (nothing user-derived — the sanitization principle holds
// by construction); the note states the shadow ruling: never applied.
func tuningWhatIf(ctx context.Context, st wire.Store, key, val string, stdout io.Writer) int {
	if code := TuningAutoKeyGate(stdout, key); code != 0 {
		return code
	}
	info, ok := TuningKeyMap()[key]
	if !ok {
		return tuningUnknownKey(stdout, key)
	}
	if !tuningValueAccepted(info, val) || (val == "0" && zeroMeansUnset(key)) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "a valid value (default " + info.Default + ", see TUNING §2)",
		}))
		return 3
	}
	rule, ok := autoRuleForKey(key)
	if !ok {
		// whitelist/registry drift — the meta Auto field and the T3 registry
		// are guarded homogenous; reaching here is a build inconsistency.
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
			Field:  key,
			Expect: "no auto rule registered for this key (registry drift — see docs/TUNING.md)",
		}))
		return 3
	}
	bound, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil {
		return tuningUsage(stdout, "what-if <key> <value>")
	}
	// query 1 — the registry verdict query at the hypothetical bound: the
	// reclass pool is the zero rows with near_score >= the bound (the same
	// definition the shadow round predicts with — one source).
	rows, err := wire.QueryOnce(ctx, st, AutoVerdictQuery(rule), []any{bound})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(rows) != 1 || len(rows[0]) != 3 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("what-if: unexpected aggregate row")))
		return 1
	}
	sampleN := tuningCellInt(rows[0][0])
	zeroN := tuningCellInt(rows[0][1])
	reclassN := tuningCellInt(rows[0][2])
	// query 2 — the zero rows' near_score distribution (the p50/p90 input —
	// the min_score-boundary distribution is the counterfactual core).
	drows, err := wire.QueryOnce(ctx, st, fmt.Sprintf(
		`SELECT near_score FROM read_events WHERE zero AND ts >= now() - interval '%d days' ORDER BY near_score`,
		rule.ObsWindowD), nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	scores := make([]float64, 0, len(drows))
	for _, r := range drows {
		if len(r) == 1 {
			// NaN/Inf cells skip (r101-1): a stored numeric NaN would
			// otherwise ride the slice into json.Marshal and fail the whole
			// output — the remaining finite distribution still ranks.
			if s, ok := tuningCellFloat(r[0]); ok && !math.IsNaN(s) && !math.IsInf(s, 0) {
				scores = append(scores, s)
			}
		}
	}
	b, err := json.Marshal(map[string]any{
		"key": key, "value": val, "auto": wire.ThresholdAutoGrade(key),
		"sample_n": sampleN, "zero_n": zeroN, "reclass_n": reclassN,
		"near_pct":       ReadZeroPct(sampleN, reclassN),
		"near_score_p50": percentileNearestRank(scores, 0.50),
		"near_score_p90": percentileNearestRank(scores, 0.90),
		"note":           "what-if — counterfactual analysis only, nothing is applied",
	})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// tuningStatusRow — one key row of `tuning status`: the TuningList
// skeleton (key/value/default/consumer/override — unchanged order and
// semantics) extended with the auto grade (registry Auto field, manual
// keys omit) and drift = current − default (numeric keys only, rounded to
// 6 decimals — a display guard against float noise).
type tuningStatusRow struct {
	Key      string   `json:"key"`
	Value    string   `json:"value"`
	Default  string   `json:"default"`
	Consumer string   `json:"consumer,omitempty"`
	Override bool     `json:"override"`
	Auto     string   `json:"auto,omitempty"`
	Drift    *float64 `json:"drift,omitempty"`
}

// tuningAccumulation — the JSON projection of the AccumulationGate
// verdict (AccumulationState itself is the T3 pure function's type — the
// lower-case wire tags live here, on the surface that owns the output).
type tuningAccumulation struct {
	Eligible bool   `json:"eligible"`
	Detail   string `json:"detail"`
}

// tuningAutoCard — the per-rule accumulation card: the fired counter
// (meta fired_auto_<signal>), the consecutive-hit streak (meta
// auto_streak_<key>), the shadow rounds observed (meta auto_rounds_<key>
// — every stamped round, fired or not; r100-1 split from the
// tuning_predictions count, which only sees fired rounds with a
// proposal), and the AccumulationGate verdict (the v1.24.0 apply-gate
// progress — "a hit is stability, not causality").
type tuningAutoCard struct {
	Signal       string             `json:"signal"`
	Key          string             `json:"key"`
	Grade        string             `json:"grade"`
	Fired        int                `json:"fired"`
	Streak       int                `json:"streak"`
	Rounds       int                `json:"rounds"`
	Accumulation tuningAccumulation `json:"accumulation"`
}

// TuningStatus — `tuning status`: the C2/C6 visibility surface — the list
// skeleton plus drift, and per auto rule the fired/streak/rounds counters
// with the observation-accumulation progress (the AccumulationGate reuse —
// rounds read meta auto_rounds_<key>, the every-stamped-round counter;
// fires the fired counter). A counter surface: no read_events observation
// query runs (that is what-if's job); two queries total (TuningCurrent +
// the counter read).
func TuningStatus(ctx context.Context, st wire.Store, stdout io.Writer) int {
	th, raw, err := TuningCurrent(ctx, st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	km := TuningKeyMap()
	outRows := make([]tuningStatusRow, 0, 22)
	for _, k := range tuningKeyList() {
		info := km[k]
		row := tuningStatusRow{
			Key: k, Value: info.Get(th), Default: info.Default,
			Consumer: info.Consumer, Override: raw[k] != "",
		}
		row.Auto = wire.ThresholdAutoGrade(k)
		if cur, e1 := strconv.ParseFloat(row.Value, 64); e1 == nil {
			if def, e2 := strconv.ParseFloat(row.Default, 64); e2 == nil {
				d := math.Round((cur-def)*1e6) / 1e6
				row.Drift = &d
			}
		}
		outRows = append(outRows, row)
	}
	for _, mk := range TuningMetaKeys() {
		value, overridden := mk.Default, false
		if v, ok := raw[mk.Key]; ok {
			value, overridden = v, true
		}
		outRows = append(outRows, tuningStatusRow{
			Key: mk.Key, Value: value, Default: mk.Default, Override: overridden,
		})
	}
	// the per-rule counters (query 2): fired_auto_* + auto_streak_* +
	// auto_rounds_* read in one IN lookup — the values pass the render
	// outlet like every meta text.
	rules := AutoRules()
	counters := map[string]string{}
	if len(rules) > 0 {
		keys := make([]string, 0, len(rules)*3)
		for _, r := range rules {
			keys = append(keys, "fired_auto_"+r.Signal, "auto_streak_"+r.Key, "auto_rounds_"+r.Key)
		}
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT key, value FROM meta WHERE key IN ('`+strings.Join(keys, "','")+`')`, nil)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		for _, r := range rows {
			if len(r) == 2 {
				counters[fmt.Sprint(r[0])] = render.Text(fmt.Sprint(r[1]))
			}
		}
	}
	cards := make([]tuningAutoCard, 0, len(rules))
	for _, r := range rules {
		// rounds read the every-stamped-round counter (r100-1): a
		// non-fired round still counts toward the rounds ≥ 14 condition —
		// the old tuning_predictions count only saw fired rounds with a
		// proposal, conflating the two independent gate inputs.
		rounds, _ := strconv.Atoi(counters["auto_rounds_"+r.Key])
		fired, _ := strconv.Atoi(counters["fired_auto_"+r.Signal])
		streak, _ := strconv.Atoi(counters["auto_streak_"+r.Key])
		gate := AccumulationGate(AccumulationProgress{Rounds: rounds, Fires: fired, Streak: streak})
		cards = append(cards, tuningAutoCard{
			Signal: r.Signal, Key: r.Key, Grade: wire.ThresholdAutoGrade(r.Key),
			Fired: fired, Streak: streak, Rounds: rounds,
			Accumulation: tuningAccumulation(gate),
		})
	}
	mode := "off"
	if v, ok := raw["auto_mode"]; ok {
		mode = v
	}
	b, err := json.Marshal(map[string]any{"mode": mode, "keys": outRows, "auto": cards})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
