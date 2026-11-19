package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// fakeStore — Store 인터페이스 fake. stub 필드(exists·*Err)는 지정 시 우선되고,
// rows·queryFn 미지정 시 영(零) 동작을 반환한다. 히스토리 5메서드는
// LogEntries 캡처 계층(아래)·HistoryStubs 기본구현(나머지 4종)으로
// 승계한다.
type fakeStore struct {
	wire.HistoryStubs // LogEntries 오버라이드·나머지 4종 기본구현 승계

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

	// 기록 — report 병렬 수집(v1.9.3 T2)이 Query를 동시 호출하므로
	// 기록 계열 필드 접근은 전부 mu로 직렬화한다(doctor_test 선례 계열).
	// Query는 queryFn 호출까지 락 내에서 실행 — queryFn 클로저의 테스트
	// 로컬 기록(queries·argsBySQL append 등)도 같이 직렬화된다.
	mu       sync.Mutex
	inserted []map[string]any
	commits  []string
	queries  []string
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, msg)
	if f.commitErr != nil {
		return "", f.commitErr
	}
	return "abc123", nil
}
func (f *fakeStore) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeStore) Close()                         {}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return f.rows, nil
}

// 히스토리 5메서드 — wire.Store 계약 충족용 캡처 계층. db 실SQL(방언)은
// lib/db 소유 — 여기선 LogQuery 모드 식별자와 인자를 Query seam(queryFn·
// queries 캡처)으로 내려보내 소비 테스트가 "어떤 로그 조회에 어떤 인자가
// 갔는가"를 단언하게 한다. 모드 식별자는 방언 아닌 조각(LIKE·date <= $1·
// commit_order 정렬·message NOT LIKE)로 구분된다(asof_test·audit_test 등
// queryFn 소비자 호환). DiffRows·DiffStat·DiffCommits·QueryAsOf는 소비자가
// 계약 오버라이드 fake를 쓰므로 HistoryStubs 기본구현으로 승계.
func (f *fakeStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	var (
		sql  string
		args []any
	)
	switch {
	case q.Earliest:
		sql = `SELECT date FROM commit_log ORDER BY commit_order ASC LIMIT 1`
	case q.Prefix != "":
		sql = `SELECT commit_hash FROM commit_log WHERE commit_hash LIKE $1||'%' ORDER BY commit_order DESC`
		args = []any{q.Prefix}
	case q.Hash != "":
		sql = `SELECT commit_hash, committer, date, message FROM commit_log WHERE commit_hash = $1`
		args = []any{q.Hash}
	case !q.BeforeOrAt.IsZero():
		sql = `SELECT commit_hash FROM commit_log WHERE date <= $1 ORDER BY commit_order DESC LIMIT 1`
		args = []any{q.BeforeOrAt}
	case q.LatestN > 0:
		// 조립 순서 승계: NOT LIKE→LIKE, $2부터 순차 부여, AND 결합,
		// LIMIT $1은 문미(인자 전달 검증용).
		sql = `SELECT commit_hash, committer, date, message FROM commit_log`
		args = []any{q.LatestN}
		var conds []string
		ph := 2
		if q.MessageNotLike != "" {
			conds = append(conds, fmt.Sprintf(`message NOT LIKE $%d`, ph))
			args = append(args, q.MessageNotLike)
			ph++
		}
		if q.MessageLike != "" {
			conds = append(conds, fmt.Sprintf(`message LIKE $%d`, ph))
			args = append(args, q.MessageLike)
		}
		if len(conds) > 0 {
			sql += ` WHERE ` + strings.Join(conds, ` AND `)
		}
		sql += ` ORDER BY commit_order DESC LIMIT $1`
	default:
		return nil, fmt.Errorf("log entries: empty query")
	}
	rows, err := f.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return fakeLogEntryRows(rows), nil
}

// fakeLogEntryRows — 로그 조회 행 → LogEntry 관용 매핑. 열 폭 관례 승계:
// 4열(hash·committer·date·message), 2열(hash·date), 1열은 값이 시각
// 파싱되면 date(Hash 모드 date 단독 픽스처), 아니면 hash.
func fakeLogEntryRows(rows [][]any) []wire.LogEntry {
	var out []wire.LogEntry
	for _, r := range rows {
		var e wire.LogEntry
		switch len(r) {
		case 0:
			continue
		case 1:
			if t, ok := fakeLogDate(r[0]); ok {
				e.Date = t
			} else {
				e.Hash = fmt.Sprint(r[0])
			}
		case 2:
			e.Hash = fmt.Sprint(r[0])
			e.Date, _ = fakeLogDate(r[1])
		default:
			e.Hash = fmt.Sprint(r[0])
			e.Committer = fmt.Sprint(r[1])
			e.Date, _ = fakeLogDate(r[2])
			e.Message = fmt.Sprint(r[3])
		}
		out = append(out, e)
	}
	return out
}

// fakeLogDate — parseLogDate(db)와 동등: time.Time 그대로, 문자열은
// 레이아웃 3종 파싱.
func fakeLogDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, lay := range []string{
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999",
			time.RFC3339,
		} {
			if ts, err := time.Parse(lay, x); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}

// seamRealDSN — SUPERLITE_REAL=1 실구현 seam의 DSN 해결: FLINT_TEST_DSN
// 설정 시만 실DB 접속을 허용한다. 미설정이면 dsnFromEnv 기본값(운영
// 5434/flint)으로 폴백하지 않고 ("", false) — 호출원 seam은 fake 경로로
// 내려가 운영 지표 오염을 원천 차단한다.
func seamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

// withFake — newStore seam 교체. 기본 fake, FLINT_TEST_DSN 설정 +
// SUPERLITE_REAL=1이면 실구현 위임 fake(미설정 stub만 위임). 실DB 미기동 시
// 스킵. FLINT_TEST_DSN 미설정 시에는 실구현으로 폴백하지 않는다.
func withFake(t *testing.T, f *fakeStore) {
	t.Helper()
	prev := newStore
	if dsn, real := seamRealDSN(); real {
		realStore, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(func() { realStore.Close() })
		delegate := realStore
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

// cleanupRealResidue — SUPERLITE_REAL seam에서 공유 실DB의 잔재를 정리한다
// (best-effort — 미설정/접속 실패/쿼리 실패는 조용히 넘긴다). 해시 dedup·
// 빈도 상한 meta·PK가 공유 DB에 남아 실DB seam 테스트의 멱등성을 깨는 것을
// 시작 경계에서 지운다(TestImportMigrateJSONLRealSlow 정리 관례의 배선 버전).
func cleanupRealResidue(queries ...string) {
	dsn, ok := seamRealDSN()
	if !ok {
		return
	}
	st, err := newRealStore(context.Background(), dsn)
	if err != nil {
		return
	}
	defer st.Close()
	for _, q := range queries {
		_, _ = st.Query(context.Background(), q)
	}
}

// delegatingFake — stub 미지정 연산은 실구현에 위임해 seam 검증이 실코드를 통과하게 한다.
type delegatingFake struct {
	fake *fakeStore
	real Store
}

func (d *delegatingFake) Migrate(ctx context.Context) error { return d.real.Migrate(ctx) }

// 히스토리 5메서드 — 위임 패턴 승계(실구현 seam 그대로 통과).
func (d *delegatingFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	return d.real.LogEntries(ctx, q)
}
func (d *delegatingFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return d.real.QueryAsOf(ctx, q)
}
func (d *delegatingFake) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return d.real.DiffRows(ctx, fromHash, toHash)
}
func (d *delegatingFake) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return d.real.DiffStat(ctx, hashes)
}
func (d *delegatingFake) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return d.real.DiffCommits(ctx, fromHash, toHash)
}
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

// Close — no-op: Run의 `defer st.Close()`(wire/run.go)가 커맨드마다 호출하는데,
// real 스토어는 테스트 간 공유 풀이므로 여기서 닫으면 이후 커맨드가 closed
// pool 에러로 실패한다. 실 스토어 종결 소유권은 withFake의 t.Cleanup에 있다.
func (d *delegatingFake) Close() {}
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
	// write는 content_hash dedup(공유 실DB)를 통과한다 — 실행마다
	// 고유 topic으로 잔재 해시 충돌을 회피해야 committed가 멱등하다.
	topic := fmt.Sprintf("t-%s-%d", t.Name(), time.Now().UnixNano())
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+topic+`","summary":"s","confidence":0.9,"tags":["b","a"]}`)
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
	// 잔재 해시 충돌 회피(TestWriteCommitted 주석 참조).
	topic := fmt.Sprintf("t-%s-%d", t.Name(), time.Now().UnixNano())
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+topic+`","summary":"s","confidence":0.79}`)
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
	// 공유 실DB의 프로젝트명 잔재 충돌 회피 — 실행마다 고유 name.
	name := fmt.Sprintf("p1-%s-%d", t.Name(), time.Now().UnixNano())
	_, code := mustRun(t, []string{"write", "project"},
		`{"name":"`+name+`","status":"active","note":"메타데이터"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	rec := f.inserted[0]
	// note는 project 컬럼으로 등록돼 저장된다(kindColumns).
	if rec["note"] != "메타데이터" {
		t.Fatalf("note 컬럼 미저장: %v", rec)
	}
	if rec["name"] != name || rec["status"] != "active" {
		t.Fatalf("컬럼 유실: %v", rec)
	}
	// 미등록 키는 여전히 무경고 삭제 금지: warnings에 있어야 한다(다른 키로 검증).
	out2, code2 := mustRun(t, []string{"write", "project"},
		`{"name":"`+name+`x","status":"active","bogus_key":"v"}`)
	if code2 != 0 {
		t.Fatalf("exit=%d, want 0", code2)
	}
	m := decode(t, out2)
	w, ok := m["warnings"].([]any)
	if !ok || len(w) == 0 || !strings.Contains(fmt.Sprint(w[0]), "bogus_key") {
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

func TestRetriesExhaustedExit1(t *testing.T) {
	f := &fakeStore{commitErr: store.ErrRetriesExhausted}
	withFake(t, f)
	// 잔재 해시 충돌 회피(TestWriteCommitted 주석 참조) — duplicate로
	// commit 경로 도달 전 스킵되면 exit 1 재현 자체가 불가능하다.
	topic := fmt.Sprintf("t-%s-%d", t.Name(), time.Now().UnixNano())
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"`+topic+`","summary":"s","confidence":0.9}`)
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
	// --json에서 요청 limit이 상한(50) 초과면 additive clamp
	// 라인(limit_requested·limit_applied)이 행 뒤에 따른다(기존 키 불변).
	want := "{\"id\":\"1\",\"topic\":\"t\"}\n" +
		`{"limit_requested":999,"limit_applied":50}`
	if strings.TrimSpace(out) != want {
		t.Fatalf("json 출력=%q", out)
	}
}

func TestHotTruncatedToBudget(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"decision", strings.Repeat("가", 3000), "s", "ts"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"hot", "--session", "S1", "--full"}, "")
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
