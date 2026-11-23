package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func mustSuggestStore(scan [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return scan, nil
		}
		return nil, nil
	}}
}

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
			return nil, nil
		}
	}}
}

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
