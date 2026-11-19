// read_v0150_test.go — the read wiring contracts (a revision of spec R1 and R2).
// After the read_stats blob abolition, the metering round-trip budget shrank (writes 4->2: only
// BumpReads and RecordReadEvent); classification (zero, near) is passed only as read_events arguments.
// The fakeStore query-capture pattern is inherited from read_v0140_test.go.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// readClassRecorder — a fake recording RecordReadEvent and RecordReadUsage calls
// (the readEventRecorder convention — adding inbound-path recording for the write-reduction assertions).
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

// TestReadInstrumentationQueryBudget — an eligible hit read's
// query round trips <= 6 (1 main query + <= 5 metering). With the read_stats
// read-write and the daily inbound record gone, round trips shrink further (the cap is kept).
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
	if count > 6 { // 1 main query + <= 5 metering
		t.Errorf("쿼리 왕복 %d회, 본 쿼리 포함 ≤ 6 초과", count)
	}
}

// TestReadClassifiesZeroNear — the classification of a zero-hit + near read
// (zero=true, near=true) is reflected only in the 1 read_events row (RecordReadEvent) — the
// read_stats blob write and RecordReadUsage inbound record are abolished (0 calls).
// A hit read is 1 row with (false,false).
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
			return nil, nil // phrase and fallback zero hit
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
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
