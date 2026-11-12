// read_v0420_test.go — v0.42.0 T2 Review Focus: read_stats blob 폐지 후
// read 1회 쓰기 4→2(BumpReads·RecordReadEvent만). read 경로가 meta
// read_stats 키를 읽거나 쓰지 않는다(운영 DB 잔존 blob은 무해 잔류 —
// 접근 부재가 계약). 유입 계측(RecordReadUsage daily 경로)도 read kind
// 에서는 더 호출되지 않는다.
package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// readBudgetRecorder — BumpReads·RecordReadEvent·RecordReadUsage 호출과
// meta 질의(SQL·인자)를 전부 녹음하는 fake(readEventRecorder 관례 승계).
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

// newReadBudgetRecorder — active_knowledge 조회만 응답하는 녹음 fake.
// hit=true면 구문 히트 행, 아니면 제로히트+제안 스캔 1행(근접 분류).
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
			// 제안 스캔(3열) — 제로히트 read의 근접 후보 1행. 히트 read는
			// 애초에 스캔이 발동하지 않는다.
			return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
		}
		if strings.Contains(sql, "FROM active_knowledge") {
			if hit {
				return hitRowsFixture, nil
			}
			return nil, nil // 구문·폴백 제로히트
		}
		return nil, nil
	}
	return r
}

// TestReadWritesReduced — Review Focus: read 호출(히트·제로히트 각 1회)
// 후 meta 쓰기에서 read_stats 키 부재 단언. 계측은 BumpReads(참조 증분)
// ·RecordReadEvent(이벤트 1행)만 남는다.
func TestReadWritesReduced(t *testing.T) {
	// ① 히트 read — BumpReads ids 전수 + read_events 1행(hits·zero=false).
	hit := newReadBudgetRecorder(true)
	var out strings.Builder
	if code := RunRead(context.Background(), hit, []string{"백업 정책"}, nil, &out); code != 0 {
		t.Fatalf("hit read exit=%d: %s", code, out.String())
	}
	// ② 제로히트+근접 read — read_events 1행(zero·near=true).
	zn := newReadBudgetRecorder(false)
	var out2 strings.Builder
	if code := RunRead(context.Background(), zn, []string{"백업 복구 절차"}, nil, &out2); code != 1 {
		t.Fatalf("zero-near read exit=%d, want 1(침묵): %s", code, out2.String())
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
