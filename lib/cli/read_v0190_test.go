// read_v0190_test.go — effect-metering wiring: a single read is recorded into read_events
// with counts, flags, and the session label (ReadEventStore seam, METRICS §4).
// The fakeStore query-capture pattern is inherited from read_v0150_test.go.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// readEventRecorder — a fake layering ReadEventStore (the seam) onto fakeStore
// (inheriting the readUsageRecorder pattern). It also absorbs RecordReadUsage, simplifying
// the counter paths. Adds recording of ids (the returned-id CSV).
type readEventRecorder struct {
	*fakeStore
	events []readEventRecord
}

// readEventRecord — records RecordReadEvent call arguments.
type readEventRecord struct {
	hits     int
	zero     bool
	near     bool
	fallback bool
	session  string
	ids      string
}

func (r *readEventRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return nil
}

func (r *readEventRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, readEventRecord{ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs})
	return nil
}

// hitRowsFixture — the hit read's active_knowledge rows (9 columns, the v0150 convention).
var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

// TestReadEventRecordedOnHit — a hit read records the result count and session
// label as-is (hits=2, zero=false, fallback=false).
func TestReadEventRecordedOnHit(t *testing.T) {
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return hitRowsFixture, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	code := runRead(context.Background(), st,
		[]string{"--session", "s-42", "백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("이벤트 %d건, want 1", len(st.events))
	}
	ev := st.events[0]
	if ev.hits != 2 || ev.zero || ev.near || ev.fallback {
		t.Errorf("이벤트 = {%d %v %v %v}, want {2 false false false}", ev.hits, ev.zero, ev.near, ev.fallback)
	}
	if ev.session != "s-42" {
		t.Errorf("session = %q, want s-42", ev.session)
	}
	// the returned-id CSV (top 10) — "1,2" for a 2-row hit.
	if ev.ids != "1,2" {
		t.Errorf("ids = %q, want 1,2", ev.ids)
	}
}

// TestReadEventRecordsFallback — a read revived by the zero-hit -> fallback re-query
// is recorded with fallback=true (the fallback flag passed through).
func TestReadEventRecordsFallback(t *testing.T) {
	// auto-label adoption isolation — prevents inheriting the ambient session label.
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " OR "):
			// fallback OR re-query — 1 row recovered (escaping the zero hit).
			return [][]any{{"3", "백업 상태", "매일 점검", ":b:", "", "", "0.9", "active", "0"}}, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil // the main query zero-hits
	}}}
	var out strings.Builder
	// a multi-token query — a single token is identical to the fallback syntax, so it never triggers.
	code := runRead(context.Background(), st,
		[]string{"백업 정책 상태"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("이벤트 %d건, want 1", len(st.events))
	}
	ev := st.events[0]
	if ev.hits != 1 || ev.zero || ev.near || !ev.fallback {
		t.Errorf("이벤트 = {%d %v %v %v}, want {1 false false true}", ev.hits, ev.zero, ev.near, ev.fallback)
	}
	if ev.session != "" {
		t.Errorf("session = %q, want 빈 라벨(미지정)", ev.session)
	}
}

// TestReadSessionFlagValidation — a trailing --session with no value follows the
// same rule as other value flags: exit 3. A flag-shaped value
// (the `--session --json` absorption typo) is also exit 3 — since the label persists in read_events
// for 14 days, preventing DB pollution takes precedence over lookup convenience.
func TestReadSessionFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--session"},           // no value
		{"--session", "--json"}, // flag-absorption typo
		{"--session", "-q"},     // short flag form
	} {
		st := &fakeStore{}
		var out strings.Builder
		code := runRead(context.Background(), st, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("args=%v exit = %d, want 3: %s", args, code, out.String())
		}
		if !strings.Contains(out.String(), `"session"`) {
			t.Errorf("args=%v 오류 필드에 session 없음: %s", args, out.String())
		}
	}
}
