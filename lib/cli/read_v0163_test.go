// read_v0163_test.go — v0.16.3: read 제로히트 제안 경로가 meta
// suggest_scan_cap 오버라이드를 반영하는지(Thresholds → SetSuggestThresholds
// 주입 배선. suggest_min_score도 같은 주입 호출로 흐른다).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

// suggest_scan_cap=42 오버라이드 — 제안 스캔 SQL의 LIMIT이 42로 바뀐다
// (기본 5000이었다면 오버라이드 미반영).
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
		return nil, nil // 구문·폴백·계측 전부 제로/빈
	}}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("제로히트 exit = %d, want 1", code)
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
