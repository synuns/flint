// read_v0140_test.go — the zero-hit silence contract (fully no output +
// exit 1) and the zero_near internal classification metering (spec §3 R1, R2). The fakeStore query
// capture pattern is inherited from read_v0100_test.go.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// mustSuggestStore — both phrase and fallback zero-hit; the suggestion scan returns the scan rows.
// If scan is an empty slice, it becomes a 0-candidate fixture.
func mustSuggestStore(scan [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return scan, nil
		}
		return nil, nil // phrase, fallback, and meta all zero/empty
	}}
}

// nearCountingStore — 1 suggestion-scan row (a similarity-cap candidate) + meta INSERT
// key=value capture. Verifies the zero_near increment rewrite.
func nearCountingStore(metaInserts *[]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "id, topic, summary FROM active_knowledge"):
			return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			if len(args) > 1 {
				*metaInserts = append(*metaInserts, fmt.Sprint(args[0])+"="+fmt.Sprint(args[1]))
			}
			return nil, nil
		default:
			return nil, nil // phrase, fallback, and meta reads all zero/empty
		}
	}}
}

// TestReadZeroHitSilent — a zero hit is fully silent with or without scan candidates
// (exit 1, including no suggestions line — no leftover suggestion line).
func TestReadZeroHitSilent(t *testing.T) {
	cases := []struct {
		name string
		scan [][]any
	}{
		{"scan-hit", [][]any{{int32(7), "flint 백업 절차", "덤프"}}},
		{"scan-empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var out bytes.Buffer
			f := mustSuggestStore(tc.scan)
			if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
			}
			if got := out.String(); got != "" {
				t.Errorf("제로히트 출력 = %q, want \"\"", got)
			}
		})
	}
}

// TestReadZeroHitNearCounted — a zero hit with 1 scan candidate ->
// the near classification is recorded in 1 read_events row (Near=true) (the old
// read_stats zero_near increment is abolished — no read_stats key in the meta INSERT).
func TestReadZeroHitNearCounted(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var inserts []string
	f := nearCountingStore(&inserts)
	st := &readClassRecorder{fakeStore: f}
	if code := runRead(ctx, st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if len(st.events) != 1 || !st.events[0].Zero || !st.events[0].Near {
		t.Fatalf("read_events = %+v, want 1행 {Zero:true Near:true}", st.events)
	}
	for _, kv := range inserts {
		if strings.HasPrefix(kv, "read_stats=") {
			t.Errorf("read_stats 쓰기 잔존(폐지): %s", kv)
		}
	}
}
