package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeStore struct {
	wire.HistoryStubs

	exists        bool
	existsErr     error
	existsProject map[string]bool
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)

	hashExistsFn func(hash string) (DupMeta, bool)

	inserted    []map[string]any
	commits     []string
	queries     []string
	linkInserts [][3]any

	queryLog []writeQueryCall
}

type writeQueryCall struct {
	SQL  string
	Args []any
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	return 0, "", nil
}
func (f *fakeStore) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	return nil
}
func (f *fakeStore) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	return nil
}
func (f *fakeStore) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	return nil
}
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
	return f.existsProject[name], nil
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

func (f *fakeStore) InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error {
	f.linkInserts = append(f.linkInserts, [3]any{fromID, toID, relation})
	return nil
}

func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	f.queryLog = append(f.queryLog, writeQueryCall{SQL: sql, Args: args})
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}

	if strings.Contains(sql, "content_hash = ANY($1)") {
		if hashes, ok := args[0].([]string); ok {
			var rows [][]any
			for _, h := range hashes {
				meta, exists := DupMeta{}, f.exists
				if f.hashExistsFn != nil {
					meta, exists = f.hashExistsFn(h)
				}
				if exists {
					rows = append(rows, []any{h, meta.ID, meta.Topic, meta.State})
				}
			}
			return rows, nil
		}
	}
	return f.rows, nil
}

func run(t *testing.T, st wire.Store, args []string, stdin string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunWrite(context.Background(), st, args, strings.NewReader(stdin), &out)
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

func TestWriteJSONLMixedCommitsNew(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: 11}, calls == 1
	}}
	jsonl := `{"topic":"rv-b","summary":"existing","confidence":0.9}` + "\n" +
		`{"topic":"rv-c","summary":"fresh","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge"}, jsonl)
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

func TestWriteJSONLAllDuplicateIdempotent(t *testing.T) {
	f := &fakeStore{exists: true}
	jsonl := `{"topic":"sd-a","summary":"a","confidence":0.9}` + "\n" +
		`{"topic":"sd-b","summary":"b","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge"}, jsonl)
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

func TestWriteJSONLMultiOutputNoFieldMix(t *testing.T) {
	f := &fakeStore{}
	jsonl := `{"topic":"mx-a","summary":"low","confidence":0.5}` + "\n" +
		`{"topic":"mx-b","summary":"high","confidence":0.95}`
	out, code := run(t, f, []string{"knowledge"}, jsonl)
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
	if f.inserted[0]["status"] != "pending" || f.inserted[1]["status"] != "pending" {
		t.Fatalf("claim 없는 knowledge는 conf 무관 pending 강제: %v", f.inserted)
	}
}

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
		return false, nil
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil
}

func TestWriteRaceRecoversNewLines(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, racedDups: 1}
	jsonl := `{"topic":"rc-a","summary":"raced","confidence":0.9}` + "\n" +
		`{"topic":"rc-b","summary":"fresh","confidence":0.9}`
	out, code := run(t, rs, []string{"knowledge"}, jsonl)
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

func TestWriteRaceAllDupIdempotent(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, racedDups: 1}
	out, code := run(t, rs, []string{"knowledge"},
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

func TestWriteRaceRetryConflictErrors(t *testing.T) {
	f := &fakeStore{}
	rs := &raceDupStore{fakeStore: f, failRetry: true}
	out, code := run(t, rs, []string{"knowledge"},
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

func TestWriteProjectKindDedup(t *testing.T) {
	t.Run("pre-check mixed", func(t *testing.T) {
		f := &fakeStore{existsProject: map[string]bool{"데모 게임 프로젝트": true}}
		jsonl := `{"name":"데모 게임 프로젝트","status":"active","summary":"기존"}` + "\n" +
			`{"name":"새 프로젝트","status":"active","summary":"신규"}`
		out, code := run(t, f, []string{"project"}, jsonl)
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
		out, code := run(t, rs, []string{"project"},
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

type countCall struct {
	kind, source string
	n            int
}

type countRecorder struct {
	*fakeStore
	singles []countCall
	counts  []countCall
}

func (c *countRecorder) RecordUsage(ctx context.Context, kind, source string) error {
	c.singles = append(c.singles, countCall{kind, source, 1})
	return nil
}

func (c *countRecorder) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	c.counts = append(c.counts, countCall{kind, source, count})
	return nil
}

func TestWriteDupBatchSingleCountCall(t *testing.T) {
	t.Run("all-dup", func(t *testing.T) {
		c := &countRecorder{fakeStore: &fakeStore{exists: true}}
		out, code := run(t, c, []string{"knowledge"}, strings.Join([]string{
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
		if len(c.counts) != 2 || c.counts[0] != (countCall{"dup", "", 3}) ||
			c.counts[1] != (countCall{"dup_manual", "", 3}) {
			t.Fatalf("dup 계측=%v, want [dup \"\" 3, dup_manual \"\" 3] 각 1회 일괄", c.counts)
		}
	})
	t.Run("mixed", func(t *testing.T) {
		calls := 0
		c := &countRecorder{fakeStore: &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
			calls++
			return DupMeta{}, calls <= 2
		}}}
		jsonl := `{"topic":"rv-e","summary":"a","confidence":0.9}` + "\n" +
			`{"topic":"rv-f","summary":"b","confidence":0.9}` + "\n" +
			`{"topic":"rv-g","summary":"c","confidence":0.9}`
		out, code := run(t, c, []string{"knowledge"}, jsonl)
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
		if len(c.counts) != 2 || c.counts[0] != (countCall{"dup", "", 2}) ||
			c.counts[1] != (countCall{"dup_manual", "", 2}) {
			t.Fatalf("dup 계측=%v, want [dup \"\" 2, dup_manual \"\" 2] 각 1회 일괄(실제 skip 수)", c.counts)
		}
	})
}

func TestWriteAllDuplicateKeepsWarnings(t *testing.T) {
	f := &fakeStore{exists: true}
	out, code := run(t, f, []string{"knowledge"},
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

func dedupQueryCount(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "content_hash = ANY($1)") {
			n++
		}
	}
	return n
}

func TestWriteMultiLineDedupBatchQuery(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: calls}, calls == 2
	}}
	var lines []string
	for _, topic := range []string{"wb-a", "wb-b", "wb-c", "wb-d"} {
		lines = append(lines, fmt.Sprintf(`{"topic":%q,"summary":"합성 줄","confidence":0.9}`, topic))
	}
	out, code := run(t, f, []string{"knowledge"}, strings.Join(lines, "\n"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" || m["inserted"] != float64(3) || m["duplicates"] != float64(1) {
		t.Fatalf("출력=%v, want committed/inserted=3/duplicates=1", m)
	}
	if len(f.inserted) != 3 || f.inserted[0]["topic"] != "wb-a" ||
		f.inserted[1]["topic"] != "wb-c" || f.inserted[2]["topic"] != "wb-d" {
		t.Fatalf("기존 wb-b만 skip·순서 보존: %v", f.inserted)
	}
	if n := dedupQueryCount(f); n != 1 {
		t.Fatalf("배치 dedup 질의=%d회, want 1(줄 수 4와 무관)", n)
	}
	if calls != 4 {
		t.Fatalf("해시별 응답=%d회, want 4(전 줄 해시 조회)", calls)
	}
}

func TestWriteSingleLineDedupKeepsRowLookup(t *testing.T) {
	f := &fakeStore{exists: true}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"sl-a","summary":"단발 합성","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want 맨몸 duplicate(폴백 바이트 불변)", out)
	}
	if n := dedupQueryCount(f); n != 0 {
		t.Fatalf("배치 dedup 질의=%d회, want 0(단발은 줄별 조회)", n)
	}
}

func TestWriteMultiLineBatchLookupErrorExits(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "content_hash = ANY($1)") {
			return nil, fmt.Errorf("dial: %w", store.ErrServerDown)
		}
		return nil, nil
	}}
	jsonl := `{"topic":"be-a","summary":"a","confidence":0.9}` + "\n" +
		`{"topic":"be-b","summary":"b","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge"}, jsonl)
	if code != 2 {
		t.Fatalf("exit=%d, want 2(서버다운 — 래핑 통과 MapExit); out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("출력=%q, want error JSON", out)
	}
	if len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("오류 시 커밋 금지: 삽입 %d·커밋 %d", len(f.inserted), len(f.commits))
	}
}

func TestWriteMultiLineDedupChunked(t *testing.T) {
	saved := importDedupChunk
	importDedupChunk = 2
	t.Cleanup(func() { importDedupChunk = saved })
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{}, calls <= 2
	}}
	var lines []string
	for _, topic := range []string{"ck-a", "ck-b", "ck-c", "ck-d", "ck-e"} {
		lines = append(lines, fmt.Sprintf(`{"topic":%q,"summary":"청크 합성","confidence":0.9}`, topic))
	}
	out, code := run(t, f, []string{"knowledge"}, strings.Join(lines, "\n"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["inserted"] != float64(3) || m["duplicates"] != float64(2) {
		t.Fatalf("출력=%v, want inserted=3/duplicates=2", m)
	}
	if n := dedupQueryCount(f); n != 3 {
		t.Fatalf("배치 dedup 질의=%d회, want 3(5해시/청크2)", n)
	}
}

func BenchmarkWriteMultiLinePipeline(b *testing.B) {
	const lines = 2000
	var buf strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&buf, "{\"topic\":\"bench-%d\",\"summary\":\"합성 벤치 줄 %d\",\"tags\":[\"bench\"],\"confidence\":0.9}\n", i, i)
	}
	stdin := buf.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := &fakeStore{}
		var out strings.Builder
		if code := RunWrite(context.Background(), f, []string{"knowledge"}, strings.NewReader(stdin), &out); code != 0 {
			b.Fatalf("exit=%d out=%s", code, out.String())
		}
	}
}

func writeRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	return testengine.ResolveDSN(), true
}

type writeRealStore struct {
	*db.Repo
	pool *pgxpool.Pool
}

func (s *writeRealStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return nil, wire.ErrHistoryUnsupported
}

func (s *writeRealStore) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return nil, wire.ErrHistoryUnsupported
}

func (s *writeRealStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return nil, wire.ErrHistoryUnsupported
}

func (s *writeRealStore) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return nil, wire.ErrHistoryUnsupported
}

func (s *writeRealStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *writeRealStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func (s *writeRealStore) InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (from_id, to_id, relation) DO NOTHING`,
		fromID, toID, relation)
	return err
}

func (s *writeRealStore) Close() {
	s.Repo.Close()
	s.pool.Close()
}

func TestWriteBatchDedupRealSlow(t *testing.T) {
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	const tag = "wt3-합성-"
	const lines = 5000
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, tag)
	}
	t.Cleanup(cleanup)
	cleanup()
	var buf strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&buf, "{\"topic\":\"%s%d\",\"summary\":\"배치 dedup 벤치 합성 줄 %d\",\"confidence\":0.9}\n", tag, i, i)
	}
	stdin := buf.String()

	start := time.Now()
	var out strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(stdin), &out); code != 0 {
		t.Fatalf("1차 적립 exit=%d out=%s", code, out.String())
	}
	fresh := time.Since(start)
	if !strings.Contains(out.String(), fmt.Sprintf(`"inserted":%d`, lines)) {
		t.Fatalf("1차 적립 출력: %s", out.String())
	}

	start = time.Now()
	var out2 strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(stdin), &out2); code != 0 {
		t.Fatalf("2차 재전송 exit=%d out=%s", code, out2.String())
	}
	rerun := time.Since(start)
	if strings.TrimSpace(out2.String()) != `{"status":"duplicate"}` {
		t.Fatalf("2차 재전송 출력=%q, want duplicate(멱등)", out2.String())
	}

	t.Logf("실DB 배치 dedup: 1차(신규 %d줄)=%v·2차(전부 dup)=%v·50k 환산 1차≈%v·2차≈%v",
		lines, fresh.Round(time.Millisecond), rerun.Round(time.Millisecond),
		(fresh * 10).Round(time.Millisecond), (rerun * 10).Round(time.Millisecond))
}

func TestSanitizePayloadTagsSecrets(t *testing.T) {
	p := map[string]any{
		"topic":  "토픽",
		"tags":   []any{"ghp_abcdefghijklmnopqrst", "안전"},
		"domain": "api-key=sk-live-1234567890abcdef",
	}
	warns := SanitizePayload(p)
	joined := strings.Join(warns, ";")
	if !strings.Contains(joined, "possible secret") {
		t.Errorf("비밀 경고 없음: %v", warns)
	}

	p2 := map[string]any{"tags": []any{"a[flint]b"}}
	_ = SanitizePayload(p2)
	if raw, _ := p2["tags"].([]any); len(raw) > 0 {
		if cell, _ := raw[0].(string); strings.Contains(cell, "[flint]") {
			t.Errorf("tags 마커 미중립: %v", p2["tags"])
		}
	}
}

func writeMaxRowsFixture(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "{\"topic\":\"demo write maxrows %02d\",\"summary\":\"합성 데모 — write 상한 검증 줄 %02d\",\"confidence\":0.9}\n", i, i)
	}
	return b.String()
}

func TestRunWriteMaxRowsExceeded(t *testing.T) {
	f := maxRowsFake("2")
	out, code := run(t, f, []string{"knowledge"}, writeMaxRowsFixture(3))
	if code != 3 {
		t.Fatalf("exit=%d, want 3(상한 초과); out=%s", code, out)
	}
	if !strings.Contains(out, "write row limit exceeded") ||
		!strings.Contains(out, `"max":2`) || !strings.Contains(out, `"count":3`) {
		t.Fatalf("오류 JSON max·count 미포함: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

func TestRunWriteMaxRowsBoundary(t *testing.T) {
	f := maxRowsFake("2")
	out, code := run(t, f, []string{"knowledge"}, writeMaxRowsFixture(2))
	if code != 0 || !strings.Contains(out, `"inserted":2`) {
		t.Fatalf("경계(==max) exit=%d out=%s", code, out)
	}
}

func TestRunWriteFieldBytesExceeded(t *testing.T) {
	f := &fakeStore{}
	big := strings.Repeat("a", maxPayloadFieldBytes+1)
	stdin := fmt.Sprintf("{\"topic\":\"demo field limit\",\"summary\":\"%s\",\"confidence\":0.9}\n", big)
	out, code := run(t, f, []string{"knowledge"}, stdin)
	if code != 3 {
		t.Fatalf("exit=%d, want 3(필드 상한); out=%.200s", code, out)
	}
	if !strings.Contains(out, `"field":"summary"`) ||
		!strings.Contains(out, fmt.Sprint(maxPayloadFieldBytes)) {
		t.Fatalf("필드 상한 오류(필드명·상한 바이트) 미포함: %.200s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("필드 상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

func TestRunWriteFieldBytesBoundary(t *testing.T) {
	f := &fakeStore{}
	ok := strings.Repeat("b", maxPayloadFieldBytes)
	stdin := fmt.Sprintf("{\"topic\":\"%s\",\"summary\":\"%s\",\"confidence\":0.9}\n",
		"demo ok"+strings.Repeat("c", maxPayloadFieldBytes-7), ok)
	out, code := run(t, f, []string{"knowledge"}, stdin)
	if code != 0 {
		t.Fatalf("경계(==상한) exit=%d, want 0; out=%.200s", code, out)
	}
	if !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("경계 적립 출력 아님: %.200s", out)
	}
}

func openWriteRealStore(t *testing.T) (*writeRealStore, string) {
	t.Helper()
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	return st, dsn
}

func raceStdin(roundTag, writerTag string, shared, uniq int) string {
	var b strings.Builder
	for i := 0; i < shared; i++ {
		fmt.Fprintf(&b, "{\"topic\":\"%sshared-%03d\",\"summary\":\"합성 데모 — 레이스 공유 줄 %03d\",\"confidence\":0.9}\n", roundTag, i, i)
	}
	for i := 0; i < uniq; i++ {
		fmt.Fprintf(&b, "{\"topic\":\"%s%s-uniq-%03d\",\"summary\":\"합성 데모 — 레이스 고유 줄 %03d\",\"confidence\":0.9}\n", roundTag, writerTag, i, i)
	}
	return b.String()
}

func raceRowCount(ctx context.Context, st *writeRealStore, roundTag string) int {
	rows, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE topic LIKE $1 || '%'`, roundTag)
	if err != nil {
		return -1
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return -1
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return -1
	}
	return n
}

func TestWriteParallelRaceRealSlow(t *testing.T) {
	stA, dsn := openWriteRealStore(t)
	ctx := context.Background()
	repoB, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store B unavailable: %v", err)
	}
	t.Cleanup(repoB.Close)
	poolB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real pool B unavailable: %v", err)
	}
	t.Cleanup(poolB.Close)
	stB := &writeRealStore{Repo: repoB, pool: poolB}

	const tag = "r33e1-합성-레이스-"
	cleanup := func() {
		_, _ = stA.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, tag)
	}
	t.Cleanup(cleanup)
	cleanup()

	const rounds = 5
	for round := 1; round <= rounds; round++ {
		roundTag := fmt.Sprintf("%s%02d-", tag, round)
		stdinA := raceStdin(roundTag, "a", 50, 50)
		stdinB := raceStdin(roundTag, "b", 50, 50)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var outA, outB strings.Builder
		var codeA, codeB int
		begin := time.Now()
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			codeA = RunWrite(ctx, stA, []string{"knowledge"}, strings.NewReader(stdinA), &outA)
		}()
		go func() {
			defer wg.Done()
			<-start
			codeB = RunWrite(ctx, stB, []string{"knowledge"}, strings.NewReader(stdinB), &outB)
		}()
		close(start)
		wg.Wait()
		elapsed := time.Since(begin)
		if codeA != 0 || codeB != 0 {
			t.Fatalf("라운드 %d exit a=%d b=%d — 패자 고유줄 유실(outA=%.300s outB=%.300s)",
				round, codeA, codeB, outA.String(), outB.String())
		}
		if n := raceRowCount(ctx, stA, roundTag); n != 150 {
			t.Fatalf("라운드 %d 잔존 %d행, want 150(공유 50 + 고유 50×2)", round, n)
		}
		inserted := 0
		for _, o := range []string{outA.String(), outB.String()} {
			m := decode(t, o)
			inserted += int(m["inserted"].(float64))
		}
		t.Logf("라운드 %d: 경과=%v inserted 합=%d", round, elapsed.Round(time.Millisecond), inserted)
	}
}

func TestParseSourceCorrectionValid(t *testing.T) {
	src, err := ParseSourceFlag([]string{"--source", "correction"})
	if err != nil || src != "correction" {
		t.Fatalf("ParseSourceFlag=%q,%v — want correction,nil(등록표 통과)", src, err)
	}
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1110-t1-correction","summary":"합성 데모 교정 줄","confidence":0.9}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(--source correction 유효); out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("출력=%q, want committed", out)
	}
}

func TestParseSourceUnregisteredRejectedExit3(t *testing.T) {
	if _, err := ParseSourceFlag([]string{"--source", "nope"}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("미등록 값 오류=%v, want ErrInvalidSource", err)
	}
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "nope"},
		`{"topic":"v1110-t1-bad","summary":"합성 데모","confidence":0.9}`+"\n")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(미등록 source); out=%s", code, out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("미등록 source 적립 발생: %d건", len(f.inserted))
	}
}

func TestSourceRegistryDerivationGuard(t *testing.T) {
	want := strings.Join(sourceRegistry, "|")
	f := &fakeStore{}
	out, code := run(t, f, []string{"--help"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(--help)", code)
	}
	if !strings.Contains(out, "--source "+want) {
		t.Fatalf("usage 문자열이 등록표와 불일치: want %q 포함, got %q", want, out)
	}
	if !strings.Contains(ErrInvalidSource.Error(), "("+want+")") {
		t.Fatalf("ErrInvalidSource 메시지가 등록표와 불일치: want (%s), got %q", want, ErrInvalidSource)
	}
	if len(sourceRegistry) == 0 {
		t.Fatal("sourceRegistry 비어 있음")
	}
	for _, s := range sourceRegistry {
		if !validSources[s] {
			t.Fatalf("등록표 항목 %q가 validSources에 없음(등록표↔멤버십 불일치)", s)
		}
	}
	if len(validSources) != len(sourceRegistry) {
		t.Fatalf("validSources 크기=%d, want %d(등록표와 1:1)", len(validSources), len(sourceRegistry))
	}
}

func TestParseSourceGapValid(t *testing.T) {
	if !validSources["gap"] {
		t.Fatal("등록표에 gap 없음(validSources 멤버십 누락)")
	}
	src, err := ParseSourceFlag([]string{"--source", "gap"})
	if err != nil || src != "gap" {
		t.Fatalf("ParseSourceFlag=%q,%v — want gap,nil(등록표 통과)", src, err)
	}
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "gap"},
		`{"topic":"v1120-t1-gap","summary":"합성 데모 격차 줄","confidence":0.4}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(--source gap 유효); out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("출력=%q, want committed", out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if f.inserted[0]["tags"] != nil && f.inserted[0]["tags"] != "" {
		t.Fatalf("tags=%v, want 부착 없음(gap은 태그 기본값 없음 — T3가 부착)", f.inserted[0]["tags"])
	}
}

func TestParseSourceUnregisteredStillRejectedAfterGap(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "gaps"},
		`{"topic":"v1120-t1-bad","summary":"합성 데모","confidence":0.9}`+"\n")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(미등록 source); out=%s", code, out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("미등록 source 적립 발생: %d건", len(f.inserted))
	}
}

func TestWriteCorrectionDefaults(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-correction-defaults","summary":"합성 데모 교정 줄"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["state"] != "pending" {
		t.Fatalf("state=%v, want pending(기본 0.7 < conf_floor 0.8)", m["state"])
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if conf, _ := f.inserted[0]["confidence"].(float64); conf != 0.7 {
		t.Fatalf("confidence=%v, want 0.7(threshold 기본)", f.inserted[0]["confidence"])
	}
	if f.inserted[0]["status"] != "pending" {
		t.Fatalf("status=%v, want pending", f.inserted[0]["status"])
	}
	if f.inserted[0]["tags"] != ":correction:" {
		t.Fatalf("tags=%v, want :correction: 자동 부착", f.inserted[0]["tags"])
	}
}

func TestWriteCorrectionRespectsExplicitConfidence(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-correction-explicit","summary":"합성 데모","confidence":0.95}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["state"] != "pending" {
		t.Fatalf("state=%v, want pending(claim 없으면 conf 무관 강제)", m["state"])
	}
	if conf, _ := f.inserted[0]["confidence"].(float64); conf != 0.95 {
		t.Fatalf("confidence=%v, want 0.95(지정값 존중)", f.inserted[0]["confidence"])
	}
	if f.inserted[0]["status"] != "pending" {
		t.Fatalf("status=%v, want pending", f.inserted[0]["status"])
	}
}

func TestWriteCorrectionTagMerge(t *testing.T) {
	t.Run("기존 태그 병합", func(t *testing.T) {
		f := &fakeStore{}
		out, code := run(t, f, []string{"knowledge", "--source", "correction"},
			`{"topic":"t2-tag-merge","summary":"합성 데모","confidence":0.9,"tags":["demo","fix"]}`+"\n")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if f.inserted[0]["tags"] != ":correction:demo:fix:" {
			t.Fatalf("tags=%v, want :correction:demo:fix: 병합(정렬 저장 형식)", f.inserted[0]["tags"])
		}
	})
	t.Run("이미 있으면 중복 부착 없음", func(t *testing.T) {
		f := &fakeStore{}
		out, code := run(t, f, []string{"knowledge", "--source", "correction"},
			`{"topic":"t2-tag-idem","summary":"합성 데모","confidence":0.9,"tags":["correction"]}`+"\n")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if f.inserted[0]["tags"] != ":correction:" {
			t.Fatalf("tags=%v, want :correction:(멱등 — 중복 부착 없음)", f.inserted[0]["tags"])
		}
	})
}

type hashMatchStore struct {
	*fakeStore
	hash string
}

func (h *hashMatchStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return hash == h.hash, nil
}

func TestWriteCorrectionDedupCircuitReused(t *testing.T) {
	f1 := &fakeStore{}
	out, code := run(t, f1, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-dedup-reuse","summary":"합성 데모","confidence":0.9,"tags":["demo"]}`+"\n")
	if code != 0 {
		t.Fatalf("1차 exit=%d, want 0; out=%s", code, out)
	}
	hash, _ := f1.inserted[0]["content_hash"].(string)
	if hash == "" {
		t.Fatal("1차 적립 content_hash 없음")
	}

	f2 := &hashMatchStore{fakeStore: &fakeStore{}, hash: hash}
	out2, code2 := run(t, f2, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-dedup-reuse","summary":"합성 데모","confidence":0.9,"tags":["demo"]}`+"\n")
	if code2 != 0 {
		t.Fatalf("2차 exit=%d, want 0; out=%s", code2, out2)
	}
	if !strings.Contains(out2, `"status":"duplicate"`) {
		t.Fatalf("2차 출력=%q, want duplicate(회로 승계)", out2)
	}
	if len(f2.inserted) != 0 {
		t.Fatalf("2차 적립 발생: %d건", len(f2.inserted))
	}
}

func TestWriteCorrectionConfidenceThresholdOverride(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") &&
			strings.Contains(sql, "correction_confidence") {
			return [][]any{{"correction_confidence", "0.5"}}, nil
		}
		return nil, nil
	}}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-threshold-override","summary":"합성 데모"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if conf, _ := f.inserted[0]["confidence"].(float64); conf != 0.5 {
		t.Fatalf("confidence=%v, want 0.5(meta 오버라이드)", f.inserted[0]["confidence"])
	}
	if f.inserted[0]["status"] != "pending" {
		t.Fatalf("status=%v, want pending", f.inserted[0]["status"])
	}
}

func TestWriteManualMissingConfidenceStillRejected(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"t2-manual-noconf","summary":"합성 데모"}`+"\n")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(manual은 confidence 필수 불변); out=%s", code, out)
	}
	if !strings.Contains(out, `"confidence"`) {
		t.Fatalf("출력=%q, want confidence 오류", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("적립 발생: %d건", len(f.inserted))
	}
}

func correctionCapFake(maxOverride, todayCount string, bumps *int) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.HasPrefix(sql, "INSERT INTO meta") {
			if key, _ := args[0].(string); strings.HasPrefix(key, "daily:correction_write:") && bumps != nil {
				*bumps++
			}
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			if maxOverride != "" {
				return [][]any{{"correction_daily_max", maxOverride}}, nil
			}
			return nil, nil
		}
		if strings.Contains(sql, "SELECT value FROM meta WHERE key = $1") {
			if key, _ := args[0].(string); strings.HasPrefix(key, "daily:correction_write:") && todayCount != "" {
				return [][]any{{todayCount}}, nil
			}
		}
		return nil, nil
	}}
}

func TestWriteCorrectionDailyCapReachedSkips(t *testing.T) {
	var bumps int
	f := correctionCapFake("", "20", &bumps)
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t3-cap-reached","summary":"합성 데모"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(조용한 skip); out=%s", code, out)
	}
	if len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("상한 도달 적립 발생: 삽입 %d·커밋 %d", len(f.inserted), len(f.commits))
	}
	if !strings.Contains(out, "correction daily max") {
		t.Fatalf("경고 1행 없음: %q", out)
	}
	if bumps != 0 {
		t.Fatalf("skip 시 bump 발생: %d회", bumps)
	}
}

func TestWriteCorrectionDailyCapPartial(t *testing.T) {
	var bumps int
	f := correctionCapFake("", "19", &bumps)
	jsonl := `{"topic":"t3-cap-a","summary":"합성 데모","confidence":0.9}` + "\n" +
		`{"topic":"t3-cap-b","summary":"합성 데모","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge", "--source", "correction"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["inserted"] != float64(1) {
		t.Fatalf("inserted=%v, want 1(부분 적립)", m["inserted"])
	}
	if len(f.inserted) != 1 || f.inserted[0]["topic"] != "t3-cap-a" {
		t.Fatalf("적립=%v, want t3-cap-a만", f.inserted)
	}
	w, _ := m["warnings"].([]any)
	capWarns := 0
	for _, x := range w {
		if strings.Contains(fmt.Sprint(x), "correction daily max") {
			capWarns++
		}
	}
	if capWarns != 1 {
		t.Fatalf("상한 경고=%d행, want 1", capWarns)
	}
	if bumps != 1 {
		t.Fatalf("bump=%d회, want 1(적립 1건)", bumps)
	}
}

func TestWriteCorrectionDailyCapOverride(t *testing.T) {
	f := correctionCapFake("1", "", nil)
	jsonl := `{"topic":"t3-ovr-a","summary":"합성 데모","confidence":0.9}` + "\n" +
		`{"topic":"t3-ovr-b","summary":"합성 데모","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge", "--source", "correction"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["inserted"] != float64(1) {
		t.Fatalf("inserted=%v, want 1(오버라이드 1)", m["inserted"])
	}
	if len(f.inserted) != 1 || f.inserted[0]["topic"] != "t3-ovr-a" {
		t.Fatalf("적립=%v, want t3-ovr-a만", f.inserted)
	}
}

func TestWriteCorrectionDailyCapUnderPasses(t *testing.T) {
	var bumps int
	f := correctionCapFake("", "0", &bumps)
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t3-under","summary":"합성 데모"}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.Contains(out, "correction daily max") {
		t.Fatalf("미달 경고 발생: %q", out)
	}
	if !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("출력=%q, want committed", out)
	}
	if bumps != 1 {
		t.Fatalf("bump=%d회, want 1", bumps)
	}
}

func TestWriteCommitMessageNamePreferred(t *testing.T) {
	t.Run("project — name 우선", func(t *testing.T) {
		f := &fakeStore{}
		out, code := run(t, f, []string{"project"},
			`{"name":"demo-d4-프로젝트","status":"active"}`+"\n")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if len(f.commits) != 1 {
			t.Fatalf("커밋=%d회, want 1", len(f.commits))
		}
		if want := "project: demo-d4-프로젝트"; f.commits[0] != want {
			t.Fatalf("커밋 제목=%q, want %q", f.commits[0], want)
		}
	})
	t.Run("name 부재 — summary 폴백 불변", func(t *testing.T) {
		f := &fakeStore{}
		out, code := run(t, f, []string{"knowledge"},
			`{"topic":"demo-d4-토픽","summary":"합성 데모 요약","confidence":0.9}`+"\n")
		if code != 0 {
			t.Fatalf("exit=%d, want 0; out=%s", code, out)
		}
		if len(f.commits) != 1 {
			t.Fatalf("커밋=%d회, want 1", len(f.commits))
		}
		if want := "knowledge: 합성 데모 요약"; f.commits[0] != want {
			t.Fatalf("커밋 제목=%q, want %q", f.commits[0], want)
		}
	})
}

type gapAccrualStore struct {
	*fakeStore
	dupRows map[string]DupMeta
	updates []string
}

func (g *gapAccrualStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	m, ok := g.dupRows[hash]
	return m, ok, nil
}

func (g *gapAccrualStore) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	g.updates = append(g.updates, fmt.Sprintf("%d:%s", id, summary))
	return 1, nil
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = saved
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestGapCandidateAccrualAtThreshold(t *testing.T) {
	t.Run("임계 미달 — 미적립", func(t *testing.T) {
		f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{}}
		maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 2)
		if len(f.inserted) != 0 {
			t.Fatalf("삽입=%d건, want 0(임계 미달)", len(f.inserted))
		}
	})
	t.Run("임계 도달 — pending 적립", func(t *testing.T) {
		f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{}}
		maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
		if len(f.inserted) != 1 {
			t.Fatalf("삽입=%d건, want 1", len(f.inserted))
		}
		rec := f.inserted[0]
		if want := "격차 후보: 데모 쿼리"; rec["topic"] != want {
			t.Fatalf("topic=%v, want %q", rec["topic"], want)
		}
		if want := "회상 실패 3회 — 지식 격차 후보"; rec["summary"] != want {
			t.Fatalf("summary=%v, want %q", rec["summary"], want)
		}
		if conf, _ := rec["confidence"].(float64); conf != 0.4 {
			t.Fatalf("confidence=%v, want 0.4", rec["confidence"])
		}
		if rec["tags"] != ":gap-candidate:" {
			t.Fatalf("tags=%v, want :gap-candidate:", rec["tags"])
		}
		if rec["status"] != "pending" {
			t.Fatalf("status=%v, want pending", rec["status"])
		}
		if rec["kind"] != "knowledge" {
			t.Fatalf("kind=%v, want knowledge", rec["kind"])
		}
		if rec["content_hash"] != gapCandidateHash(NormalizeGapQuery("데모 쿼리")) {
			t.Fatalf("content_hash=%v, want gapCandidateHash(N-invariant)", rec["content_hash"])
		}
		if len(f.commits) != 1 {
			t.Fatalf("커밋=%d회, want 1(적립 커밋)", len(f.commits))
		}
	})
}

func TestGapCandidateThresholdMetaOverride(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT key, value FROM meta WHERE key IN (") {
			return [][]any{{"gap_query_threshold", "5"}}, nil
		}
		return nil, nil
	}}, dupRows: map[string]DupMeta{}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
	if len(f.inserted) != 0 {
		t.Fatalf("삽입=%d건, want 0(오버라이드 5 > 3)", len(f.inserted))
	}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 5)
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1(오버라이드 5 도달)", len(f.inserted))
	}
}

func TestGapCandidatePendingRefreshUpdatesN(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{
		gapCandidateHash(NormalizeGapQuery("데모 쿼리")): {ID: 7, Topic: "격차 후보: 데모 쿼리", State: "pending"},
	}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 5)
	if len(f.updates) != 1 || f.updates[0] != "7:회상 실패 5회 — 지식 격차 후보" {
		t.Fatalf("갱신=%v, want [7:회상 실패 5회 — 지식 격차 후보](pending N 갱신)", f.updates)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("삽입=%d건, want 0(갱신 경로 — 신규 아님)", len(f.inserted))
	}
}

func TestGapCandidateActiveNoop(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{
		gapCandidateHash(NormalizeGapQuery("데모 쿼리")): {ID: 9, Topic: "격차 후보: 데모 쿼리", State: "active"},
	}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
	if len(f.updates) != 0 || len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("active NOOP 위반: updates=%v inserted=%d commits=%d", f.updates, len(f.inserted), len(f.commits))
	}
}

func TestGapCandidateDailyCapSkipsQuietly(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := &gapAccrualStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT value FROM meta WHERE key = $1") && len(args) > 0 {
			if fmt.Sprint(args[0]) == "daily:gap_write:"+today {
				return [][]any{{"10"}}, nil
			}
		}
		return nil, nil
	}}, dupRows: map[string]DupMeta{}}
	var inserted int
	stderr := captureStderr(t, func() {
		maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
		inserted = len(f.inserted)
	})
	if inserted != 0 {
		t.Fatalf("삽입=%d건, want 0(상한 도달 skip)", inserted)
	}
	if !strings.Contains(stderr, "gap_daily_max") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("경고=%q, want 1행(gap_daily_max 언급)", stderr)
	}
}

func TestGapCandidateHashInvariantToN(t *testing.T) {
	h := gapCandidateHash(NormalizeGapQuery("데모 쿼리"))
	if h != gapCandidateHash(NormalizeGapQuery("데모 쿼리")) {
		t.Fatal("동일 쿼리 해시 불안정")
	}
	if h == gapCandidateHash(NormalizeGapQuery("다른 쿼리")) {
		t.Fatal("상이 쿼리 해시 충돌")
	}
}

const (
	echoWTestTopic   = "데모 프로젝트 운영 기록"
	echoWDupSummary  = "회의록 오늘 확정된 마일스톤은 3개다"
	echoWContraSum   = "회의록 다음 주는 전체 휴가 기간이다"
	echoWOtherTopic  = "개인 독서 목록 정리"
	echoWOtherSummar = "올해 읽은 책 다섯 권 감상"
)

func runWriteEcho(t *testing.T, st wire.Store, topic, summary string, conf float64) (map[string]any, int) {
	t.Helper()
	var out bytes.Buffer
	line := fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":%f}`+"\n", topic, summary, conf)
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(line), &out)
	if code != 0 {
		t.Fatalf("RunWrite exit=%d out=%s", code, out.String())
	}
	return decode(t, out.String()), code
}

func TestWriteEchoFieldOnNewKnowledge(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed", m["status"])
	}
	for _, k := range []string{"hash", "state", "id"} {
		if _, ok := m[k]; !ok {
			t.Errorf("기존 필드 %q 소실 (하위 호환 위반): %v", k, m)
		}
	}
	echo, ok := m["echo"].([]any)
	if !ok || len(echo) != 1 {
		t.Fatalf("echo=%v, want 1건 배열", m["echo"])
	}
	c, _ := echo[0].(map[string]any)
	if c["id"] != float64(7) || c["relation"] != "duplicates" {
		t.Errorf("echo 후보=%v, want id=7 duplicates", c)
	}
	if _, ok := c["score"]; !ok {
		t.Errorf("echo 후보에 score 필드 부재: %v", c)
	}
	if _, ok := c["similarity"]; ok {
		t.Errorf("echo 후보에 similarity 필드는 부재해야 함: %v", c)
	}
	if c["topic"] != echoWTestTopic {
		t.Errorf("echo topic=%v, want %q", c["topic"], echoWTestTopic)
	}
}

func TestWriteEchoOmittedOnExactDup(t *testing.T) {
	st := &fakeStore{exists: true, rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "duplicate" {
		t.Fatalf("status=%v, want duplicate", m["status"])
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("정확-hash 중복 응답에 echo 부재해야 함: %v", m)
	}
}

func TestWriteEchoOmittedOnScanFailure(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}, queryErr: errors.New("scan down")}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("스캔 실패에도 status=%v, want committed", m["status"])
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("스캔 실패 시 echo 부재해야 함: %v", m)
	}
}

func TestWriteEchoOmittedWhenNoCandidates(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(9), echoWOtherTopic, echoWOtherSummar}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if _, ok := m["echo"]; ok {
		t.Errorf("후보 0건에 echo 부재해야 함: %v", m)
	}
}

func TestWriteEchoMultiLineOmitted(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}
	var out bytes.Buffer
	lines := fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9}`+"\n", echoWTestTopic, echoWDupSummary) +
		fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9}`+"\n", echoWTestTopic+" 복", echoWDupSummary)
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(lines), &out)
	if code != 0 {
		t.Fatalf("RunWrite exit=%d out=%s", code, out.String())
	}
	m := decode(t, out.String())
	if _, ok := m["echo"]; ok {
		t.Errorf("다중 행 응답에 echo 부재해야 함: %v", m)
	}
}

func TestWriteEchoThresholdSeam(t *testing.T) {
	t.Cleanup(func() { SetEchoThresholds(5, 0.3, 0.4) })
	st := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWContraSum}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	echo, ok := m["echo"].([]any)
	if !ok || len(echo) != 1 || echo[0].(map[string]any)["relation"] != "contradicts" {
		t.Fatalf("기본 임계 echo=%v, want 1건 contradicts", m["echo"])
	}
	SetEchoThresholds(5, 0.99, 0.4)
	m, _ = runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if _, ok := m["echo"]; ok {
		t.Errorf("min_score 0.99에서 echo 부재해야 함: %v", m)
	}
}

func echoInsertCalls(f *fakeStore) []writeQueryCall {
	var out []writeQueryCall
	for _, c := range f.queryLog {
		if strings.Contains(c.SQL, "INSERT INTO echo_candidates") {
			out = append(out, c)
		}
	}
	return out
}

func TestWriteEchoCandsAccruedOnNewRowV1241(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed", m["status"])
	}
	calls := echoInsertCalls(st)
	if len(calls) != 1 {
		t.Fatalf("echo_candidates INSERT %d회, want 1(후보 수와 동일): %+v", len(calls), calls)
	}
	c := calls[0]
	if !strings.Contains(c.SQL, "INSERT INTO echo_candidates") {
		t.Fatalf("SQL=%q", c.SQL)
	}

	if len(c.Args) != 4 {
		t.Fatalf("인자 %d개, want 4(후보 1건×4열): %v", len(c.Args), c.Args)
	}
	if got, _ := c.Args[0].(int); got != 7 {
		t.Errorf("candidate_id=%v, want 7(근접 행)", c.Args[0])
	}
	if got, _ := c.Args[1].(int); got != 1 {
		t.Errorf("echo_id=%v, want 1(신규 행 id)", c.Args[1])
	}
	if rel, _ := c.Args[2].(string); rel != "duplicates" {
		t.Errorf("relation=%q, want duplicates", rel)
	}
	if score, _ := c.Args[3].(float64); math.Abs(score-1.0) > 1e-9 {
		t.Errorf("score=%v, want 1.0(동일 문면)", c.Args[3])
	}
}

type echoDupMetaStore struct {
	*fakeStore
}

func (d *echoDupMetaStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	return DupMeta{ID: 42, Topic: echoWTestTopic, State: "active"}, true, nil
}

func TestWriteEchoCandsAccruedOnDupResendV1241(t *testing.T) {
	st := &echoDupMetaStore{fakeStore: &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "duplicate" {
		t.Fatalf("status=%v, want duplicate", m["status"])
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("중복 응답에 echo 부재해야 함(적립은 응답 불변): %v", m)
	}
	if len(st.inserted) != 0 {
		t.Fatalf("중복 재전송에 적립 발생: %d건", len(st.inserted))
	}
	calls := echoInsertCalls(st.fakeStore)
	if len(calls) != 1 {
		t.Fatalf("echo_candidates INSERT %d회, want 1(기존 id 기준 적립): %+v", len(calls), calls)
	}
	c := calls[0]
	if len(c.Args) != 4 {
		t.Fatalf("인자 %d개, want 4: %v", len(c.Args), c.Args)
	}
	if got, _ := c.Args[0].(int); got != 7 {
		t.Errorf("candidate_id=%v, want 7", c.Args[0])
	}
	if got, _ := c.Args[1].(int); got != 42 {
		t.Errorf("echo_id=%v, want 42(DupMeta.ID — 기존 행 기준)", c.Args[1])
	}
	if rel, _ := c.Args[2].(string); rel != "duplicates" {
		t.Errorf("relation=%q, want duplicates", rel)
	}
}

func TestWriteEchoCandsZeroWhenNoEchoV1241(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(9), echoWOtherTopic, echoWOtherSummar}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed", m["status"])
	}
	if calls := echoInsertCalls(st); len(calls) != 0 {
		t.Fatalf("후보 0건 write에 INSERT %d회, want 0: %+v", len(calls), calls)
	}
}

type raceEchoStore struct {
	*raceDupStore
}

func (r *raceEchoStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	if r.insertCalls == 0 {
		return DupMeta{}, false, nil
	}
	return DupMeta{ID: 42, Topic: echoWTestTopic, State: "active"}, true, nil
}

func TestWriteRaceRecheckEchoAccruedV1242(t *testing.T) {
	f := &fakeStore{rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}}
	rs := &raceEchoStore{raceDupStore: &raceDupStore{fakeStore: f, racedDups: 1}}
	m, _ := runWriteEcho(t, rs, echoWTestTopic, echoWDupSummary, 0.9)
	if m["status"] != "duplicate" {
		t.Fatalf("출력=%v, want duplicate(재확인 전부 존재)", m)
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("중복 응답에 echo 부재해야 함(적립은 응답 불변): %v", m)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("레이스 재확인 전부 존재에 삽입 발생: %d건", len(f.inserted))
	}
	calls := echoInsertCalls(f)
	if len(calls) != 1 {
		t.Fatalf("echo_candidates INSERT %d회, want 1(dupEcho=echoCands 적립): %+v", len(calls), calls)
	}
	c := calls[0]
	if len(c.Args) != 4 {
		t.Fatalf("인자 %d개, want 4(후보 1건×4열): %v", len(c.Args), c.Args)
	}
	if got, _ := c.Args[0].(int); got != 7 {
		t.Errorf("candidate_id=%v, want 7(근접 행)", c.Args[0])
	}
	if got, _ := c.Args[1].(int); got != 42 {
		t.Errorf("echo_id=%v, want 42(동시 삽입 행 DupMeta.ID)", c.Args[1])
	}
	if rel, _ := c.Args[2].(string); rel != "duplicates" {
		t.Errorf("relation=%q, want duplicates", rel)
	}
}

func TestWriteEchoPersistFailureQuietV1242(t *testing.T) {
	st := &fakeStore{
		rows: [][]any{{int64(7), echoWTestTopic, echoWDupSummary}},
		queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "INSERT INTO echo_candidates") {
				return nil, errors.New("합성 echo 적립 실패")
			}
			return [][]any{{int64(7), echoWTestTopic, echoWDupSummary}}, nil
		},
	}
	m, code := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(§13 quiet)", code)
	}
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed", m["status"])
	}
	echo, ok := m["echo"].([]any)
	if !ok || len(echo) != 1 {
		t.Fatalf("echo 필드=%v, want 1건(응답 불변)", m["echo"])
	}
	if len(st.commits) != 1 {
		t.Fatalf("커밋=%d회, want 1(적립 실패가 커밋에 영향 없음)", len(st.commits))
	}
	if calls := echoInsertCalls(st); len(calls) != 1 {
		t.Fatalf("echo_candidates INSERT %d회, want 1(시도는 발화 — quiet는 결과만): %+v", len(calls), calls)
	}
}

func relPayload(topic, summary string, relations string) string {
	if relations == "" {
		return fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9}`, topic, summary)
	}
	return fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9,"relations":[%s]}`, topic, summary, relations)
}

func TestWriteRelationsVocabularyReject(t *testing.T) {
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		relPayload("합성 관계 어휘", "관계 어휘 거부 합성", `{"to":1,"relation":"bogus"}`))
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if !strings.Contains(out, "rejected relation") {
		t.Fatalf("출력에 rejected relation 클래스 부재: %s", out)
	}
	if !strings.Contains(out, "refines") {
		t.Fatalf("출력에 어휘 목록 부재: %s", out)
	}
}

func TestWriteRelationsShapeReject(t *testing.T) {
	cases := []string{
		`{"to":"one","relation":"refines"}`,
		`{"relation":"refines"}`,
		`{"to":1}`,
		`{"to":0,"relation":"refines"}`,
	}
	for i, rel := range cases {
		out, code := run(t, &fakeStore{}, []string{"knowledge"},
			relPayload(fmt.Sprintf("합성 형식 %d", i), "관계 형식 거부 합성", rel))
		if code != 3 {
			t.Fatalf("case %d exit=%d out=%s, want 3", i, code, out)
		}
	}

	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"합성 형식 비배열","summary":"관계 비배열 거부 합성","confidence":0.9,"relations":{"to":1}}`)
	if code != 3 {
		t.Fatalf("non-array exit=%d out=%s, want 3", code, out)
	}
}

func TestWriteRelationsTargetMissingFast(t *testing.T) {
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		relPayload("합성 대상 부재", "관계 대상 부재 거부 합성", `{"to":999,"relation":"refines"}`))
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if !strings.Contains(out, "999") {
		t.Fatalf("출력에 대상 id 부재: %s", out)
	}
}

func t5ScratchStore(t *testing.T) *writeRealStore {
	t.Helper()
	_, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	scratchDSN := testengine.Scratch(t)
	repo, err := db.NewRepo(ctx, scratchDSN)
	if err != nil {
		t.Skipf("repo unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, scratchDSN)
	if err != nil {
		repo.Close()
		t.Skipf("pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	return st
}

func t5LinkRows(t *testing.T, st *writeRealStore) [][3]int {
	t.Helper()
	rows, err := st.Query(context.Background(),
		`SELECT from_id, to_id, relation FROM knowledge_links ORDER BY from_id, to_id, relation`)
	if err != nil {
		t.Fatalf("links 조회 실패: %v", err)
	}
	out := [][3]int{}
	for _, r := range rows {
		f, _ := render.CellToInt(r[0])
		o, _ := render.CellToInt(r[1])
		rel, _ := r[2].(string)
		out = append(out, [3]int{f, o, len(rel)})
	}
	return out
}

func TestWriteLinksRealSlow(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()

	ids := map[string]int{}
	for _, top := range []string{"합성 링크 A", "합성 링크 B"} {
		out, code := run(t, st, []string{"knowledge"},
			relPayload(top, "링크 시나리오 합성 본문 "+top, ""))
		if code != 0 {
			t.Fatalf("적립 %s exit=%d out=%s", top, code, out)
		}
		m := decode(t, out)
		idf, _ := m["id"].(float64)
		ids[top] = int(idf)
	}
	if ids["합성 링크 A"] <= 0 || ids["합성 링크 B"] <= 0 {
		t.Fatalf("적립 id 파싱 실패: %v", ids)
	}
	a, b := ids["합성 링크 A"], ids["합성 링크 B"]

	rels := fmt.Sprintf(`{"to":%d,"relation":"refines"},{"to":%d,"relation":"supports"}`, a, b)
	out, code := run(t, st, []string{"knowledge"}, relPayload("합성 링크 C", "링크 시나리오 합성 본문 C", rels))
	if code != 0 {
		t.Fatalf("링크 적립 exit=%d out=%s", code, out)
	}
	rows, err := st.Query(ctx,
		`SELECT to_id, relation FROM knowledge_links WHERE from_id = (SELECT id FROM knowledge WHERE topic = '합성 링크 C') ORDER BY to_id`)
	if err != nil {
		t.Fatalf("links 조회 실패: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("적립 링크 수=%d, want 2 (rows=%v)", len(rows), rows)
	}
	gotRel := map[int]string{}
	for _, r := range rows {
		to, _ := render.CellToInt(r[0])
		rel, _ := r[1].(string)
		gotRel[to] = rel
	}
	if gotRel[a] != "refines" || gotRel[b] != "supports" {
		t.Fatalf("링크 관계 오류: %v", gotRel)
	}
	nLinks := len(t5LinkRows(t, st))

	out2, code2 := run(t, st, []string{"knowledge"}, relPayload("합성 링크 C", "링크 시나리오 합성 본문 C", rels))
	if code2 != 0 || !strings.Contains(out2, `"duplicate"`) {
		t.Fatalf("재전송 exit=%d out=%s, want duplicate", code2, out2)
	}
	if got := len(t5LinkRows(t, st)); got != nLinks {
		t.Fatalf("재전송 후 링크 수=%d, want %d (멱등)", got, nLinks)
	}

	if out3, c3 := run(t, st, []string{"knowledge"}, relPayload("합성 링크 D", "링크 시나리오 합성 본문 D", "")); c3 != 0 {
		t.Fatalf("D 적립 exit=%d out=%s", c3, out3)
	}
	relsD := fmt.Sprintf(`{"to":%d,"relation":"duplicates"}`, a)
	out4, c4 := run(t, st, []string{"knowledge"}, relPayload("합성 링크 D", "링크 시나리오 합성 본문 D", relsD))
	if c4 != 0 || !strings.Contains(out4, `"duplicate"`) {
		t.Fatalf("D 재전송 exit=%d out=%s, want duplicate", c4, out4)
	}
	if !strings.Contains(out4, `"relations"`) {
		t.Fatalf("duplicate 응답에 relations 반영 부재: %s", out4)
	}
	dRows, err := st.Query(ctx,
		`SELECT l.relation FROM knowledge_links l JOIN knowledge k ON k.id = l.from_id WHERE k.topic = '합성 링크 D'`)
	if err != nil {
		t.Fatalf("D 링크 조회 실패: %v", err)
	}
	if len(dRows) != 1 {
		t.Fatalf("D duplicate 경로 링크 수=%d, want 1", len(dRows))
	}
	if rel, _ := dRows[0][0].(string); rel != "duplicates" {
		t.Fatalf("D 링크 관계=%q, want duplicates", rel)
	}

	out5, c5 := run(t, st, []string{"knowledge"},
		relPayload("합성 링크 E", "링크 시나리오 합성 본문 E", `{"to":99999999,"relation":"refines"}`))
	if c5 != 3 {
		t.Fatalf("대상 부재 exit=%d out=%s, want 3", c5, out5)
	}
	if n, _ := st.Query(ctx, `SELECT id FROM knowledge WHERE topic = '합성 링크 E'`); len(n) != 0 {
		t.Fatalf("대상 부재 거부 시 본문 적립되면 안 됨")
	}
}

func condPayload(topic, conditions string) string {
	return fmt.Sprintf(`{"topic":%q,"summary":"조건 태그 시나리오 합성 본문","confidence":0.9,"conditions":%q}`, topic, conditions)
}

func TestWriteConditionsStoredRoundtrip(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"}, condPayload("합성 조건 A", "호환 버전 2 미만에서만"))
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed", m["status"])
	}
	if len(f.inserted) != 1 || f.inserted[0]["conditions"] != "호환 버전 2 미만에서만" {
		t.Fatalf("conditions 열 저장 부재: %v", f.inserted)
	}
	if f.inserted[0]["topic"] != "합성 조건 A" {
		t.Fatalf("본문 필드 유실: %v", f.inserted[0])
	}
}

func TestWriteConditionsEmptyStringPasses(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"}, condPayload("합성 조건 빈값", ""))
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["conditions"] != "" {
		t.Fatalf("빈 문자열 conditions 거부 또는 미저장: %v", f.inserted)
	}
}

func TestWriteConditionsAbsentOmitted(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"합성 조건 없음","summary":"조건 태그 시나리오 합성 본문","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d, want 1", len(f.inserted))
	}
	if _, ok := f.inserted[0]["conditions"]; ok {
		t.Fatalf("미지정 conditions 주입 금지: %v", f.inserted[0])
	}
}

func TestWriteConditionsLengthCap(t *testing.T) {
	long := strings.Repeat("조", 501)
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"}, condPayload("합성 조건 초과", long))
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if !strings.Contains(out, "conditions") {
		t.Fatalf("거부 메시지에 필드명 부재: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("거부 시 적립 금지: %v", f.inserted)
	}

	bound := strings.Repeat("조", 500)
	if out, code := run(t, &fakeStore{}, []string{"knowledge"}, condPayload("합성 조건 경계", bound)); code != 0 {
		t.Fatalf("500루네 경계 exit=%d out=%s, want 0", code, out)
	}
}

func TestWriteConditionsNonStringReject(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"합성 조건 비문자열","summary":"조건 태그 시나리오 합성 본문","confidence":0.9,"conditions":123}`)
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("거부 시 적립 금지: %v", f.inserted)
	}
}

func TestWriteConditionsHashInvariantDuplicate(t *testing.T) {
	f := &fakeStore{}
	base := `{"topic":"합성 조건 B","summary":"조건 태그 시나리오 합성 본문","confidence":0.9}`
	out, code := run(t, f, []string{"knowledge"}, base)
	if code != 0 {
		t.Fatalf("최초 적립 exit=%d out=%s", code, out)
	}
	firstHash, _ := decode(t, out)["hash"].(string)
	if firstHash == "" {
		t.Fatalf("최초 해시 파싱 실패: %s", out)
	}

	out2, code2 := run(t, f, []string{"knowledge"}, condPayload("합성 조건 B", "재검토 조건 합성"))
	if code2 != 0 {
		t.Fatalf("conditions 재전송 exit=%d out=%s, want 0", code2, out2)
	}
	if len(f.inserted) != 2 {
		t.Fatalf("삽입=%d, want 2 (fake 미존재 시 커밋)", len(f.inserted))
	}
	if h, _ := f.inserted[1]["content_hash"].(string); h != firstHash {
		t.Fatalf("±conditions 해시 불일치: %q vs %q — hashVolatile 계약 위반", h, firstHash)
	}

	f.exists = true
	out3, code3 := run(t, f, []string{"knowledge"}, condPayload("합성 조건 B", "재검토 조건 합성"))
	if code3 != 0 || !strings.Contains(out3, `"duplicate"`) {
		t.Fatalf("동일 재전송 exit=%d out=%s, want duplicate", code3, out3)
	}
	if len(f.inserted) != 2 || len(f.commits) != 2 {
		t.Fatalf("재전송 후 삽입=%d·커밋=%d, want 2·2 (0행 커밋)", len(f.inserted), len(f.commits))
	}
}

func TestWriteConditionsRealSlow(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()

	out, code := run(t, st, []string{"knowledge"}, condPayload("합성 조건 실DB A", "프로토콜 v9 배포 전까지"))
	if code != 0 {
		t.Fatalf("적립 exit=%d out=%s", code, out)
	}
	rows, err := st.Query(ctx,
		`SELECT conditions FROM knowledge WHERE topic = '합성 조건 실DB A'`)
	if err != nil {
		t.Fatalf("conditions 조회 실패: %v", err)
	}
	if len(rows) != 1 || rows[0][0] != "프로토콜 v9 배포 전까지" {
		t.Fatalf("conditions 왕복 불일치: %v", rows)
	}

	out2, code2 := run(t, st, []string{"knowledge"}, condPayload("합성 조건 실DB A", "다른 조건값 합성"))
	if code2 != 0 || !strings.Contains(out2, `"duplicate"`) {
		t.Fatalf("conditions 재전송 exit=%d out=%s, want duplicate", code2, out2)
	}
	rows2, _ := st.Query(ctx,
		`SELECT conditions FROM knowledge WHERE topic = '합성 조건 실DB A'`)
	if len(rows2) != 1 || rows2[0][0] != "프로토콜 v9 배포 전까지" {
		t.Fatalf("duplicate 재전송이 행을 바꾸면 안 됨: %v", rows2)
	}

	out3, c3 := run(t, st, []string{"knowledge"}, condPayload("합성 조건 실DB B", "조건 명시 합성"))
	if c3 != 0 {
		t.Fatalf("B 적립 exit=%d out=%s", c3, out3)
	}
	out4, c4 := run(t, st, []string{"knowledge"},
		`{"topic":"합성 조건 실DB B","summary":"조건 태그 시나리오 합성 본문","confidence":0.9}`)
	if c4 != 0 || !strings.Contains(out4, `"duplicate"`) {
		t.Fatalf("B 무조건 재전송 exit=%d out=%s, want duplicate", c4, out4)
	}

	long := strings.Repeat("조", 501)
	out5, c5 := run(t, st, []string{"knowledge"}, condPayload("합성 조건 실DB 초과", long))
	if c5 != 3 {
		t.Fatalf("길이 초과 exit=%d out=%s, want 3", c5, out5)
	}
	if n, _ := st.Query(ctx, `SELECT id FROM knowledge WHERE topic = '합성 조건 실DB 초과'`); len(n) != 0 {
		t.Fatalf("거부 시 적립되면 안 됨")
	}
}

func v124Body(topic string) string {
	return `{"topic":"` + topic + `","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.8}`
}

func stampCalls(f *fakeStore) []writeQueryCall {
	var out []writeQueryCall
	for _, q := range f.queryLog {
		if strings.Contains(q.SQL, "UPDATE knowledge SET kind") {
			out = append(out, q)
		}
	}
	return out
}

func TestWriteKnowledgeKindSourcePathRoundtrip(t *testing.T) {
	f := &fakeStore{}
	payload := `{"topic":"v124-t2-왕복-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.8,` +
		`"knowledge_kind":"Preference","source_path":"demo/notes/v124-t2-a.md"}`
	out, code := run(t, f, []string{"knowledge"}, payload)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립 행=%d, want 1", len(f.inserted))
	}
	rec := f.inserted[0]
	if rec["kind"] != "knowledge" {
		t.Fatalf("판별자 rec[kind]=%v, want knowledge(불변)", rec["kind"])
	}
	if sp, _ := rec["source_path"].(string); sp != "demo/notes/v124-t2-a.md" {
		t.Fatalf("source_path 열 왕복=%q", sp)
	}
	if _, ok := rec["knowledge_kind"]; ok {
		t.Fatalf("knowledge_kind이 레코드에 잔류 — 소비 키 규칙 위반: %v", rec)
	}

	stamps := stampCalls(f)
	if len(stamps) != 1 {
		t.Fatalf("kind 스탬프 질의=%d회, want 1: %+v", len(stamps), f.queryLog)
	}
	if len(stamps[0].Args) < 2 || stamps[0].Args[0] != "preference" {
		t.Fatalf("스탬프 인자=%v, want [preference <ids>]", stamps[0].Args)
	}

	g := &fakeStore{}
	if out, code = run(t, g, []string{"knowledge"}, v124Body("v124-t2-왕복-데모")); code != 0 {
		t.Fatalf("무필드 재전송 exit=%d out=%s", code, out)
	}
	if g.inserted[0]["content_hash"] != rec["content_hash"] {
		t.Fatalf("content_hash 불변 위반: %v vs %v",
			rec["content_hash"], g.inserted[0]["content_hash"])
	}
}

func TestWriteKnowledgeKindVocabularyGuard(t *testing.T) {
	for name, val := range map[string]string{
		"미등록 값":     `"knowledge_kind":"hypothesis"`,
		"비문자열":      `"knowledge_kind":3`,
		"source 초과": `"source_path":"` + strings.Repeat("a", 1025) + `"`,
	} {
		f := &fakeStore{}
		payload := `{"topic":"v124-t2-거부-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.9,` + val + `}`
		out, code := run(t, f, []string{"knowledge"}, payload)
		if code != 3 {
			t.Fatalf("%s: exit=%d out=%s, want 3", name, code, out)
		}
		if len(f.inserted) != 0 || len(stampCalls(f)) != 0 {
			t.Fatalf("%s: 거부 전 적립/스탬프 잔류", name)
		}
	}

	for name, extra := range map[string]string{
		"부재":   ``,
		"null": `,"knowledge_kind":null`,
		"공백":   `,"knowledge_kind":"  "`,
	} {
		f := &fakeStore{}
		payload := `{"topic":"v124-t2-미지정-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.9` + extra + `}`
		out, code := run(t, f, []string{"knowledge"}, payload)
		if code != 0 {
			t.Fatalf("%s: exit=%d out=%s, want 0", name, code, out)
		}
		if n := len(stampCalls(f)); n != 0 {
			t.Fatalf("%s: 미지정(NUll)에 스탬프 %d회", name, n)
		}
		if _, ok := f.inserted[0]["knowledge_kind"]; ok {
			t.Fatalf("%s: 미지정이 레코드 키로 잔류", name)
		}
	}
}

func TestWriteKnowledgeKindDuplicateNoStamp(t *testing.T) {
	f := &fakeStore{exists: true}
	payload := `{"topic":"v124-t2-멱등-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.9,` +
		`"knowledge_kind":"fact","source_path":"demo/notes/v124-t2-b.md"}`
	out, code := run(t, f, []string{"knowledge"}, payload)
	if code != 0 || !strings.Contains(out, `"duplicate"`) {
		t.Fatalf("재전송 exit=%d out=%s, want 0+duplicate", code, out)
	}
	if len(f.inserted) != 0 || len(stampCalls(f)) != 0 {
		t.Fatalf("멱등 위반: 적립=%d 스탬프=%d", len(f.inserted), len(stampCalls(f)))
	}
}

func TestWriteSourcePathSanitized(t *testing.T) {
	f := &fakeStore{}
	payload := `{"topic":"v124-t2-살균-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.8,` +
		`"source_path":"demo/no` + `\u0000` + `tes[flint]/a.md"}`
	out, code := run(t, f, []string{"knowledge"}, payload)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	sp, _ := f.inserted[0]["source_path"].(string)
	if strings.ContainsAny(sp, "\x00") || strings.Contains(sp, "[flint]") {
		t.Fatalf("source_path 살균 실패: %q", sp)
	}
}

func TestWriteCorrectionKindCounters(t *testing.T) {
	line := func(topic, kind string) string {
		if kind == "" {
			return v124Body(topic)
		}
		return `{"topic":"` + topic + `","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.4,` +
			`"knowledge_kind":"` + kind + `"}`
	}
	correction := func(f *fakeStore, lines string) (string, int) {
		return run(t, f, []string{"knowledge", "--source", "correction"}, lines)
	}
	counter := func(f *fakeStore, key string) (int, bool) {
		day := time.Now().UTC().Format("2006-01-02")
		full := "daily:" + key + ":" + day
		for _, q := range f.queryLog {
			if len(q.Args) >= 2 {
				if k, ok := q.Args[0].(string); ok && k == full {
					n := 0
					if s, ok := q.Args[1].(string); ok {
						n, _ = strconv.Atoi(s)
					}
					return n, true
				}
			}
		}
		return 0, false
	}

	f := &fakeStore{}
	body := strings.Join([]string{
		line("v124-d2-데모-p1", "preference"),
		line("v124-d2-데모-p2", "preference"),
		line("v124-d2-데모-f1", "fact"),
		line("v124-d2-데모-x1", ""),
	}, "\n")
	out, code := correction(f, body)
	if code != 0 {
		t.Fatalf("correction exit=%d out=%s", code, out)
	}
	if n, ok := counter(f, "correction_preference"); !ok || n != 2 {
		t.Fatalf("correction_preference=%d(ok=%v), want 2", n, ok)
	}
	if n, ok := counter(f, "correction_fact"); !ok || n != 1 {
		t.Fatalf("correction_fact=%d(ok=%v), want 1", n, ok)
	}

	g := &fakeStore{}
	if out, code = run(t, g, []string{"knowledge"}, line("v124-d2-데모-m1", "preference")); code != 0 {
		t.Fatalf("manual exit=%d out=%s", code, out)
	}
	if _, ok := counter(g, "correction_preference"); ok {
		t.Fatalf("manual 채널에 correction_preference 적립 — 분기 조건 위반")
	}

	h := &fakeStore{}
	if out, code = correction(h, line("v124-d2-데모-c1", "")); code != 0 {
		t.Fatalf("무kind correction exit=%d out=%s", code, out)
	}
	if _, ok := counter(h, "correction_preference"); ok {
		t.Fatalf("무kind correction에 적립 — kind값별 분기 위반")
	}
	if _, ok := counter(h, "correction_fact"); ok {
		t.Fatalf("무kind correction에 fact 적립 — kind값별 분기 위반")
	}
}

func TestWriteKindSourcePathRealSlow(t *testing.T) {
	_, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	scratchDSN := testengine.Scratch(t)
	repo, err := db.NewRepo(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	pool, err := db.Connect(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}

	const path = "demo/notes/v124-t2-real.md"
	body := `{"topic":"v124-t2-실DB-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.8,` +
		`"knowledge_kind":"preference","source_path":"` + path + `"}`
	out, code := run(t, st, []string{"knowledge"}, body)
	if code != 0 {
		t.Fatalf("적립 exit=%d out=%s", code, out)
	}
	rows, err := st.Query(ctx,
		`SELECT kind, source_path FROM knowledge WHERE topic = 'v124-t2-실DB-데모'`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("행 조회: %v %v", rows, err)
	}
	if k := fmt.Sprint(rows[0][0]); k != "preference" {
		t.Fatalf("kind 열 왕복=%q, want preference", k)
	}
	if sp := fmt.Sprint(rows[0][1]); sp != path {
		t.Fatalf("source_path 열 왕복=%q, want %q", sp, path)
	}

	plain := `{"topic":"v124-t2-실DB-데모","summary":"v124 T2 합성 데모 본문","tags":["demo"],"confidence":0.9}`
	out, code = run(t, st, []string{"knowledge"}, plain)
	if code != 0 || !strings.Contains(out, `"duplicate"`) {
		t.Fatalf("무필드 재전송 exit=%d out=%s, want 0+duplicate", code, out)
	}
	rows, err = st.Query(ctx,
		`SELECT kind, source_path FROM knowledge WHERE topic = 'v124-t2-실DB-데모'`)
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0][0]) != "preference" {
		t.Fatalf("duplicate 재전송이 행을 바꾸면 안 됨: %v %v", rows, err)
	}

	corr := `{"topic":"v124-t2-실DB-교정","summary":"v124 T2 합성 교정 본문","tags":["demo"],"confidence":0.4,` +
		`"knowledge_kind":"fact"}`
	out, code = run(t, st, []string{"knowledge", "--source", "correction"}, corr)
	if code != 0 {
		t.Fatalf("correction exit=%d out=%s", code, out)
	}
	rows, err = st.Query(ctx,
		`SELECT kind FROM knowledge WHERE topic = 'v124-t2-실DB-교정'`)
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0][0]) != "fact" {
		t.Fatalf("correction kind 왕복: %v %v", rows, err)
	}
	day := time.Now().UTC().Format("2006-01-02")
	meta, err := st.Query(ctx,
		`SELECT value FROM meta WHERE key = $1`, "daily:correction_fact:"+day)
	if err != nil || len(meta) != 1 || fmt.Sprint(meta[0][0]) != "1" {
		t.Fatalf("daily:correction_fact 계수: %v %v (want 1)", meta, err)
	}
}
