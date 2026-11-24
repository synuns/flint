package wire

import (
	"context"
	"reflect"
	"strings"

	"github.com/synuns/flint/lib/store"
)

func ThresholdsKeys() []string {
	metas := ThresholdMetas()
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Key)
	}
	return out
}

func thresholdsKeySQL() string {
	return "SELECT key, value FROM meta WHERE key IN ('" +
		strings.Join(ThresholdsKeys(), "','") + "')"
}

func ApplyThresholdsFallback(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	tv := reflect.ValueOf(&th).Elem()
	dv := reflect.ValueOf(&def).Elem()
	seen := map[string]bool{}
	for _, m := range store.ThresholdMetas() {
		if !m.Fallback || seen[m.Field] {
			continue
		}
		seen[m.Field] = true
		cur := tv.FieldByName(m.Field)
		d := dv.FieldByName(m.Field)
		if !cur.IsValid() || !cur.CanSet() || !d.IsValid() {
			continue
		}
		switch m.Type {
		case "int":
			if cur.Kind() != reflect.Int {
				continue
			}
			if cur.Int() == 0 || cur.Int() < 0 {
				cur.SetInt(d.Int())
			}
		case "int-neg":
			if cur.Kind() != reflect.Int {
				continue
			}
			if cur.Int() == 0 {
				cur.SetInt(d.Int())
			}
		case "ratio", "pos-float", "nonneg-float":
			if cur.Kind() != reflect.Float64 {
				continue
			}
			if cur.Float() <= 0 {
				cur.SetFloat(d.Float())
			}
		}
	}
	return th
}

var LoadThresholdsFn = LoadThresholds

func LoadThresholds(ctx context.Context, st Store) (store.Thresholds, error) {
	rows, err := QueryOnce(ctx, st, thresholdsKeySQL(), nil)
	if err != nil {
		return store.Thresholds{}, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return th, err
	}
	return ApplyThresholdsFallback(th), nil
}
