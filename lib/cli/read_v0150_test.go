// read_v0150_test.go — v0.15.0 T2: read 와이어링 통합 계약(스펙 R1·R2).
// 계측 왕복 예산(단일 read_stats 라이터)·분류 일괄 전달(RecordReadUsage
// seam) 검증. fakeStore 질의 캡처 패턴은 read_v0140_test.go 승계.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// readUsageRecorder — fakeStore에 RecordReadUsage(R2 seam)를 얹은 fake
// (readhot_v06_test.go instrumentedFake 패턴 승계). 호출 분류를 녹음한다.
type readUsageRecorder struct {
	*fakeStore
	calls [][2]bool
}

func (r *readUsageRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	r.calls = append(r.calls, [2]bool{zero, near})
	return nil
}

// TestReadInstrumentationQueryBudget — Review Focus: eligible 히트 read
// 1회의 쿼리 왕복 ≤ 6(본 쿼리 1 + 계측 ≤ 5). read_stats는 단일 읽기-쓰기로
// 통합돼야 한다(R1 단일 라이터).
func TestReadInstrumentationQueryBudget(t *testing.T) {
	count := 0
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		count++
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
		case strings.Contains(sql, "read_stats"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "INSERT INTO meta") || strings.Contains(sql, "UPDATE"):
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if count > 6 { // 본 쿼리 1 + 계측 ≤ 5
		t.Errorf("쿼리 왕복 %d회, 본 쿼리 포함 ≤ 6 초과", count)
	}
}

// TestReadClassifiesZeroNear — Review Focus: 제로히트+근접 read는 read_stats
// 1회 쓰기에 calls/zero/zero_near 전부 반영(R1)하고 분류 (true,true)를
// RecordReadUsage로 전달(R2). 히트 read는 (false,false) 전달.
func TestReadClassifiesZeroNear(t *testing.T) {
	t.Run("zero-near", func(t *testing.T) {
		var inserts []string
		st := &readUsageRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "id, topic, summary FROM active_knowledge"):
				return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
			case strings.Contains(sql, "SELECT value FROM meta"):
				return [][]any{{`{"calls":5,"zero":2}`}}, nil
			case strings.Contains(sql, "INSERT INTO meta"):
				if len(args) > 1 {
					inserts = append(inserts, fmt.Sprint(args[0])+"="+fmt.Sprint(args[1]))
				}
				return nil, nil
			}
			return nil, nil // 구문·폴백 제로히트
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		n := 0
		for _, kv := range inserts {
			if !strings.HasPrefix(kv, "read_stats=") {
				continue
			}
			n++
			for _, want := range []string{`"calls":6`, `"zero":3`, `"zero_near":1`} {
				if !strings.Contains(kv, want) {
					t.Errorf("read_stats 쓰기에 %s 미반영: %s", want, kv)
				}
			}
		}
		if n != 1 {
			t.Errorf("read_stats 쓰기 %d회, want 1(단일 라이터): %v", n, inserts)
		}
		if len(st.calls) != 1 || st.calls[0] != [2]bool{true, true} {
			t.Errorf("RecordReadUsage = %v, want [(true true)]", st.calls)
		}
	})
	t.Run("hit", func(t *testing.T) {
		var inserts []string
		st := &readUsageRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM active_knowledge"):
				return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
			case strings.Contains(sql, "SELECT value FROM meta"):
				return [][]any{{`{"calls":5,"zero":2}`}}, nil
			case strings.Contains(sql, "INSERT INTO meta"):
				if len(args) > 1 {
					inserts = append(inserts, fmt.Sprint(args[0])+"="+fmt.Sprint(args[1]))
				}
				return nil, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit %d: %s", code, out.String())
		}
		if len(st.calls) != 1 || st.calls[0] != [2]bool{false, false} {
			t.Errorf("RecordReadUsage = %v, want [(false false)]", st.calls)
		}
		for _, kv := range inserts {
			if strings.HasPrefix(kv, "read_stats=") && strings.Contains(kv, "zero_near") {
				t.Errorf("히트 read에 zero_near 증분: %s", kv)
			}
		}
	})
}
