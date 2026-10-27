// write_test.go — v0.9.2 회귀: JSONL 다중 쓰기 줄별 dedup. 계약: duplicate
// 줄은 skip(신규 줄 부분 커밋), 전부 duplicate 재실행은 0건 커밋·exit 0
// "duplicate"(시딩 멱등 유지), 2줄 이상 출력은 집계형(inserted·duplicates·
// 커밋 hash) — id/state 혼합 없음. 단일 줄 출력은 기존 불변. 레이스
// (InsertBatch ErrDuplicate)는 재확인 skip + 잔여 재시도로 신규줄 유실 제거.
package cli

import (
	"context"
	"strings"
	"testing"

	"flint/lib/store"
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

// raceDupStore — 레이스 재현 fake: 사전 ExistsHash는 전부 통과(false),
// InsertBatch 첫 호출만 store.ErrDuplicate(동시 삽입으로 트랜잭션 롤백).
// 이후 ExistsHash는 앞 racedDups줄만 존재(true)로 응답(재확인 시점).
// failRetry면 재시도 배치도 계속 ErrDuplicate(실제 충돌).
type raceDupStore struct {
	*fakeStore
	insertCalls int
	rechecks    int
	racedDups   int
	failRetry   bool
}

func (r *raceDupStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	r.insertCalls++
	if r.insertCalls == 1 || r.failRetry {
		return nil, store.ErrDuplicate
	}
	return r.fakeStore.InsertBatch(ctx, recs)
}

func (r *raceDupStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if r.insertCalls == 0 {
		return false, nil // 사전 확인: 전부 신규로 통과
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil // 재확인: 앞 racedDups줄만 동시 삽입됨
}

// withRace — newStore seam을 raceDupStore로 교체.
func withRace(t *testing.T, r *raceDupStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return r, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteRaceRecoversNewLines — 레이스(사전 확인 통과 후 배치에서 일부만
// 동시 삽입): 재확인으로 존재 줄은 skip, 신규 줄은 재시도로 커밋(회귀: 전체
// 롤백 + exit 0 "duplicate"로 신규줄 유실). inserted=1·duplicates=1.
func TestWriteRaceRecoversNewLines(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, racedDups: 1}
	withRace(t, rs)
	jsonl := `{"topic":"rc-a","summary":"raced","confidence":0.9}` + "\n" +
		`{"topic":"rc-b","summary":"fresh","confidence":0.9}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" || m["inserted"] != float64(1) || m["duplicates"] != float64(1) {
		t.Fatalf("출력=%v, want committed/inserted=1/duplicates=1", m)
	}
	if len(f.inserted) != 1 || f.inserted[0]["topic"] != "rc-b" {
		t.Fatalf("신규 rc-b만 삽입되어야 한다: %v", f.inserted)
	}
	if len(f.commits) != 1 {
		t.Fatalf("커밋=%d회, want 1(부분 커밋)", len(f.commits))
	}
	if rs.insertCalls != 2 {
		t.Fatalf("InsertBatch=%d회, want 2(초기+재시도 1회)", rs.insertCalls)
	}
}

// TestWriteRaceAllDupIdempotent — 레이스 후 재확인에서 전부 존재: 신규줄
// 유실 없이 0건 커밋·exit 0 "duplicate"(시딩 멱등 계약 유지).
func TestWriteRaceAllDupIdempotent(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, racedDups: 1}
	withRace(t, rs)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"rc-c","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want duplicate 한 줄", out)
	}
	if len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("0건 커밋: 삽입 %d·커밋 %d", len(f.inserted), len(f.commits))
	}
	if rs.insertCalls != 1 {
		t.Fatalf("InsertBatch=%d회, want 1(재확인 전부 존재→재시도 없음)", rs.insertCalls)
	}
}

// TestWriteRaceRetryConflictErrors — 재확인을 통과한 줄의 재시도까지
// ErrDuplicate면 실제 충돌: exit 0 "duplicate"가 아닌 에러 보고(회귀: 유실
// 가능한 경로의 exit 0 성공 위장).
func TestWriteRaceRetryConflictErrors(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, failRetry: true}
	withRace(t, rs)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"rc-d","summary":"s","confidence":0.9}`)
	if code != 1 {
		t.Fatalf("exit=%d, want 1(실제 충돌 에러); out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("출력=%q, want error JSON", out)
	}
	if strings.Contains(out, `"status":"duplicate"`) {
		t.Fatalf("출력=%q, want duplicate 성공 위장 금지", out)
	}
	if len(f.commits) != 0 {
		t.Fatalf("커밋=%d회, want 0", len(f.commits))
	}
}

// raceProjectStore — project 레이스 재현 fake: 사전 ExistsProject는 전부
// 통과(false), InsertBatch 첫 호출만 store.ErrDuplicate(동시 삽입 롤백),
// 이후 ExistsProject는 존재(true)로 응답(재확인 시점 — 자연키 name 확인).
type raceProjectStore struct {
	*fakeStore
	insertCalls int
}

func (r *raceProjectStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	r.insertCalls++
	if r.insertCalls == 1 {
		return nil, store.ErrDuplicate
	}
	return r.fakeStore.InsertBatch(ctx, recs)
}

func (r *raceProjectStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return r.insertCalls > 0, nil
}

// TestWriteProjectKindDedup — project kind 자연키(name) 줄별 dedup: 기존
// name 줄은 skip, 신규 줄만 커밋·duplicates=1·exit 0(회귀: project는
// content_hash가 없어 사전 dedup·재확인 모두 불가 → InsertBatch unique(name)
// 충돌 → 재시도 재충돌 "batch conflict" 에러로 첫 시딩 실패).
func TestWriteProjectKindDedup(t *testing.T) {
	t.Run("pre-check mixed", func(t *testing.T) {
		f := &fakeStore{existsProject: map[string]bool{"메이플 머지게임": true}}
		withFake(t, f)
		jsonl := `{"name":"메이플 머지게임","status":"active","summary":"기존"}` + "\n" +
			`{"name":"새 프로젝트","status":"active","summary":"신규"}`
		out, code := mustRun(t, []string{"write", "project"}, jsonl)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		m := decode(t, out)
		if m["status"] != "committed" || m["inserted"] != float64(1) || m["duplicates"] != float64(1) {
			t.Fatalf("출력=%v, want committed/inserted=1/duplicates=1", m)
		}
		if len(f.inserted) != 1 || f.inserted[0]["name"] != "새 프로젝트" {
			t.Fatalf("신규 프로젝트만 삽입되어야 한다: %v", f.inserted)
		}
		if len(f.commits) != 1 {
			t.Fatalf("커밋=%d회, want 1(부분 커밋)", len(f.commits))
		}
	})
	t.Run("race recheck", func(t *testing.T) {
		f := &fakeStore{}
		rs := &raceProjectStore{fakeStore: f}
		prev := newStore
		newStore = func(ctx context.Context, dsn string) (Store, error) { return rs, nil }
		t.Cleanup(func() { newStore = prev })
		out, code := mustRun(t, []string{"write", "project"},
			`{"name":"레이스 프로젝트","status":"active"}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if strings.TrimSpace(out) != `{"status":"duplicate"}` {
			t.Fatalf("출력=%q, want duplicate 한 줄(재확인 skip → 0건 커밋)", out)
		}
		if len(f.inserted) != 0 || len(f.commits) != 0 {
			t.Fatalf("0건 커밋: 삽입 %d·커밋 %d", len(f.inserted), len(f.commits))
		}
		if rs.insertCalls != 1 {
			t.Fatalf("InsertBatch=%d회, want 1(재확인 전부 존재→재시도 없음)", rs.insertCalls)
		}
	})
}

// countCall — RecordUsageCount 호출 기록(kind, source, count).
type countCall struct {
	kind, source string
	n            int
}

// countRecorder — RecordUsage(1회)·RecordUsageCount(일괄) 호출을 녹음하는
// fake: realStore 계측 세임(#5: dup N을 1회 호출로 일괄 증분). existsFn은
// 줄별 dedup 시나리오용 훅(미지정 시 fakeStore 동작).
type countRecorder struct {
	*fakeStore
	existsFn func() bool
	singles  []countCall
	counts   []countCall
}

func (c *countRecorder) RecordUsage(ctx context.Context, kind, source string) error {
	c.singles = append(c.singles, countCall{kind, source, 1})
	return nil
}

func (c *countRecorder) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	c.counts = append(c.counts, countCall{kind, source, count})
	return nil
}

func (c *countRecorder) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if c.existsFn != nil {
		return c.existsFn(), nil
	}
	return c.fakeStore.ExistsHash(ctx, hash)
}

// withCounter — newStore seam을 countRecorder로 교체.
func withCounter(t *testing.T, c *countRecorder) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return c, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteDupBatchSingleCountCall — dup 계측은 줄 수와 무관하게
// RecordUsageCount 1회 호출(회귀: recordDups가 RecordUsage를 N회 루프 — O(N)
// 읽기-쓰기). write 1회(RecordUsage)·dup N(RecordUsageCount) 각 1회.
func TestWriteDupBatchSingleCountCall(t *testing.T) {
	t.Run("all-dup", func(t *testing.T) {
		c := &countRecorder{fakeStore: &fakeStore{exists: true}}
		withCounter(t, c)
		out, code := mustRun(t, []string{"write", "knowledge"}, strings.Join([]string{
			`{"topic":"cc-a","summary":"a","confidence":0.9}`,
			`{"topic":"cc-b","summary":"b","confidence":0.9}`,
			`{"topic":"cc-c","summary":"c","confidence":0.9}`,
		}, "\n"))
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if len(c.singles) != 1 || c.singles[0] != (countCall{"write", "manual", 1}) {
			t.Fatalf("write 계측=%v, want [write manual] 1회", c.singles)
		}
		if len(c.counts) != 1 || c.counts[0] != (countCall{"dup", "", 3}) {
			t.Fatalf("dup 계측=%v, want [dup \"\" 3] 1회 일괄", c.counts)
		}
	})
	t.Run("mixed", func(t *testing.T) {
		calls := 0
		c := &countRecorder{fakeStore: &fakeStore{}, existsFn: func() bool {
			calls++
			return calls <= 2 // 첫 2줄만 기존 레코드
		}}
		withCounter(t, c)
		jsonl := `{"topic":"rv-e","summary":"a","confidence":0.9}` + "\n" +
			`{"topic":"rv-f","summary":"b","confidence":0.9}` + "\n" +
			`{"topic":"rv-g","summary":"c","confidence":0.9}`
		out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		m := decode(t, out)
		if m["inserted"] != float64(1) || m["duplicates"] != float64(2) {
			t.Fatalf("inserted/duplicates=%v/%v, want 1/2", m["inserted"], m["duplicates"])
		}
		if len(c.singles) != 1 || c.singles[0] != (countCall{"write", "manual", 1}) {
			t.Fatalf("write 계측=%v, want [write manual] 1회", c.singles)
		}
		if len(c.counts) != 1 || c.counts[0] != (countCall{"dup", "", 2}) {
			t.Fatalf("dup 계측=%v, want [dup \"\" 2] 1회 일괄(실제 skip 수)", c.counts)
		}
	})
}

// TestWriteAllDuplicateKeepsWarnings — 전부 duplicate여도 경고(비밀 스캔)는
// 폐기되지 않는다(회귀: recs==0 분기가 경고 없이 duplicate 한 줄만 출력).
func TestWriteAllDuplicateKeepsWarnings(t *testing.T) {
	f := &fakeStore{exists: true}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"warn-dup","summary":"token ghp_0123456789abcdefghijklm","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "duplicate" {
		t.Fatalf("출력=%v, want duplicate", m)
	}
	w, ok := m["warnings"].([]any)
	if !ok || len(w) == 0 || !strings.Contains(w[0].(string), "github-token") {
		t.Fatalf("경고 폐기: %v", m)
	}
}
