package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

type readBudgetRecorder struct {
	*fakeStore
	bumped  [][]int
	events  []db.ReadEvent
	usages  [][2]bool
	metaOps []struct {
		sql  string
		args []any
	}
}

func (r *readBudgetRecorder) BumpReads(ctx context.Context, ids []int) error {
	r.bumped = append(r.bumped, ids)
	return nil
}

func (r *readBudgetRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	r.usages = append(r.usages, [2]bool{zero, near})
	return nil
}

func (r *readBudgetRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func newReadBudgetRecorder(hit bool) *readBudgetRecorder {
	r := &readBudgetRecorder{fakeStore: &fakeStore{}}
	r.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INSERT INTO meta") ||
			strings.Contains(sql, "SELECT value FROM meta") {
			r.metaOps = append(r.metaOps, struct {
				sql  string
				args []any
			}{sql, args})
			return nil, nil
		}
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {

			return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
		}
		if strings.Contains(sql, "FROM recall_knowledge") || strings.Contains(sql, "FROM active_knowledge") {
			if hit {
				return hitRowsFixture, nil
			}
			return nil, nil
		}
		return nil, nil
	}
	return r
}

func TestReadWritesReduced(t *testing.T) {

	hit := newReadBudgetRecorder(true)
	var out strings.Builder
	if code := RunRead(context.Background(), hit, []string{"백업 정책"}, nil, &out); code != 0 {
		t.Fatalf("hit read exit=%d: %s", code, out.String())
	}

	zn := newReadBudgetRecorder(false)
	var out2 strings.Builder
	if code := RunRead(context.Background(), zn, []string{"백업 복구 절차"}, nil, &out2); code != 0 {
		t.Fatalf("zero-near read exit=%d, want 0(제로히트 exit 0 — v1.5.3): %s", code, out2.String())
	}

	for name, r := range map[string]*readBudgetRecorder{"hit": hit, "zero-near": zn} {
		if len(r.events) != 1 {
			t.Errorf("%s: read_events 기록 %d건, want 1(계측 원천)", name, len(r.events))
		}
		if len(r.usages) != 0 {
			t.Errorf("%s: RecordReadUsage %d회 호출, want 0(read kind daily 쓰기 폐지): %v", name, len(r.usages), r.usages)
		}
		for _, op := range r.metaOps {
			if strings.Contains(op.sql, "read_stats") {
				t.Errorf("%s: read_stats 질의 잔존: %s", name, op.sql)
			}
			for _, a := range op.args {
				if s, ok := a.(string); ok && (s == "read_stats" || s == "last_read_at" || strings.HasPrefix(s, "daily:")) {
					t.Errorf("%s: 폐지 키 meta 쓰기(%s): %s", name, s, op.sql)
				}
			}
		}
		for _, q := range r.fakeStore.queries {
			if strings.Contains(q, "read_stats") {
				t.Errorf("%s: read_stats SQL 잔존: %s", name, q)
			}
		}
	}
	if ev := hit.events[0]; ev.Hits != 2 || ev.Zero || ev.Near {
		t.Errorf("hit read 이벤트 = %+v, want {Hits:2 Zero:false Near:false}", ev)
	}
	if ev := zn.events[0]; ev.Hits != 0 || !ev.Zero || !ev.Near {
		t.Errorf("zero-near read 이벤트 = %+v, want {Hits:0 Zero:true Near:true}", ev)
	}
}
