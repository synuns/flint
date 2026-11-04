// read_v0140_test.go — v0.14.0 T2: 제로히트 침묵 계약(완전 무출력·
// exit 1)·zero_near 내부 분류 계측(스펙 §3 R1·R2). fakeStore 질의
// 캡처 패턴은 read_v0100_test.go 승계.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// mustSuggestStore — 구문·폴백 모두 제로히트, 제안 스캔은 scan 행 반환.
// scan이 빈 슬라이스면 스캔 후보 0건 픽스처가 된다.
func mustSuggestStore(scan [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return scan, nil
		}
		return nil, nil // 구문·폴백·meta 전부 제로/빈
	}}
}

// nearCountingStore — 제안 스캔 1행(유사도 상한 후보) + meta INSERT
// key=value 캡처. zero_near 증분 재기록을 검증한다.
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
			return nil, nil // 구문·폴백·meta 읽기 전부 제로/빈
		}
	}}
}

// TestReadZeroHitSilent — Review Focus: 제로히트는 스캔 후보 있든 없든
// 완전 무출력·exit 1(suggestions 라인 부재 포함 — 제안 라인 잔존 금지).
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
			if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if got := out.String(); got != "" {
				t.Errorf("제로히트 출력 = %q, want \"\"", got)
			}
		})
	}
}

// TestReadZeroHitNearCounted — Review Focus: 제로히트·스캔 후보 1건 →
// meta read_stats INSERT에 zero_near 1 증분 포함.
func TestReadZeroHitNearCounted(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var inserts []string
	f := nearCountingStore(&inserts)
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, kv := range inserts {
		if strings.HasPrefix(kv, "read_stats=") && strings.Contains(kv, `"zero_near":1`) {
			return
		}
	}
	t.Errorf("read_stats INSERT에 zero_near:1 미포함: %v", inserts)
}
