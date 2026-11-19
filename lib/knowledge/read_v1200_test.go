// read_v1200_test.go — the recording wiring (read surface).
// TestReadEventColumns (bytes=output len·source label·zero_reason
// branches)·TestZeroReasonDecompose (the threshold-branch pure
// function)·the read full id set into knowledge_reads (kind=read).
// Contract: one read's bytes·zero_reason·source are recorded in one
// read_events row, and the full returned id set is left in
// knowledge_reads with kind='read' (the BumpReads cache path untouched —
// covered separately by read_v0420_test).
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// v1200Recorder — a fake recording the full ReadEvent value·
// RecordKnowledgeReads calls (implementing the ReadEventStore·
// KnowledgeReadsRecorder seams together).
type v1200Recorder struct {
	*fakeStore
	events []db.ReadEvent
	kreads []kreadsCall
}

// kreadsCall — a recorded RecordKnowledgeReads call's arguments.
type kreadsCall struct {
	ids     []int
	session string
	kind    string
}

func (r *v1200Recorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func (r *v1200Recorder) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	r.kreads = append(r.kreads, kreadsCall{ids, session, kind})
	return nil
}

// activeCountQueryRows — the active-knowledge count query answer for
// zero-hit decomposition ( count(*) ).
func activeCountQueryRows(n int32) [][]any {
	return [][]any{{n}}
}

// TestReadEventColumns — one read's ReadEvent carries bytes (len of the
// final output)·the source label (session label→hook·absent→cli·explicit
// wins)·the zero_reason branch (unowned/owned_miss/empty).
func TestReadEventColumns(t *testing.T) {
	ctx := context.Background()
	t.Run("히트 — bytes=출력 len·source=cli·zero_reason 공란", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.events) != 1 {
			t.Fatalf("read_events 기록 %d건, want 1", len(st.events))
		}
		ev := st.events[0]
		if ev.Bytes != len(out.String()) {
			t.Errorf("bytes = %d, want %d(len 최종 출력)", ev.Bytes, len(out.String()))
		}
		if ev.Source != "cli" {
			t.Errorf("source = %q, want cli(세션 라벨 부재)", ev.Source)
		}
		if ev.ZeroReason != "" {
			t.Errorf("zero_reason = %q, want \"\"(zero=0)", ev.ZeroReason)
		}
	})
	t.Run("세션 라벨 — source=hook", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--session", "s1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if ev := st.events[0]; ev.Source != "hook" {
			t.Errorf("source = %q, want hook(세션 라벨 채택)", ev.Source)
		}
	})
	t.Run("--source mcp — 명시 라벨 우선", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source", "mcp", "--session", "s1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if ev := st.events[0]; ev.Source != "mcp" {
			t.Errorf("source = %q, want mcp(명시 우선)", ev.Source)
		}
	})
	t.Run("--source 무효값 — exit 3", func(t *testing.T) {
		st := &fakeStore{}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source", "bogus", "q"}, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("exit = %d, want 3(무효 source)", code)
		}
	})
	t.Run("--source 값 없음 — exit 3", func(t *testing.T) {
		st := &fakeStore{}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source"}, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("exit = %d, want 3(빈 source)", code)
		}
	})
	t.Run("제로히트 — active 0 → unowned", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(0), nil
			}
			return nil, nil // active_knowledge zero-hit·fallback·suggestion all 0 rows
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "unowned" {
			t.Errorf("zero_reason = %q, want unowned(active 0 < floor 1)", ev.ZeroReason)
		}
	})
	t.Run("제로히트 — active≥1 → owned_miss", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(2), nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "owned_miss" {
			t.Errorf("zero_reason = %q, want owned_miss(active 2 ≥ floor 1)", ev.ZeroReason)
		}
	})
	t.Run("제로히트 — zero_owned_floor 오버라이드(3)·active 2 → unowned", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(2), nil
			case strings.Contains(sql, "SELECT value FROM meta"):
				return [][]any{{"3"}}, nil // zero_owned_floor=3
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "unowned" {
			t.Errorf("zero_reason = %q, want unowned(active 2 < floor 3)", ev.ZeroReason)
		}
	})
}

// TestZeroReasonDecompose — the zero-hit decomposition pure function
// (the threshold-branch table).
func TestZeroReasonDecompose(t *testing.T) {
	cases := []struct {
		name          string
		zero          bool
		active, floor int
		want          string
	}{
		{"zero=0 — 분해 없음", false, 0, 1, ""},
		{"zero·active 0 — 미보유", true, 0, 1, "unowned"},
		{"zero·active 1(기본 임계) — 보유탐색실패", true, 1, 1, "owned_miss"},
		{"zero·floor 3·active 2 — 미보유", true, 2, 3, "unowned"},
		{"zero·floor 3·active 3 — 보유탐색실패", true, 3, 3, "owned_miss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ZeroReason(tc.zero, tc.active, tc.floor); got != tc.want {
				t.Errorf("ZeroReason(%v, %d, %d) = %q, want %q", tc.zero, tc.active, tc.floor, got, tc.want)
			}
		})
	}
}

// TestReadRecordsKnowledgeReads — the read-returned full id set is
// recorded in knowledge_reads (kind=read, session label accompanying).
func TestReadRecordsKnowledgeReads(t *testing.T) {
	ctx := context.Background()
	st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return hitRowsFixture, nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunRead(ctx, st, []string{"--session", "kr-1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if len(st.kreads) != 1 {
		t.Fatalf("knowledge_reads 기록 %d건, want 1", len(st.kreads))
	}
	call := st.kreads[0]
	if len(call.ids) != 2 || call.ids[0] != 1 || call.ids[1] != 2 {
		t.Errorf("kreads ids = %v, want [1 2](반환 id 전수)", call.ids)
	}
	if call.session != "kr-1" || call.kind != "read" {
		t.Errorf("kreads session·kind = %q·%q, want kr-1·read", call.session, call.kind)
	}
}
