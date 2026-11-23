package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func compactRun(t *testing.T, f *fakeStore, stdin string) (string, int) {
	t.Helper()
	withFake(t, f)
	out, code := mustRun(t, []string{"compact"}, stdin)
	return out, code
}

func test_compact_writes_session(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{}
	out, code := compactRun(t, f,
		`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"API 설계 완료"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입 레코드 수=%d, want 1", len(f.inserted))
	}
	rec := f.inserted[0]
	if rec["kind"] != "session" || rec["summary"] != "API 설계 완료" {
		t.Fatalf("레코드=%v, want session/API 설계 완료", rec)
	}
	if rec["agent"] != "claude" {
		t.Fatalf("agent=%v, want claude", rec["agent"])
	}
	if ts, _ := rec["ts"].(string); ts == "" {
		t.Fatalf("ts 비빔: %v", rec)
	}
	m, err := ReadMarker()
	if err != nil || m == nil {
		t.Fatalf("마커 부재/오류: %v %v", m, err)
	}
	if m.SessionID != "s1" || m.Reason != "compact" {
		t.Fatalf("마커=%+v, want s1/compact", m)
	}
	if len(f.commits) != 1 {
		t.Fatalf("커밋 수=%d, want 1", len(f.commits))
	}
}

func test_compact_bad_stdin_exit0(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{}
	out, code := compactRun(t, f, "not json")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("삽입 발생: %v", f.inserted)
	}
	if m, _ := ReadMarker(); m != nil {
		t.Fatalf("마커 존재: %+v", m)
	}
}

func test_compact_empty_summary_noop(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{}
	out, code := compactRun(t, f,
		`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":""}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("삽입 발생: %v", f.inserted)
	}
	if m, _ := ReadMarker(); m != nil {
		t.Fatalf("마커 존재: %+v", m)
	}
}

func test_compact_no_session_id_no_marker(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{}
	out, code := compactRun(t, f,
		`{"hook_event_name":"PostCompact","compact_summary":"요약"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입 레코드 수=%d, want 1", len(f.inserted))
	}
	if m, _ := ReadMarker(); m != nil {
		t.Fatalf("마커 존재: %+v", m)
	}
}

func test_compact_write_fail_exit0(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{insertErr: errFakeInsert}
	out, code := compactRun(t, f,
		`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"요약"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out)
	}
	if m, _ := ReadMarker(); m != nil {
		t.Fatalf("마커 존재: %+v", m)
	}
}

func test_compact_st_nil_noop(t *testing.T) {
	var out bytes.Buffer
	code := runCompact(context.Background(), nil, nil,
		strings.NewReader(`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"요약"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out.String() != "" {
		t.Fatalf("stdout=%q, want 빈 문자열", out.String())
	}
}

func TestCompact(t *testing.T) {
	t.Run("writes_session", test_compact_writes_session)
	t.Run("bad_stdin_exit0", test_compact_bad_stdin_exit0)
	t.Run("empty_summary_noop", test_compact_empty_summary_noop)
	t.Run("no_session_id_no_marker", test_compact_no_session_id_no_marker)
	t.Run("write_fail_exit0", test_compact_write_fail_exit0)
	t.Run("st_nil_noop", test_compact_st_nil_noop)
	t.Run("store_error_quiet", test_compact_store_error_quiet)
	t.Run("accrues_session_knowledge", test_compact_accrues_session_knowledge)
}

func test_compact_accrues_session_knowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	f := &fakeStore{}

	cleanupRealResidue(`DELETE FROM meta WHERE key = 'session_knowledge_last'`)
	summary := fmt.Sprintf("API 설계와 구현을 마쳤고 리뷰 피드백을 반영했다 (%s %d)",
		t.Name(), time.Now().UnixNano())
	out, code := compactRun(t, f,
		`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"`+summary+`"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	var session, knowledge int
	for _, rec := range f.inserted {
		if rec["kind"] == "session" {
			session++
		}
		if rec["kind"] == "knowledge" {
			knowledge++
			if rec["status"] != "pending" {
				t.Errorf("지식 상태=%v, want pending", rec["status"])
			}
			if tags, ok := rec["tags"].(string); !ok || tags != ":process:" {
				t.Errorf("지식 tags=%#v, want \":process:\"", rec["tags"])
			}
		}
	}
	if session != 1 {
		t.Fatalf("세션 행=%d, want 1", session)
	}
	if knowledge != 1 {
		t.Fatalf("세션 지식화 적립=%d, want 1(PostCompact 표준 경로)", knowledge)
	}
}

func test_compact_store_error_quiet(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, errors.New("connection refused")
	}
	t.Cleanup(func() { newStore = prev })

	var out bytes.Buffer
	code := Run([]string{"compact"},
		strings.NewReader(`{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"요약"}`),
		&out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out.String() != "" {
		t.Fatalf("stdout=%q, want 빈 문자열(에러 JSON 누출)", out.String())
	}
	if m, _ := ReadMarker(); m != nil {
		t.Fatalf("마커 존재: %+v", m)
	}
}

var errFakeInsert = errors.New("insert fail")
