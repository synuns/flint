// write_test.go — v0.39.0 T2: lib/cli/write_test.go 이동(write 5단계
// 파이프라인 계약 — dedup·레이스 재확인·경고 병합). 계약 불변: duplicate
// 줄은 skip(신규 줄 부분 커밋), 전부 duplicate 재실행은 0건 커밋·exit 0
// "duplicate"(시딩 멱등), 2줄 이상 출력은 집계형(inserted·duplicates·
// 커밋 hash), 단일 줄 출력은 기존 불변. 레이스(InsertBatch ErrDuplicate)는
// 재확인 skip + 잔여 재시도로 신규줄 유실 제거. dispatch(mustRun) 경유
// 테스트는 cli 잔류(write_v06 등)가 담당 — 여기선 RunWrite 직접 호출.
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeStore — wire.Store 인터페이스 fake(cli_test.go 관례 이동분). stub
// 필드(exists·*Err)는 지정 시 우선되고, rows·queryFn 미지정 시 영(零)
// 동작을 반환한다.
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
	// hashExistsFn — v1.6.0 T3: content_hash = ANY($1) 배치 dedup 조회의
	// 해시별 응답(미지정 시 exists 플래그로 전 해시 균일 응답).
	hashExistsFn func(hash string) (DupMeta, bool)

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
	// v1.6.0 T3: 배치 dedup 조회(content_hash = ANY($1)) 응답 — 해시별
	// hashExistsFn(미지정 시 exists 플래그)로 기존 행 메타 행을 만든다.
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

// run — RunWrite 직접 실행 + 출력 반환(dispatch 토큰 "write"는 제외한
// args). cli mustRun 경유 테스트와 동일 계약, 저장소 주입만 명시적.
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

// TestWriteJSONLMixedCommitsNew — 신규+duplicate 혼합: duplicate 줄은 skip,
// 신규 줄만 커밋(부분 커밋). exit 0·inserted=1·duplicates=1.
func TestWriteJSONLMixedCommitsNew(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: 11}, calls == 1 // 첫 줄(rv-b)만 기존 레코드
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

// TestWriteJSONLAllDuplicateIdempotent — 전부 duplicate 재실행: 0건 커밋·
// exit 0·{"status":"duplicate"} 한 줄(시딩 멱등 계약).
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

// TestWriteJSONLMultiOutputNoFieldMix — 2신규(pending+active 혼합 confidence):
// 다중 줄 출력에 id·state 없음·inserted=2·hash는 실제 커밋 1개(커밋 해시).
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

// TestWriteRaceRecoversNewLines — 레이스(사전 확인 통과 후 배치에서 일부만
// 동시 삽입): 재확인으로 존재 줄은 skip, 신규 줄은 재시도로 커밋(회귀: 전체
// 롤백 + exit 0 "duplicate"로 신규줄 유실). inserted=1·duplicates=1.
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

// TestWriteRaceAllDupIdempotent — 레이스 후 재확인에서 전부 존재: 신규줄
// 유실 없이 0건 커밋·exit 0 "duplicate"(시딩 멱등 계약 유지).
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

// TestWriteRaceRetryConflictErrors — 재확인을 통과한 줄의 재시도까지
// ErrDuplicate면 실제 충돌: exit 0 "duplicate"가 아닌 에러 보고(회귀: 유실
// 가능한 경로의 exit 0 성공 위장).
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

// countCall — RecordUsageCount 호출 기록(kind, source, count).
type countCall struct {
	kind, source string
	n            int
}

// countRecorder — RecordUsage(1회)·RecordUsageCount(일괄) 호출을 녹음하는
// fake: realStore 계측 세임(#5: dup N을 1회 호출로 일괄 증분). dedup 시나리오
// 는 fakeStore.hashExistsFn(배치 ANY($1) 조회 응답)로 지정한다.
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

// TestWriteDupBatchSingleCountCall — dup 계측은 줄 수와 무관하게 각 키
// RecordUsageCount 1회 호출(회귀: recordDups가 RecordUsage를 N회 루프 — O(N)
// 읽기-쓰기). write 1회(RecordUsage)·총계 dup N + 소스 dup_<source> N
// (RecordUsageCount 각 1회 — v0.10.2 R2 additive, 룰링 갱신).
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
			return DupMeta{}, calls <= 2 // 첫 2줄만 기존 레코드
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

// TestWriteAllDuplicateKeepsWarnings — 전부 duplicate여도 경고(비밀 스캔)는
// 폐기되지 않는다(회귀: recs==0 분기가 경고 없이 duplicate 한 줄만 출력).
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

// ── v1.6.0 T3(R30-C C3): write 배치 dedup — 줄별 lookupHash(N왕복) 대신
// lookupHashBatch(content_hash = ANY($1) 청크) 1회 조회. 단발(1행 hook
// 패턴)은 기존 줄별 조회 계약 불변. ──

// dedupQueryCount — 기록된 질의 중 배치 dedup(ANY($1)) 질의 수.
func dedupQueryCount(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "content_hash = ANY($1)") {
			n++
		}
	}
	return n
}

// TestWriteMultiLineDedupBatchQuery — 다중 줄 사전 dedup은 줄 수와 무관하게
// ANY($1) 배치 질의 1회(회귀: 줄별 조회 N왕복 — 50k행 RTT 지배). 감지
// 동일성: 기존 줄 skip·신규 줄 부분 커밋·duplicates 계수 불변.
func TestWriteMultiLineDedupBatchQuery(t *testing.T) {
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{ID: calls}, calls == 2 // 둘째 줄만 기존 행
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

// TestWriteSingleLineDedupKeepsRowLookup — 단발(1행 hook 패턴)은 배치
// 질의로 전환하지 않는다: ANY($1) 질의 0회·ExistsHash 줄별 조회 그대로
// (DupMetaStore→DupIDStore→ExistsHash 폴백 계약 불변).
func TestWriteSingleLineDedupKeepsRowLookup(t *testing.T) {
	f := &fakeStore{exists: true} // ExistsHash 폴백 경로
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

// TestWriteMultiLineBatchLookupErrorExits — 배치 dedup 조회 오류는 조용한
// 신규 판정 없이 전파(오류 계약: 서버다운 exit 2 — 래핑을 거쳐도 보존).
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

// TestWriteMultiLineDedupChunked — 배치 질의도 청크 경계를 넘으면 청크
// 수만큼 발화(lookupHashBatch importDedupChunk 재사용 — 경계 축소 검증).
func TestWriteMultiLineDedupChunked(t *testing.T) {
	saved := importDedupChunk
	importDedupChunk = 2
	t.Cleanup(func() { importDedupChunk = saved })
	calls := 0
	f := &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		calls++
		return DupMeta{}, calls <= 2 // 첫 2줄만 기존 행
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

// BenchmarkWriteMultiLinePipeline — 합성 다중 줄 write 파이프라인(검증→
// 살균→해시→배치 dedup→적립)의 줄당 CPU 비용(fake 저장소 — RTT 제외).
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

// writeRealDSN — SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만 실DB 접속
// 허용(cli seamRealDSN·importRealDSN 계약 승계 — 운영 폴백 차단).
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

// writeRealStore — db.Repo 쓰기 + 풀 읽기 어댑터(importRealStore 관례).
type writeRealStore struct {
	*db.Repo
	pool *pgxpool.Pool
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

// TestWriteBatchDedupRealSlow — 실DB slow 벤치: 합성 5k줄 1차 적립(신규)
// ·2차 재전송(전부 duplicate)의 배치 dedup 완복 시간. 줄별 조회 대비
// 환산 수치를 로그로 남긴다(합성 데모 데이터만 — 태그로 행 정리).
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
	const tag = "wt3-합성-" // topic 접두사(정리 마커) — tags는 어휘 경고를 만들어 벤치 출력을 오염시키므로 쓰지 않는다
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
	// 50k 환산(선형 근사 — 벤치 기록용 로그).
	t.Logf("실DB 배치 dedup: 1차(신규 %d줄)=%v·2차(전부 dup)=%v·50k 환산 1차≈%v·2차≈%v",
		lines, fresh.Round(time.Millisecond), rerun.Round(time.Millisecond),
		(fresh * 10).Round(time.Millisecond), (rerun * 10).Round(time.Millisecond))
}

// TestSanitizePayloadTagsSecrets — v0.20.6(패치 루프 6차 S2): tags 배열
// 원소·domain에도 살균·비밀 스캔이 적용된다(이전 사각지대).
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
	// 비밀은 경고 후 저장 허용이 기존 룰링(문자열 필드와 동일 계약) —
	// 여기선 경고 발화와 마커 중립화를 각각 단언한다.
	p2 := map[string]any{"tags": []any{"a[flint]b"}}
	_ = SanitizePayload(p2)
	if raw, _ := p2["tags"].([]any); len(raw) > 0 {
		if cell, _ := raw[0].(string); strings.Contains(cell, "[flint]") {
			t.Errorf("tags 마커 미중립: %v", p2["tags"])
		}
	}
}
