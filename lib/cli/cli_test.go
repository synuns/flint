package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"flint/lib/store"
)

// fakeStore — Store 인터페이스 fake. stub 필드(exists·*Err)는 지정 시 우선되고,
// rows·queryFn 미지정 시 영(零) 동작을 반환한다.
type fakeStore struct {
	// stub 조건
	exists        bool
	existsErr     error
	existsProject map[string]bool // name별 존재 판정(nil이면 전부 false)
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)

	// 기록
	inserted []map[string]any
	commits  []string
	queries  []string
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	f.inserted = append(f.inserted, recs...)
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	ids := make([]int, len(recs))
	for i := range ids {
		ids[i] = i + 1
	}
	return ids, nil
}
func (f *fakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return f.exists, f.existsErr
}
func (f *fakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return f.existsProject[name], nil // nil map 조회는 false(영 동작)
}
func (f *fakeStore) Commit(ctx context.Context, msg string) (string, error) {
	f.commits = append(f.commits, msg)
	if f.commitErr != nil {
		return "", f.commitErr
	}
	return "abc123", nil
}
func (f *fakeStore) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeStore) Close()                         {}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return f.rows, nil
}

// withFake — newStore seam 교체. 기본 fake, SUPERLITE_REAL=1이면 실구현 위임
// fake(미설정 stub만 위임). 실DB 미기동 시 스킵.
func withFake(t *testing.T, f *fakeStore) {
	t.Helper()
	prev := newStore
	if os.Getenv("SUPERLITE_REAL") == "1" {
		real, err := newRealStore(context.Background(), dsnFromEnv())
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(func() { real.Close() })
		delegate := real
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return &delegatingFake{fake: f, real: delegate}, nil
		}
	} else {
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return f, nil
		}
	}
	t.Cleanup(func() { newStore = prev })
}

// delegatingFake — stub 미지정 연산은 실구현에 위임해 seam 검증이 실코드를 통과하게 한다.
type delegatingFake struct {
	fake *fakeStore
	real Store
}

func (d *delegatingFake) Migrate(ctx context.Context) error { return d.real.Migrate(ctx) }
func (d *delegatingFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	d.fake.inserted = append(d.fake.inserted, recs...)
	if d.fake.insertErr != nil {
		return nil, d.fake.insertErr
	}
	return d.real.InsertBatch(ctx, recs)
}
func (d *delegatingFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if d.fake.exists || d.fake.existsErr != nil {
		return d.fake.exists, d.fake.existsErr
	}
	return d.real.ExistsHash(ctx, hash)
}
func (d *delegatingFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	if d.fake.existsProject != nil {
		return d.fake.ExistsProject(ctx, name)
	}
	return d.real.ExistsProject(ctx, name)
}
func (d *delegatingFake) Commit(ctx context.Context, msg string) (string, error) {
	d.fake.commits = append(d.fake.commits, msg)
	if d.fake.commitErr != nil {
		return "", d.fake.commitErr
	}
	return d.real.Commit(ctx, msg)
}
func (d *delegatingFake) Ping(ctx context.Context) error {
	if d.fake.pingErr != nil {
		return d.fake.pingErr
	}
	return d.real.Ping(ctx)
}
func (d *delegatingFake) Close() { d.real.Close() }
func (d *delegatingFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	d.fake.queries = append(d.fake.queries, sql)
	if d.fake.queryErr != nil {
		return nil, d.fake.queryErr
	}
	if d.fake.queryFn != nil {
		return d.fake.queryFn(sql, args)
	}
	if d.fake.rows != nil {
		return d.fake.rows, nil
	}
	return d.real.Query(ctx, sql, args...)
}

// mustRun — Run 실행 + exit code assert.
func mustRun(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("출력이 JSON 아님: %q: %v", line, err)
	}
	return m
}

func TestWriteCommitted(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"t","summary":"s","confidence":0.9,"tags":["b","a"]}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" || m["state"] != "active" {
		t.Fatalf("status/state=%v/%v, want committed/active", m["status"], m["state"])
	}
	hash, _ := m["hash"].(string)
	if len(hash) != 64 {
		t.Fatalf("hash 길이=%d, want 64", len(hash))
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입 레코드 수=%d, want 1", len(f.inserted))
	}
	rec := f.inserted[0]
	if rec["tags"] != ":a:b:" {
		t.Fatalf("tags=%v, want :a:b:", rec["tags"])
	}
	if rec["content_hash"] != hash || rec["kind"] != "knowledge" || rec["status"] != "active" {
		t.Fatalf("레코드 정규화/해시 불일치: %v", rec)
	}
	if rec["domain"] != "main" {
		t.Fatalf("domain 기본값=%v, want main", rec["domain"])
	}
	if len(f.commits) != 1 || !strings.HasPrefix(f.commits[0], "knowledge: s") {
		t.Fatalf("커밋 메시지=%v", f.commits)
	}
}

func TestWritePendingBoundary(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"t","summary":"s","confidence":0.79}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if m := decode(t, out); m["state"] != "pending" {
		t.Fatalf("state=%v, want pending", m["state"])
	}
	if f.inserted[0]["status"] != "pending" {
		t.Fatalf("rec status=%v, want pending", f.inserted[0]["status"])
	}
}

func TestWriteDuplicateExit0(t *testing.T) {
	f := &fakeStore{exists: true}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want duplicate 한 줄", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("중복 시 삽입 금지; 삽입됨 %d건", len(f.inserted))
	}
}

func TestWriteJSONLSingleCommit(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "session"},
		`{"agent":"claude","summary":"a"}`+"\n"+`{"agent":"claude","summary":"b"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 2 {
		t.Fatalf("삽입=%d건, want 2", len(f.inserted))
	}
	if len(f.commits) != 1 {
		t.Fatalf("커밋=%d회, want 1(단일 커밋)", len(f.commits))
	}
}

func TestWriteStripsNonColumnKeys(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "project"},
		`{"name":"p1","status":"active","note":"메타데이터"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	rec := f.inserted[0]
	if _, ok := rec["note"]; ok {
		t.Fatalf("비컬럼 키 note가 저장됨: %v", rec)
	}
	if rec["name"] != "p1" || rec["status"] != "active" {
		t.Fatalf("컬럼 유실: %v", rec)
	}
	// 무경고 삭제 금지: 경고 배열이 있어야 한다.
	m := decode(t, out)
	w, ok := m["warnings"].([]any)
	if !ok || len(w) == 0 || !strings.Contains(fmt.Sprint(w[0]), "note") {
		t.Fatalf("dropped keys 경고 없음: %v", m["warnings"])
	}
}

func TestWriteWarnsDroppedKeys(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "session"},
		`{"agent":"claude","summary":"s","tasks":["a","b"]}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if len(f.inserted) != 1 || f.inserted[0]["summary"] != "s" {
		t.Fatalf("저장은 정상이어야 한다: %v", f.inserted)
	}
	m := decode(t, out)
	w, ok := m["warnings"].([]any)
	if !ok || len(w) == 0 || !strings.Contains(fmt.Sprint(w[0]), "tasks") {
		t.Fatalf("warnings=%v, want dropped keys: tasks", m["warnings"])
	}
}

func TestWriteValidationExit3(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"}, `{"topic":"t"}`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
	if m := decode(t, out); m["field"] != "summary" {
		t.Fatalf("field=%v, want summary; out=%s", m["field"], out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("검증 실패 시 삽입 금지")
	}
}

func TestWriteBadJSONExit3(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"}, `not json`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out) // 한 줄 JSON 에러여야 한다
}

func TestServerDownExit2(t *testing.T) {
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })
	out, code := mustRun(t, []string{"write", "knowledge"}, `{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if strings.TrimSpace(out) != `{"error":"server_down"}` {
		t.Fatalf("조용히 한 줄 JSON만: %q", out)
	}
}

func TestRetriesExhaustedExit1(t *testing.T) {
	f := &fakeStore{commitErr: store.ErrRetriesExhausted}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestReadOutput(t *testing.T) {
	f := &fakeStore{rows: [][]any{{float64(1), "topic", "summary text"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"read"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if out != "1|topic|summary text\n" {
		t.Fatalf("출력=%q", out)
	}
	if !strings.Contains(f.queries[0], "active_knowledge") {
		t.Fatalf("뷰 강제 실패: %s", f.queries[0])
	}
}

func TestReadLimitClampAndJSON(t *testing.T) {
	f := &fakeStore{rows: [][]any{{float64(1), "t", "s"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"read", "--limit", "999", "--json", "--fields", "id,topic"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != `{"id":"1","topic":"t"}` {
		t.Fatalf("json 출력=%q", out)
	}
}

func TestHotTruncatedToBudget(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"decision", strings.Repeat("가", 3000), "s", "ts"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"hot", "--session", "S1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(f.queries[0], "hot_view") {
		t.Fatalf("hot_view 조회 아님: %s", f.queries[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "…[truncated]") {
		t.Fatalf("절단 접미 없음(출력 %d바이트)", len(out))
	}
}

func TestStatusOK(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"1"}}, nil
		}
		return [][]any{{int64(2)}}, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["schema_version"] != "1" || m["pending"] != float64(2) || m["status"] != "ok" {
		t.Fatalf("status 출력=%v", m)
	}
}
