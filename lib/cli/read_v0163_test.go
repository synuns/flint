package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

func TestReadSuggestScanCapOverride(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		query.SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"suggest_scan_cap", "42"}}, nil
		}
		return nil, nil
	}}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("제로히트 exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	scanned := false
	for _, q := range f.queries {
		if strings.Contains(q, "id, topic, summary FROM active_knowledge") {
			scanned = true
			if !strings.Contains(q, "LIMIT 42") {
				t.Errorf("제안 스캔 상한 미반영: %q, want LIMIT 42(기본 5000 잔존 의심)", q)
			}
		}
	}
	if !scanned {
		t.Error("제안 스캔 질의 미발생")
	}
}
