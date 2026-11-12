// read_v0150_test.go — read 와이어링 계약(스펙 R1·R2의 v0.42.0 T2 개정판).
// read_stats blob 폐지 후 계측 왕복 예산 축소(쓰기 4→2: BumpReads·
// RecordReadEvent만)·분류(zero·near)는 read_events 인자로만 전달된다.
// fakeStore 질의 캡처 패턴은 read_v0140_test.go 승계.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// readClassRecorder — RecordReadEvent·RecordReadUsage 호출을 녹음하는 fake
// (readEventRecorder 관례 — 쓰기 축소 단언에 유입 경로 녹음을 추가).
type readClassRecorder struct {
	*fakeStore
	events []db.ReadEvent
	usages int
}

func (r *readClassRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	r.usages++
	return nil
}

func (r *readClassRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

// TestReadInstrumentationQueryBudget — Review Focus: eligible 히트 read
// 1회의 쿼리 왕복 ≤ 6(본 쿼리 1 + 계측 ≤ 5). v0.42.0 T2 이후 read_stats
// 읽기-쓰기·daily 유입 기록이 빠져 왕복은 더 줄어야 한다(상한은 유지).
func TestReadInstrumentationQueryBudget(t *testing.T) {
	count := 0
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		count++
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
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

// TestReadClassifiesZeroNear — Review Focus(v0.42.0 T2 개정): 제로히트+근접
// read의 분류 (zero=true, near=true)는 read_events 1행(RecordReadEvent)에만
// 반영된다 — read_stats blob 쓰기·RecordReadUsage 유입 기록은 폐지(0회).
// 히트 read는 (false,false) 1행.
func TestReadClassifiesZeroNear(t *testing.T) {
	t.Run("zero-near", func(t *testing.T) {
		var inserts []string
		st := &readClassRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "id, topic, summary FROM active_knowledge"):
				return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil // 제안 스캔 후보
			case strings.Contains(sql, "INSERT INTO meta"):
				if len(args) > 1 {
					inserts = append(inserts, args[0].(string)+"="+args[1].(string))
				}
				return nil, nil
			}
			return nil, nil // 구문·폴백 제로히트
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if len(st.events) != 1 || !st.events[0].Zero || !st.events[0].Near {
			t.Errorf("read_events = %+v, want 1행 {Zero:true Near:true}", st.events)
		}
		if st.usages != 0 {
			t.Errorf("RecordReadUsage %d회, want 0(read kind daily 쓰기 폐지)", st.usages)
		}
		for _, kv := range inserts {
			if strings.HasPrefix(kv, "read_stats=") {
				t.Errorf("read_stats 쓰기 잔존(폐지): %s", kv)
			}
		}
	})
	t.Run("hit", func(t *testing.T) {
		st := &readClassRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit %d: %s", code, out.String())
		}
		if len(st.events) != 1 || st.events[0].Zero || st.events[0].Near {
			t.Errorf("read_events = %+v, want 1행 {Zero:false Near:false}", st.events)
		}
		if st.usages != 0 {
			t.Errorf("RecordReadUsage %d회, want 0", st.usages)
		}
	})
}
