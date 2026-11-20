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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
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
