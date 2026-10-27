// write_test.go — v0.9.2 회귀: JSONL 다중 쓰기 줄별 dedup. 계약: duplicate
// 줄은 skip(신규 줄 부분 커밋), 전부 duplicate 재실행은 0건 커밋·exit 0
// "duplicate"(시딩 멱등 유지), 2줄 이상 출력은 집계형(inserted·duplicates·
// 커밋 hash) — id/state 혼합 없음. 단일 줄 출력은 기존 불변.
package cli

import (
	"context"
	"strings"
	"testing"
)

// existsHashStore — ExistsHash만 호출 시점별로 바꾼 fake(줄별 dedup 시나리오).
type existsHashStore struct {
	*fakeStore
	existsFn func() bool
}

func (e *existsHashStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if e.existsFn != nil {
		return e.existsFn(), nil
	}
	return e.fakeStore.ExistsHash(ctx, hash)
}

// withExistsHash — newStore seam을 existsHashStore로 교체.
func withExistsHash(t *testing.T, e *existsHashStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return e, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteJSONLMixedCommitsNew — 신규+duplicate 혼합: duplicate 줄은 skip,
// 신규 줄만 커밋(부분 커밋). exit 0·inserted=1·duplicates=1.
func TestWriteJSONLMixedCommitsNew(t *testing.T) {
	calls := 0
	f := &fakeStore{}
	hs := &existsHashStore{fakeStore: f, existsFn: func() bool {
		calls++
		return calls == 1 // 첫 줄(rv-b)만 기존 레코드
	}}
	withExistsHash(t, hs)
	jsonl := `{"topic":"rv-b","summary":"existing","confidence":0.9}` + "\n" +
		`{"topic":"rv-c","summary":"fresh","confidence":0.9}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" || m["inserted"] != float64(1) || m["duplicates"] != float64(1) {
		t.Fatalf("출력=%v, want committed/inserted=1/duplicates=1", m)
	}
	if len(f.inserted) != 1 || f.inserted[0]["topic"] != "rv-c" {
		t.Fatalf("신규 rv-c만 삽입되어야 한다: %v", f.inserted)
	}
	if len(f.commits) != 1 {
		t.Fatalf("커밋=%d회, want 1(부분 커밋)", len(f.commits))
	}
}

// TestWriteJSONLAllDuplicateIdempotent — 전부 duplicate 재실행: 0건 커밋·
// exit 0·{"status":"duplicate"} 한 줄(시딩 멱등 계약).
func TestWriteJSONLAllDuplicateIdempotent(t *testing.T) {
	f := &fakeStore{exists: true}
	withFake(t, f)
	jsonl := `{"topic":"sd-a","summary":"a","confidence":0.9}` + "\n" +
		`{"topic":"sd-b","summary":"b","confidence":0.9}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want duplicate 한 줄", out)
	}
	if len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("재실행 시 0건 커밋: 삽입 %d·커밋 %d", len(f.inserted), len(f.commits))
	}
}

// TestWriteJSONLMultiOutputNoFieldMix — 2신규(pending+active 혼합 confidence):
// 다중 줄 출력에 id·state 없음·inserted=2·hash는 실제 커밋 1개(커밋 해시).
func TestWriteJSONLMultiOutputNoFieldMix(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	jsonl := `{"topic":"mx-a","summary":"low","confidence":0.5}` + "\n" +
		`{"topic":"mx-b","summary":"high","confidence":0.95}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if _, ok := m["id"]; ok {
		t.Fatalf("다중 줄 출력에 id 혼재: %v", m)
	}
	if _, ok := m["state"]; ok {
		t.Fatalf("다중 줄 출력에 state 혼재: %v", m)
	}
	if m["inserted"] != float64(2) || m["duplicates"] != float64(0) {
		t.Fatalf("inserted/duplicates=%v/%v, want 2/0", m["inserted"], m["duplicates"])
	}
	if m["hash"] != "abc123" {
		t.Fatalf("hash=%v, want 실제 커밋 해시(abc123)", m["hash"])
	}
	if len(f.commits) != 1 || len(f.inserted) != 2 {
		t.Fatalf("커밋=%d·삽입=%d, want 1·2", len(f.commits), len(f.inserted))
	}
	if f.inserted[0]["status"] != "pending" || f.inserted[1]["status"] != "active" {
		t.Fatalf("줄별 판정(pending/active) 유실: %v", f.inserted)
	}
}
