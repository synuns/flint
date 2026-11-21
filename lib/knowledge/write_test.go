// write_test.go — the write five-stage pipeline contract — dedup·race
// recheck·warning merging. Contract invariants: duplicate lines are
// skipped (new lines partially committed); an all-duplicate rerun is a
// zero-row commit·exit 0 "duplicate" (seeding idempotence); output of 2+
// lines is aggregate (inserted·duplicates·commit hash); single-line
// output stays as before. A race (InsertBatch ErrDuplicate) removes new
// -line loss via recheck skip + one retry of the remainder. dispatch
// (mustRun)-mediated tests belong to the cli residual (write_v06 etc.)
// — this file calls RunWrite directly.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeStore — the wire.Store interface fake (moved from the cli_test.go
// convention). Stub fields (exists·*Err) take precedence when set; with
// rows·queryFn unset it returns a no-op.
type fakeStore struct {
	wire.HistoryStubs

	// stub conditions
	exists        bool
	existsErr     error
	existsProject map[string]bool // per-name existence (nil means all false)
	insertErr     error
	commitErr     error
	pingErr       error
	queryErr      error
	rows          [][]any
	queryFn       func(sql string, args []any) ([][]any, error)
	// hashExistsFn — the per-hash response of the content_hash = ANY($1)
	// batch dedup lookup (unset: the exists flag answers uniformly for
	// all hashes).
	hashExistsFn func(hash string) (DupMeta, bool)

	// recording
	inserted    []map[string]any
	commits     []string
	queries     []string
	linkInserts [][3]any
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
	return f.existsProject[name], nil // a nil map lookup is false (no-op)
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
// InsertKnowledgeLink — the LinkAccrualStore extension (v1.20.0 T5):
// the fakeStore side records the idempotent link inserts.
func (f *fakeStore) InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error {
	f.linkInserts = append(f.linkInserts, [3]any{fromID, toID, relation})
	return nil
}

func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	// The batch dedup lookup (content_hash = ANY($1)) response — builds
	// existing-row meta rows from the per-hash hashExistsFn (unset: the
	// exists flag).
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

// run — runs RunWrite directly + returns the output (args exclude the
// dispatch token "write"). The same contract as the cli mustRun-mediated
// tests; only the store injection is explicit.
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

// TestWriteJSONLMixedCommitsNew — new+duplicate mixed: duplicate lines
// are skipped, only new lines commit (partial commit). exit 0·inserted=1·
// duplicates=1.
func TestWriteJSONLMixedCommitsNew(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: 11}, calls == 1 // only the first line (rv-b) is an existing record
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

// TestWriteJSONLAllDuplicateIdempotent — an all-duplicate rerun:
// zero-row commit·exit 0·one {"status":"duplicate"} line (the seeding
// idempotence contract).
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

// TestWriteJSONLMultiOutputNoFieldMix — 2 new lines (mixed confidence
// yielding pending+active): the multi-line output has no id·state,
// inserted=2, and hash is the one actual commit (the commit hash).
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
	if f.inserted[0]["status"] != "pending" || f.inserted[1]["status"] != "active" {
		t.Fatalf("줄별 판정(pending/active) 유실: %v", f.inserted)
	}
}

// raceDupStore — the race-reproducing fake: the pre-ExistsHash passes
// everything (false), only the first InsertBatch call returns
// store.ErrDuplicate (a transaction rollback from a concurrent insert).
// Subsequent ExistsHash answers true only for the first racedDups lines
// (the recheck point). With failRetry, the retry batch also keeps
// failing ErrDuplicate (an actual collision).
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
		return false, nil // pre-check: everything passes as new
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil // recheck: only the first racedDups lines were concurrently inserted
}

// TestWriteRaceRecoversNewLines — a race (pre-check passes, then only
// some lines are concurrently inserted in the batch): the recheck skips
// existing lines and the retry commits the new lines (regression: a full
// rollback + exit 0 "duplicate" lost the new lines). inserted=1·
// duplicates=1.
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

// TestWriteRaceAllDupIdempotent — after the race, the recheck finds
// everything existing: zero-row commit·exit 0 "duplicate" with no
// new-line loss (the seeding idempotence contract kept).
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

// TestWriteRaceRetryConflictErrors — if even the retry of a line that
// passed the recheck yields ErrDuplicate, it is an actual collision: an
// error report, not exit 0 "duplicate" (regression: an exit 0 success
// disguise on a lossy path).
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

// raceProjectStore — the project-race-reproducing fake: the pre-
// ExistsProject passes everything (false), only the first InsertBatch
// call returns store.ErrDuplicate (a concurrent-insert rollback), and
// subsequent ExistsProject answers true (the recheck point — the
// natural-key name check).
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

// TestWriteProjectKindDedup — per-line dedup on the project kind's
// natural key (name): lines with an existing name are skipped, only new
// lines commit·duplicates=1·exit 0 (regression: project has no
// content_hash, so neither pre-dedup nor recheck is possible →
// InsertBatch unique(name) collision → the retry re-collides with a
// "batch conflict" error, failing the first seeding).
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

// countCall — a recorded RecordUsageCount call (kind, source, count).
type countCall struct {
	kind, source string
	n            int
}

// countRecorder — a fake recording RecordUsage (single)·
// RecordUsageCount (bulk) calls: the realStore metering seam (#5: dup N
// incremented in bulk with one call). Dedup scenarios are specified via
// fakeStore.hashExistsFn (the batch ANY($1) lookup response).
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

// TestWriteDupBatchSingleCountCall — dup metering is one RecordUsageCount
// call per key regardless of line count (regression: recordDups looped
// RecordUsage N times — O(N) read-writes). write ×1 (RecordUsage)·total
// dup N + source dup_<source> N (one RecordUsageCount each).
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
			return DupMeta{}, calls <= 2 // only the first 2 lines are existing records
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

// TestWriteAllDuplicateKeepsWarnings — even an all-duplicate run does
// not discard the warnings (secret scan) (regression: the recs==0 branch
// printed only the duplicate line without warnings).
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

// ── write batch dedup — one lookupHashBatch (content_hash = ANY($1)
// chunks) lookup instead of per-line lookupHash (N round trips). The
// single-shot (1-row hook pattern) keeps the per-line lookup contract. ──

// dedupQueryCount — the number of batch dedup (ANY($1)) queries among
// the recorded queries.
func dedupQueryCount(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "content_hash = ANY($1)") {
			n++
		}
	}
	return n
}

// TestWriteMultiLineDedupBatchQuery — the multi-line pre-dedup is one
// ANY($1) batch query regardless of line count (regression: per-line
// lookups N round trips — RTT-dominated at 50k rows). Detection
// identical: existing lines skipped·new lines partially committed·the
// duplicates count unchanged.
func TestWriteMultiLineDedupBatchQuery(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: calls}, calls == 2 // only the second line is an existing row
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

// TestWriteSingleLineDedupKeepsRowLookup — the single-shot (1-row hook
// pattern) does not switch to the batch query: 0 ANY($1) queries·the
// per-line ExistsHash lookup as before (the DupMetaStore→DupIDStore→
// ExistsHash fallback contract unchanged).
func TestWriteSingleLineDedupKeepsRowLookup(t *testing.T) {
	f := &fakeStore{exists: true} // the ExistsHash fallback path
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

// TestWriteMultiLineBatchLookupErrorExits — a batch dedup lookup error
// propagates without a silent new verdict (the error contract:
// server-down exit 2 — preserved even through wrapping).
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

// TestWriteMultiLineDedupChunked — the batch query also fires once per
// chunk across chunk boundaries (lookupHashBatch reusing importDedupChunk
// — boundary-shrink verification).
func TestWriteMultiLineDedupChunked(t *testing.T) {
	saved := importDedupChunk
	importDedupChunk = 2
	t.Cleanup(func() { importDedupChunk = saved })
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{}, calls <= 2 // only the first 2 lines are existing rows
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

// BenchmarkWriteMultiLinePipeline — the per-line CPU cost of the
// synthetic multi-line write pipeline (validate→sanitize→hash→batch
// dedup→accrue) (a fake store — RTT excluded).
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

// writeRealDSN — real-DB connection allowed only with SUPERLITE_REAL=1 +
// FLINT_TEST_DSN set (inheriting the cli seamRealDSN·importRealDSN
// contract — operational fallback blocked).
func writeRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

// writeRealStore — the db.Repo write + pool read adapter (the
// importRealStore convention).
type writeRealStore struct {
	*db.Repo
	pool *pgxpool.Pool
}

// Of the history contract, the 4 methods db.Repo does not implement
// return unsupported (LogEntries is inherited via the *db.Repo embedding
// — an explicit absorption with the same meaning as wire.HistoryStubs).
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

// InsertKnowledgeLink — the LinkAccrualStore extension of the slow-test
// adapter (v1.20.0 T5): the idempotent knowledge_links INSERT over the
// pool (the same statement the production realStore carries in
// cli/root.go — the write-guard scope keeps the literal in store
// implementations).
func (s *writeRealStore) InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation)
		 SELECT $1, $2, $3
		 WHERE NOT EXISTS (
		   SELECT 1 FROM knowledge_links
		   WHERE from_id = $1 AND to_id = $2 AND relation = $3)`,
		fromID, toID, relation)
	return err
}

func (s *writeRealStore) Close() {
	s.Repo.Close()
	s.pool.Close()
}

// TestWriteBatchDedupRealSlow — the real-DB slow benchmark: the
// round-trip times of the batch dedup for a first accrual (new) of 5k
// synthetic lines·a second resend (all duplicates). The converted
// per-line-lookup comparison figures are logged (synthetic demo data
// only — rows cleaned by tag).
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
	const tag = "wt3-합성-" // the topic prefix (cleanup marker) — tags are unused since they produce vocabulary warnings polluting the bench output
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
	// A 50k conversion (linear approximation — a log for the benchmark
	// record).
	t.Logf("실DB 배치 dedup: 1차(신규 %d줄)=%v·2차(전부 dup)=%v·50k 환산 1차≈%v·2차≈%v",
		lines, fresh.Round(time.Millisecond), rerun.Round(time.Millisecond),
		(fresh * 10).Round(time.Millisecond), (rerun * 10).Round(time.Millisecond))
}

// TestSanitizePayloadTagsSecrets — sanitization·secret scanning also
// apply to tags array elements·domain (formerly blind spots).
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
	// Allowing storage after a secret warning is the existing ruling (the
	// same contract as string fields) — here the warning firing and the
	// marker neutralization are each asserted.
	p2 := map[string]any{"tags": []any{"a[flint]b"}}
	_ = SanitizePayload(p2)
	if raw, _ := p2["tags"].([]any); len(raw) > 0 {
		if cell, _ := raw[0].(string); strings.Contains(cell, "[flint]") {
			t.Errorf("tags 마커 미중립: %v", p2["tags"])
		}
	}
}

// ── The write stdin row cap (reusing export_import_max_rows). ──

// writeMaxRowsFixture — a synthetic N-line knowledge stdin for the cap
// checks (no hash collisions).
func writeMaxRowsFixture(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "{\"topic\":\"demo write maxrows %02d\",\"summary\":\"합성 데모 — write 상한 검증 줄 %02d\",\"confidence\":0.9}\n", i, i)
	}
	return b.String()
}

// TestRunWriteMaxRowsExceeded — when the stdin line count exceeds the
// cap, exit 3·the error JSON includes max·count, and no accrual happens
// (the same shape as the import enforcement — memory defense against
// accruing a huge stdin in full).
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

// TestRunWriteMaxRowsBoundary — a line count equal to the cap passes
// (count == max allowed — only the excess is blocked. The same boundary
// contract as import).
func TestRunWriteMaxRowsBoundary(t *testing.T) {
	f := maxRowsFake("2")
	out, code := run(t, f, []string{"knowledge"}, writeMaxRowsFixture(2))
	if code != 0 || !strings.Contains(out, `"inserted":2`) {
		t.Fatalf("경계(==max) exit=%d out=%s", code, out)
	}
}

// ── The per-payload field byte cap (a constant — aligned with compact's
// 8MiB). ──

// TestRunWriteFieldBytesExceeded — when a single field value exceeds
// maxPayloadFieldBytes (aligned with compactMaxBytes 8MiB), it is
// rejected at the Validate stage (exit 3) and no accrual happens
// (unbounded-OOM defense against a huge field flowing into
// sanitize·hash·accrue — only the excess is blocked).
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

// TestRunWriteFieldBytesBoundary — exactly the cap size passes (only the
// excess is blocked); several fields each within the cap pass even if
// the total exceeds it (the cap is per single field).
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

// ── The concurrent write race survive — a slow real-DB parallel
// regression. ──

// openWriteRealStore — builds the real-DB store only with
// SUPERLITE_REAL=1 + FLINT_TEST_DSN set (inheriting the writeRealDSN
// contract — operational fallback blocked). Also returns the DSN (for
// the race opponent writer's extra connection).
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

// raceStdin — the synthetic stdin for the parallel race check: shared
// lines carry identical content (identical content_hash) for both
// writers; uniq lines carry content unique per writerTag.
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

// raceRowCount — counts the rows with the roundTag prefix (residue
// check).
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

// TestWriteParallelRaceRealSlow — when 2 writers (independent sessions)
// concurrently write 50 identical-content lines + 50 unique lines each,
// the race loser also keeps all its unique lines via the survive path
// (content_hash recheck skip + one retry of the remainder) — both sides
// exit 0·150 total rows remain. Before the fix, the loser's InsertBatch
// commit-time violation propagated unclassified (XX000 "constraint
// violations, transaction rolled back") and its 50 unique lines were
// lost. Multiple rounds probabilistically cover the race crossing (both
// pre-checks passing) — the deterministic crossing check belongs to
// lib/db TestInsertBatchCommitRaceClassifiedRealSlow.
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

// ── v1.11.0 T1 — the source registry (correction added) is the single
// source: usage·ErrInvalidSource are derived from it (no duplicate
// hardcoded lists). ──

// TestParseSourceCorrectionValid — "correction" is a registered source:
// ParseSourceFlag accepts it, and RunWrite with --source correction
// commits exit 0 (metered under correction — the registry admission
// contract).
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

// TestParseSourceUnregisteredRejectedExit3 — an unregistered value is
// rejected: ParseSourceFlag → ErrInvalidSource·RunWrite exit 3 with no
// insert (the inherited invalid-source contract).
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

// TestSourceRegistryDerivationGuard — the homogeneous guard: the usage
// hint (--help)·the ErrInvalidSource message·the validSources membership
// all stay in sync with the sourceRegistry (a hardcoded list drifting
// from the registry fails here — dual maintenance abolished).
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

// ── v1.12.0 T1 — the `gap` channel registration: the fifth metering
// source (recall-failure gap candidates — T3 accrues them; no defaults
// are attached at write time, unlike correction). The registry remains
// the single source: usage·ErrInvalidSource·membership derive from it. ──

// TestParseSourceGapValid — "gap" is a registered source: ParseSourceFlag
// accepts it, and RunWrite with --source gap commits exit 0 (metered
// under gap — the registry admission contract). A gap write attaches no
// tag·confidence defaults (T3's candidate payload supplies them).
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

// TestParseSourceUnregisteredStillRejectedAfterGap — the gap admission
// keeps the invalid-source contract: an unregistered value is still
// rejected exit 3 (guard against a loosened membership check).
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

// ── v1.11.0 T2 — the --source correction path: only two defaults are
// added (missing confidence → the correction_confidence threshold·the
// "correction" tag merged); validate→hash dedup→classify→approve are the
// existing circuits reused unchanged. ──

// TestWriteCorrectionDefaults — a correction write without confidence·tags:
// confidence defaults to the threshold 0.7 (below the 0.8 floor → pending),
// tags become ["correction"], single-line output state=pending.
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

// TestWriteCorrectionRespectsExplicitConfidence — an explicitly given
// confidence is respected (the manual contract unchanged): 0.95 → active
// with confidence 0.95; the correction tag is still merged.
func TestWriteCorrectionRespectsExplicitConfidence(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"t2-correction-explicit","summary":"합성 데모","confidence":0.95}`+"\n")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["state"] != "active" {
		t.Fatalf("state=%v, want active(지정 0.95 존중)", m["state"])
	}
	if conf, _ := f.inserted[0]["confidence"].(float64); conf != 0.95 {
		t.Fatalf("confidence=%v, want 0.95(지정값 존중)", f.inserted[0]["confidence"])
	}
	if f.inserted[0]["status"] != "active" {
		t.Fatalf("status=%v, want active", f.inserted[0]["status"])
	}
}

// TestWriteCorrectionTagMerge — the correction tag merges with existing
// tags (order preserved, appended last)·is idempotent (no duplicates when
// already present).
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

// hashMatchStore — a fakeStore whose ExistsHash answers existing only for
// the one given hash (single-line dedup: a hash-accurate existing-row
// response without implementing DupIDStore/DupMetaStore — the ExistsHash
// fallback path).
type hashMatchStore struct {
	*fakeStore
	hash string
}

func (h *hashMatchStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return hash == h.hash, nil
}

// TestWriteCorrectionDedupCircuitReused — the correction path reuses the
// content_hash dedup circuit unchanged (no separate branch): the same
// correction payload resent after the hash exists is a bare duplicate
// (exit 0 {"status":"duplicate"} — the existing idempotence contract).
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
	// 2nd: the exact hash of the merged-tag payload is judged existing —
	// the same single-line ExistsHash lookup circuit (no correction-specific
	// dedup). hashMatchStore answers existing only for that hash.
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

// TestWriteCorrectionConfidenceThresholdOverride — the default confidence
// comes from the thresholds registry (meta correction_confidence), not a
// code constant: an override 0.5 lands as the default·pending.
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

// TestWriteManualMissingConfidenceStillRejected — without correction, the
// existing manual contract is unchanged: a missing confidence is a
// Validate error (exit 3), no default injection.
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

// ── v1.11.0 T3 — the correction daily cap (correction_daily_max): lines
// past the day's committed-correction quota are quietly skipped with one
// warning line (exit 0 — independent of the dup mechanism; the count
// source is the daily:correction_write:<date> flat key). ──

// correctionCapFake — a fakeStore answering the two meta lookups of the
// cap path: the thresholds IN query (a correction_daily_max override when
// set) and today's daily:correction_write:<date> count (empty = 0). The
// daily:correction_write UPSERT bumps are counted into bumps (the key
// rides as a query arg, so the SQL-recording path cannot see it).
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

// TestWriteCorrectionDailyCapReachedSkips — the quota exhausted: the line
// is quietly skipped (no insert·no commit) with one warning line, exit 0.
func TestWriteCorrectionDailyCapReachedSkips(t *testing.T) {
	var bumps int
	f := correctionCapFake("", "20", &bumps) // the default cap 20 already committed today
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

// TestWriteCorrectionDailyCapPartial — quota 1 left with 2 lines: only
// the first accrues·commits (partial commit), the second is skipped with
// one warning line; the committed count is bumped once.
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

// TestWriteCorrectionDailyCapOverride — the cap comes from the thresholds
// registry (meta correction_daily_max), not a code constant: an override
// 1 with 2 fresh lines accrues only the first.
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

// TestWriteCorrectionDailyCapUnderPasses — quota remaining: lines pass
// untouched (no warning·normal commit·bump by the committed count) — the
// cap interferes only at the boundary.
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

// TestWriteCommitMessageNamePreferred — the D4 (R33) regression: the
// commit title prefers the project's name ("project: <name>"). Without
// name (knowledge etc.), the existing summary fallback is unchanged —
// before the fix, project stored the name field but the title went out
// as summary's (nil) "<nil>".
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

// ── v1.12.0 T3 — the gap-candidate accrual: a recall failure count at
// the gap_query_threshold accrues a pending candidate (source gap·tag
// gap-candidate·confidence 0.4·N-invariant content_hash succession). ──

// gapAccrualStore — a fakeStore layered for the accrual: DupMetaStore
// rows by hash (pending/active existing-row shapes) + the
// UpdatePendingSummary seam recorder.
type gapAccrualStore struct {
	*fakeStore
	dupRows map[string]DupMeta
	updates []string // "id:summary" per UpdatePendingSummary call
}

func (g *gapAccrualStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	m, ok := g.dupRows[hash]
	return m, ok, nil
}

func (g *gapAccrualStore) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	g.updates = append(g.updates, fmt.Sprintf("%d:%s", id, summary))
	return 1, nil
}

// captureStderr — swaps os.Stderr for a pipe around fn (the cap warning
// line assertion).
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

// TestGapCandidateAccrualAtThreshold — the count reaching the threshold
// (3 — the clamped default: the fake carries no meta override) ADDs one
// pending candidate: topic prefix + the 60-rune normalized query·summary
// N·confidence 0.4·tag :gap-candidate:·source-gap metering·one commit.
// Below the threshold nothing accrues.
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

// TestGapCandidateThresholdMetaOverride — the meta override loads through
// LoadThresholds: gap_query_threshold=5 in meta moves the trigger (3 →
// no accrual; 5 → accrual).
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

// TestGapCandidatePendingRefreshUpdatesN — an existing pending row with
// the same N-invariant hash gets its summary N refreshed via the
// UpdatePendingSummary seam (an UPDATE, not a supersede) — no new insert.
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

// TestGapCandidateActiveNoop — an existing active row with the same hash
// is left untouched (the promotion-fixed integrity ruling): no update,
// no insert, no commit.
func TestGapCandidateActiveNoop(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{
		gapCandidateHash(NormalizeGapQuery("데모 쿼리")): {ID: 9, Topic: "격차 후보: 데모 쿼리", State: "active"},
	}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
	if len(f.updates) != 0 || len(f.inserted) != 0 || len(f.commits) != 0 {
		t.Fatalf("active NOOP 위반: updates=%v inserted=%d commits=%d", f.updates, len(f.inserted), len(f.commits))
	}
}

// TestGapCandidateDailyCapSkipsQuietly — with the day's ADD count at the
// cap (10 — the clamped default), the candidate is quietly skipped with
// one stderr warning line; the counter key counts ADDs only.
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

// TestGapCandidateHashInvariantToN — the digest is computed over the
// topic·tag·confidence shape with the summary omitted: the same query at
// different N hashes identically (the succession key), different queries
// hash apart.
func TestGapCandidateHashInvariantToN(t *testing.T) {
	h := gapCandidateHash(NormalizeGapQuery("데모 쿼리"))
	if h != gapCandidateHash(NormalizeGapQuery("데모 쿼리")) {
		t.Fatal("동일 쿼리 해시 불안정")
	}
	if h == gapCandidateHash(NormalizeGapQuery("다른 쿼리")) {
		t.Fatal("상이 쿼리 해시 충돌")
	}
}

// ── v1.17.0 T2 — the echo field of the single-line committed output. ──

// echoWTestTopic — the shared synthetic topic of the echo write fixtures
// (the echo_test.go convention).
const (
	echoWTestTopic   = "데모 프로젝트 운영 기록"
	echoWDupSummary  = "회의록 오늘 확정된 마일스톤은 3개다"
	echoWContraSum   = "회의록 다음 주는 전체 휴가 기간이다"
	echoWOtherTopic  = "개인 독서 목록 정리"
	echoWOtherSummar = "올해 읽은 책 다섯 권 감상"
)

// runWriteEcho — runs RunWrite with one knowledge JSON line (confidence
// 0.9 — active) and returns the decoded output map.
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

// TestWriteEchoFieldOnNewKnowledge — a new single-line knowledge write
// reports the matching active row as an echo candidate: the committed
// output gains "echo":[{id,topic,score,relation}] (the field name is
// score, not similarity) while the legacy status/hash/state/id fields
// survive verbatim (backward compatibility — old readers ignore the new
// field).
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

// TestWriteEchoOmittedOnExactDup — an exact-hash duplicate line answers
// with the DuplicateMeta shape and no echo field (the response already
// names the existing row — the double-report ruling).
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

// TestWriteEchoOmittedOnScanFailure — an echo scan failure quietly omits
// the field: the write still commits, exit 0 (§13).
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

// TestWriteEchoOmittedWhenNoCandidates — zero qualifying rows omit the
// field (no empty array).
func TestWriteEchoOmittedWhenNoCandidates(t *testing.T) {
	st := &fakeStore{rows: [][]any{{int64(9), echoWOtherTopic, echoWOtherSummar}}}
	m, _ := runWriteEcho(t, st, echoWTestTopic, echoWDupSummary, 0.9)
	if _, ok := m["echo"]; ok {
		t.Errorf("후보 0건에 echo 부재해야 함: %v", m)
	}
}

// TestWriteEchoMultiLineOmitted — the multi-line aggregate output keeps
// its shape (no echo field — the response is a batch aggregate, not a
// per-line verdict).
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

// TestWriteEchoThresholdSeam — the loaded thresholds flow into the scan:
// an echo_min_score of 0.99 excludes the contradicts candidate that the
// default (0.3) admits.
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

// ── v1.20.0 T5 — the optional stdin relations array (schema v14
// knowledge_links): vocabulary validation (exit 3), shape validation
// (exit 3), target-row-existence refusal (exit 3), idempotent accrual,
// the duplicate-path accrual onto DupMeta.ID, and hash invariance (the
// same body with·without relations dedups — the T1 hashVolatile seal).
// All synthetic demo data. ──

// relPayload — one synthetic relations write payload line.
func relPayload(topic, summary string, relations string) string {
	if relations == "" {
		return fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9}`, topic, summary)
	}
	return fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":0.9,"relations":[%s]}`, topic, summary, relations)
}

// TestWriteRelationsVocabularyReject — an out-of-vocabulary relation is
// refused at exit 3 with the "rejected relation:" class (the
// colon-token rejection precedent — refused, not warned).
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

// TestWriteRelationsShapeReject — a malformed relations entry (a
// non-integer to·a missing relation) is a validation failure (exit 3).
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
	// a non-array relations value is the same class
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"합성 형식 비배열","summary":"관계 비배열 거부 합성","confidence":0.9,"relations":{"to":1}}`)
	if code != 3 {
		t.Fatalf("non-array exit=%d out=%s, want 3", code, out)
	}
}

// TestWriteRelationsTargetMissingFast — the target-existence SELECT
// answering no rows (fake default: f.rows=nil) makes the write refuse
// at exit 3 (the write-path existence check — no FK, the v14
// convention).
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

// t5ScratchStore — the dedicated T5 scratch database: created before·
// dropped after the scenario (never the shared flint_test — the slow
// isolation ruling). Requires SUPERLITE_REAL=1 + FLINT_TEST_DSN.
func t5ScratchStore(t *testing.T) *writeRealStore {
	t.Helper()
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		t.Skipf("DSN 경로 없음: %s", dsn)
	}
	name := fmt.Sprintf("flint_t5links_%d", time.Now().UnixNano())
	admin, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("admin pool unavailable: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Skipf("scratch create failed: %v", err)
	}
	admin.Close()
	repo, err := db.NewRepo(ctx, dsn[:slash+1]+name)
	if err != nil {
		t.Skipf("repo unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn[:slash+1]+name)
	if err != nil {
		repo.Close()
		t.Skipf("pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(func() {
		st.Close()
		adm, err := db.Connect(ctx, dsn)
		if err == nil {
			_, _ = adm.Exec(ctx, `DROP DATABASE `+name)
			adm.Close()
		}
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	return st
}

// t5LinkRows — the scratch knowledge_links rows (from_id,to_id,relation).
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

// TestWriteLinksRealSlow — the real-DB relations scenarios on the
// dedicated scratch database: accrual, idempotent resend, the
// duplicate-path link accrual (DupMeta.ID — hash invariance sealed by
// the same-body duplicate verdict), and the target-missing refusal.
func TestWriteLinksRealSlow(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	// Row A·B accrual — ids parsed from the committed output.
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

	// 1) new-row accrual: C carries relations onto A·B.
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

	// 2) idempotent resend: the identical line is a duplicate — no new
	// link rows (the NOT EXISTS guard).
	out2, code2 := run(t, st, []string{"knowledge"}, relPayload("합성 링크 C", "링크 시나리오 합성 본문 C", rels))
	if code2 != 0 || !strings.Contains(out2, `"duplicate"`) {
		t.Fatalf("재전송 exit=%d out=%s, want duplicate", code2, out2)
	}
	if got := len(t5LinkRows(t, st)); got != nLinks {
		t.Fatalf("재전송 후 링크 수=%d, want %d (멱등)", got, nLinks)
	}

	// 3) duplicate-path accrual: D was accrued without relations; the
	// same body resent WITH relations must land them on the existing
	// DupMeta.ID row (hash invariance — relations excluded from the
	// hash — is what makes this a duplicate at all).
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

	// 4) target missing: a relation to a nonexistent row refuses at
	// exit 3 (nothing partially accrued — the body itself is refused).
	out5, c5 := run(t, st, []string{"knowledge"},
		relPayload("합성 링크 E", "링크 시나리오 합성 본문 E", `{"to":99999999,"relation":"refines"}`))
	if c5 != 3 {
		t.Fatalf("대상 부재 exit=%d out=%s, want 3", c5, out5)
	}
	if n, _ := st.Query(ctx, `SELECT id FROM knowledge WHERE topic = '합성 링크 E'`); len(n) != 0 {
		t.Fatalf("대상 부재 거부 시 본문 적립되면 안 됨")
	}
}
