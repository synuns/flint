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
	}
	return tuningUsage(stdout, "list | get <key> | set <key> <value> | reset <key> | reset --all")
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
	// validation 1: parse the candidate value and confirm it is reflected in the field as-is (no
	// fallback). Compared numerically — accepting equivalent notations like '0.30',
	// '60.0', and '.5' that string comparison used to reject (a format difference is not an invalid value).
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
		err = fmt.Errorf("parse fallback — invalid value")
	}
	if err != nil || !reflected {
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
