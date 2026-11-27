package wire_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var _ wire.Store = (*workStateFake)(nil)

type workStateRow struct {
	id            int
	projectID     *int
	task          string
	stateHash     string
	detail        string
	nextAction    string
	status        string
	sessionLabel  string
	byKnowledgeID *int
	updatedAt     time.Time
	resolvedAt    time.Time
}

type workStateFake struct {
	wire.HistoryStubs
	states []*workStateRow
	nextID int
}

func newWorkStateFake() *workStateFake {
	return &workStateFake{nextID: 1}
}

func (f *workStateFake) Migrate(ctx context.Context) error { return nil }
func (f *workStateFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *workStateFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *workStateFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *workStateFake) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *workStateFake) Ping(ctx context.Context) error { return nil }
func (f *workStateFake) Close()                         {}
func (f *workStateFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return nil, nil
}

func workStateString(rec map[string]any, key string) string {
	s, _ := rec[key].(string)
	return s
}

func workStateProjectID(rec map[string]any) *int {
	var projectID *int
	setProject := func(v int64) {
		if v > 0 {
			p := int(v)
			projectID = &p
		}
	}
	switch v := rec["project_id"].(type) {
	case int:
		setProject(int64(v))
	case int64:
		setProject(v)
	}
	return projectID
}

func sameWorkStateProject(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (f *workStateFake) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	db.WarnWorkStateSecrets(db.SanitizeWorkStatePayload(rec))
	projectID := workStateProjectID(rec)
	normalized := db.NormalizeWorkTask(workStateString(rec, "task"))
	if normalized == "" {
		return 0, "", fmt.Errorf("open work state: empty task")
	}
	hash := db.WorkStateHash(normalized)
	detail := workStateString(rec, "detail")
	nextAction := workStateString(rec, "next_action")
	sessionLabel := workStateString(rec, "session_label")
	for _, s := range f.states {
		if sameWorkStateProject(s.projectID, projectID) && s.stateHash == hash && s.status == "open" {
			if s.detail != detail || s.nextAction != nextAction {
				s.detail = detail
				s.nextAction = nextAction
				s.updatedAt = time.Now()
				return s.id, "updated", nil
			}
			return s.id, "noop", nil
		}
	}
	f.nextID++
	row := &workStateRow{
		id:           f.nextID,
		projectID:    projectID,
		task:         normalized,
		stateHash:    hash,
		detail:       detail,
		nextAction:   nextAction,
		status:       "open",
		sessionLabel: sessionLabel,
		updatedAt:    time.Now(),
	}
	f.states = append(f.states, row)
	return row.id, "open", nil
}

func (f *workStateFake) LatestProjectForSession(ctx context.Context, session string) (int, error) {
	if session == "" {
		return 0, nil
	}
	var best *workStateRow
	for _, s := range f.states {
		if s.sessionLabel != session {
			continue
		}
		if best == nil || s.updatedAt.After(best.updatedAt) || (s.updatedAt.Equal(best.updatedAt) && s.id > best.id) {
			best = s
		}
	}
	if best == nil || best.projectID == nil {
		return 0, nil
	}
	return *best.projectID, nil
}

func (f *workStateFake) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	for _, s := range f.states {
		if s.id == id {
			s.status = "resolved"
			s.resolvedAt = time.Now()
			if byKnowledgeID > 0 {
				by := byKnowledgeID
				s.byKnowledgeID = &by
			}
			return nil
		}
	}
	return fmt.Errorf("resolve work state: id %d not found", id)
}

func (f *workStateFake) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	var matched []*workStateRow
	for _, s := range f.states {
		if s.status != "open" || s.byKnowledgeID != nil {
			continue
		}
		if projectID > 0 && (s.projectID == nil || *s.projectID != projectID) {
			continue
		}
		matched = append(matched, s)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].updatedAt.After(matched[j].updatedAt)
	})
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	return workStateRowsToMaps(matched)
}

func (f *workStateFake) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	cutoff := time.Now().AddDate(0, 0, -days)
	var matched []*workStateRow
	for _, s := range f.states {
		if s.status == "open" && s.updatedAt.Before(cutoff) {
			matched = append(matched, s)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].updatedAt.Before(matched[j].updatedAt)
	})
	return workStateRowsToMaps(matched)
}

func workStateRowsToMaps(rows []*workStateRow) []map[string]any {
	out := []map[string]any{}
	for _, s := range rows {
		out = append(out, map[string]any{
			"id":          s.id,
			"project_id":  s.projectID,
			"task":        s.task,
			"state_hash":  s.stateHash,
			"detail":      s.detail,
			"next_action": s.nextAction,
			"status":      s.status,
			"resolved_by": s.byKnowledgeID,
			"updated_at":  s.updatedAt,
		})
	}
	return out
}

type queryOnceFake struct {
	calls int
	errs  []error
	rows  [][]any
	wire.HistoryStubs
}

func (f *queryOnceFake) Migrate(ctx context.Context) error { return nil }
func (f *queryOnceFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *queryOnceFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *queryOnceFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *queryOnceFake) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *queryOnceFake) Ping(ctx context.Context) error { return nil }
func (f *queryOnceFake) Close()                         {}
func (f *queryOnceFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.rows, nil
}

func TestQueryOnceCanceledReturnsImmediately(t *testing.T) {
	f := &queryOnceFake{errs: []error{context.Canceled}}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows=%v, want nil", rows)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1(영구 오류는 재시도 금지)", f.calls)
	}
}

func TestQueryOnceWrappedCanceledNoRetry(t *testing.T) {
	f := &queryOnceFake{errs: []error{fmt.Errorf("query: %w", context.Canceled)}}
	_, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want 래핑된 context.Canceled", err)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1(래핑된 취소도 재시도 금지)", f.calls)
	}
}

func TestQueryOnceRetriesTransientError(t *testing.T) {
	want := [][]any{{int64(1)}}
	f := &queryOnceFake{errs: []error{errors.New("connection refused")}, rows: want}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if err != nil {
		t.Fatalf("err=%v, want 일시 오류 1회 재시도 후 성공", err)
	}
	if len(rows) != 1 || rows[0][0] != int64(1) {
		t.Fatalf("rows=%v, want 재시도 결과 행", rows)
	}
	if f.calls != 2 {
		t.Fatalf("Query 호출 %d회, want 2(일시 오류는 기존 재시도 유지)", f.calls)
	}
}

func TestQueryOnceSuccessSingleCall(t *testing.T) {
	f := &queryOnceFake{rows: [][]any{{int64(7)}}}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if rows[0][0] != int64(7) {
		t.Fatalf("rows=%v, want 7", rows)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1", f.calls)
	}
}

func TestQueryOnceRealCanceled(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("SUPERLITE_REAL=1일 때만 실 store 검증")
	}
	pool, err := db.Connect(context.Background(), testengine.ResolveDSN())
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	st := &poolStore{pool: pool}
	t.Cleanup(st.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = wire.QueryOnce(ctx, st, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

func TestQueryOnceDeadlineExceededReturnsImmediatelyV1321(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	st := &countingQueryStore{}
	_, err := wire.QueryOnce(ctx, st, `SELECT 1`, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if st.calls != 1 {
		t.Fatalf("calls = %d, want 1(만료는 영구 — 재시도 없음)", st.calls)
	}
}

type countingQueryStore struct {
	wire.Store
	calls int
}

func (c *countingQueryStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	c.calls++
	return nil, ctx.Err()
}

type wrappingQueryStore struct {
	wire.Store
	calls int
}

func (w *wrappingQueryStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	w.calls++
	return nil, fmt.Errorf("engine: query failed: %w", context.DeadlineExceeded)
}

func TestQueryOnceWrappedDeadlineNotRetriedV1321(t *testing.T) {
	st := &wrappingQueryStore{}
	_, err := wire.QueryOnce(context.Background(), st, `SELECT 1`, nil)
	if st.calls != 1 {
		t.Fatalf("calls = %d, want 1(래핑된 만료도 영구)", st.calls)
	}
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want wrapped DeadlineExceeded", err)
	}
}

type workStateStore interface {
	OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error)
	LatestProjectForSession(ctx context.Context, session string) (int, error)
	ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error
	ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any
	StaleOpenWorkStates(ctx context.Context, days int) []map[string]any
}

func assertWorkStateScenario(t *testing.T, st workStateStore) {
	t.Helper()
	ctx := context.Background()
	id, res, err := st.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "  Fix: PATH! ", "detail": "d",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
	if res != "open" {
		t.Fatalf("res = %q, want open", res)
	}
	id2, res, err := st.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "fix path", "detail": "d",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id2 != id {
		t.Fatalf("id = %d, want %d(동일 해시는 같은 open 행)", id2, id)
	}
	if res != "noop" {
		t.Fatalf("res = %q, want noop(완전 동일)", res)
	}
	id3, res, err := st.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "fix path", "detail": "d2",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id3 != id {
		t.Fatalf("id = %d, want %d", id3, id)
	}
	if res != "updated" {
		t.Fatalf("res = %q, want updated(detail 변경)", res)
	}
	_, res, err = st.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "fix path"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res != "updated" {
		t.Fatalf("res = %q, want updated(detail 누락은 빈 문자열과 비교)", res)
	}
	idOther, res, err := st.OpenWorkState(ctx, map[string]any{"project_id": 2, "task": "fix path"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if idOther == id {
		t.Fatalf("id = %d, want %d와 다름(프로젝트 분리)", idOther, id)
	}
	if res != "open" {
		t.Fatalf("res = %q, want open", res)
	}
	if _, _, err := st.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "  "}); err == nil {
		t.Fatalf("err = nil, want 빈 정규화 task 오류")
	}
	if err := st.ResolveWorkState(ctx, idOther, 0); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	open := st.ListOpenWorkState(ctx, 2, 10)
	if len(open) != 0 {
		t.Fatalf("len = %d, want 0(resolve 후 목록 제외)", len(open))
	}
	idL, _, err := st.OpenWorkState(ctx, map[string]any{
		"project_id": 2, "task": "session chain task", "session_label": "s-scen",
	})
	if err != nil || idL <= 0 {
		t.Fatalf("id = %d, err = %v, want open/nil", idL, err)
	}
	if got, err := st.LatestProjectForSession(ctx, "s-scen"); err != nil || got != 2 {
		t.Fatalf("got = %d, err = %v, want 2/nil(세션 사슬 해석)", got, err)
	}
	if got, err := st.LatestProjectForSession(ctx, ""); err != nil || got != 0 {
		t.Fatalf("got = %d, err = %v, want 0/nil(빈 라벨)", got, err)
	}
	if got, err := st.LatestProjectForSession(ctx, "s-unknown"); err != nil || got != 0 {
		t.Fatalf("got = %d, err = %v, want 0/nil(미매칭 라벨)", got, err)
	}
}

func TestOpenWorkStateNormalizesHash(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	id1, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "  Fix: PATH! ", "detail": "d",
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	id2, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "fix path", "detail": "d",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id2 != id1 {
		t.Fatalf("id = %d, want %d(정규화 후 동일 해시)", id2, id1)
	}
	if res != "noop" {
		t.Fatalf("res = %q, want noop", res)
	}
	id3, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "ＦＩＸ　ＰＡＴＨ！", "detail": "d",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id3 != id1 {
		t.Fatalf("id = %d, want %d(NFKC 접기 동일 해시)", id3, id1)
	}
	if res != "noop" {
		t.Fatalf("res = %q, want noop", res)
	}
}

func TestOpenWorkStateQueryIndexBranches(t *testing.T) {
	sqlNil, argsNil := db.OpenWorkStateQuery(nil, "hash1")
	if !strings.Contains(sqlNil, "project_id IS NULL") || strings.Contains(sqlNil, "IS NOT DISTINCT FROM") {
		t.Fatalf("sql = %q, want IS NULL 분기(btree 인덱스 매칭)", sqlNil)
	}
	if len(argsNil) != 1 || argsNil[0] != "hash1" {
		t.Fatalf("args = %v, want hash 1개", argsNil)
	}
	pid := 3
	sqlP, argsP := db.OpenWorkStateQuery(&pid, "hash2")
	if !strings.Contains(sqlP, "project_id = $1") || strings.Contains(sqlP, "IS NOT DISTINCT FROM") {
		t.Fatalf("sql = %q, want 등가 일치 분기(btree 인덱스 매칭)", sqlP)
	}
	if len(argsP) != 2 || argsP[0] != 3 || argsP[1] != "hash2" {
		t.Fatalf("args = %v, want [3 hash2]", argsP)
	}
}

func TestOpenWorkStateNewTaskOpens(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	id1, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "  Fix: PATH! ", "detail": "d",
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	id2, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "fix path",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id2 != id1 {
		t.Fatalf("id = %d, want %d", id2, id1)
	}
	if res != "updated" {
		t.Fatalf("res = %q, want updated", res)
	}
	id3, res, err := f.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "deploy worker",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id3 == id1 {
		t.Fatalf("id = %d, want 신규(다른 task)", id3)
	}
	if res != "open" {
		t.Fatalf("res = %q, want open", res)
	}
}

func TestResolveWorkStateNullableBy(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	id, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "task a"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if err := f.ResolveWorkState(ctx, id, 0); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	row := f.states[0]
	if row.status != "resolved" {
		t.Fatalf("status = %q, want resolved", row.status)
	}
	if row.resolvedAt.IsZero() {
		t.Fatalf("resolved_at 미기록 — 보존 기반 정리·감사 불가")
	}
	if row.byKnowledgeID != nil {
		t.Fatalf("resolved_by = %v, want NULL", *row.byKnowledgeID)
	}
	id2, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "task b"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if err := f.ResolveWorkState(ctx, id2, 42); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if f.states[1].byKnowledgeID == nil || *f.states[1].byKnowledgeID != 42 {
		t.Fatalf("resolved_by = %v, want 42", f.states[1].byKnowledgeID)
	}
	if f.states[1].resolvedAt.IsZero() {
		t.Fatalf("resolved_at 미기록 — 보존 기반 정리·감사 불가")
	}
	if err := f.ResolveWorkState(ctx, 9999, 0); err == nil {
		t.Fatalf("err = nil, want 미존재 id 오류")
	}
}

func TestListOpenOrderAndProjectFilter(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	idOld, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "older task"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	idNew, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "newer task"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	_, _, err = f.OpenWorkState(ctx, map[string]any{"project_id": 2, "task": "other project"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	idResolved, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "resolved task"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	for _, s := range f.states {
		if s.id == idOld {
			s.updatedAt = time.Now().Add(-time.Hour)
		}
	}
	if err := f.ResolveWorkState(ctx, idResolved, 7); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	rows := f.ListOpenWorkState(ctx, 1, 10)
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2(resolved·타 프로젝트 제외)", len(rows))
	}
	if rows[0]["id"] != idNew || rows[1]["id"] != idOld {
		t.Fatalf("ids = %v, want [%d %d](updated_at 최신순)", []any{rows[0]["id"], rows[1]["id"]}, idNew, idOld)
	}
	if rows[0]["task"] != "newer task" {
		t.Fatalf("task = %v, want 정규화 저장된 task", rows[0]["task"])
	}
	rows = f.ListOpenWorkState(ctx, 1, 1)
	if len(rows) != 1 || rows[0]["id"] != idNew {
		t.Fatalf("rows = %v, want limit 1 적용 최신 1건", rows)
	}
	rows = f.ListOpenWorkState(ctx, 2, 10)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1(프로젝트 필터)", len(rows))
	}
}

func TestStaleOpenWorkStatesAllProjects(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	idStale, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "stale task"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	_, _, err = f.OpenWorkState(ctx, map[string]any{"project_id": 2, "task": "fresh task"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	idResolved, _, err := f.OpenWorkState(ctx, map[string]any{"project_id": 1, "task": "resolved stale"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	for _, s := range f.states {
		if s.id == idStale || s.id == idResolved {
			s.updatedAt = time.Now().AddDate(0, 0, -5)
		}
	}
	if err := f.ResolveWorkState(ctx, idResolved, 0); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	rows := f.StaleOpenWorkStates(ctx, 3)
	if len(rows) != 1 || rows[0]["id"] != idStale {
		t.Fatalf("rows = %v, want 5일 경과 open 1건(resolve 제외)", rows)
	}
	rows = f.StaleOpenWorkStates(ctx, 30)
	if len(rows) != 0 {
		t.Fatalf("len = %d, want 0", len(rows))
	}
}

func TestOpenWorkStateWithoutProjectNullMembership(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	id, res, err := f.OpenWorkState(ctx, map[string]any{"task": "t"})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	id2, res2, err := f.OpenWorkState(ctx, map[string]any{"task": "T!"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id2 != id || res2 != "noop" {
		t.Fatalf("id = %d, res = %q, want %d/noop(NULL 프로젝트 동등 dedup)", id2, res2, id)
	}
	rows := f.ListOpenWorkState(ctx, 0, 10)
	if len(rows) != 1 || rows[0]["id"] != id {
		t.Fatalf("rows = %v, want NULL 소속 포함 open 1건(0=무필터)", rows)
	}
	if p, ok := rows[0]["project_id"].(*int); !ok || p != nil {
		t.Fatalf("project_id = %v, want NULL", rows[0]["project_id"])
	}
}

func captureStderrWorkState(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = orig
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpenWorkStateSanitizesAndWarnsSecrets(t *testing.T) {
	f := newWorkStateFake()
	ctx := context.Background()
	detail := "메모 ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234\x07 꼬리"
	var id int
	var res string
	var err error
	stderr := captureStderrWorkState(t, func() {
		id, res, err = f.OpenWorkState(ctx, map[string]any{
			"project_id": 1, "task": "secret check", "detail": detail,
		})
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id <= 0 || res != "open" {
		t.Fatalf("id = %d, res = %q, want open", id, res)
	}
	if !strings.Contains(stderr, "possible secret: github-token") {
		t.Fatalf("stderr = %q, want possible secret: github-token 경고", stderr)
	}
	want := store.SanitizeText(detail)
	rows := f.ListOpenWorkState(ctx, 1, 10)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if got, _ := rows[0]["detail"].(string); got != want {
		t.Fatalf("detail = %q, want 살균 저장 %q", got, want)
	}
	if got, _ := rows[0]["task"].(string); got != "secret check" {
		t.Fatalf("task = %q, want 정규화 task", got)
	}
}

func TestWorkStateFakeRealHomologous(t *testing.T) {
	assertWorkStateScenario(t, newWorkStateFake())
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return
	}
	pool, err := db.Connect(context.Background(), testengine.ResolveDSN())
	if err != nil {
		t.Fatalf("SUPERLITE_REAL=1은 실 store 보장 전제 — 연결 실패: %v", err)
	}
	defer pool.Close()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'work_state')`,
	).Scan(&exists); err != nil {
		t.Fatalf("work_state 존재 조회 실패: %v", err)
	}
	if !exists {
		t.Fatalf("work_state 스키마 미정착(T1a/T9) — SUPERLITE_REAL 시름은 skip 회피 금지")
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM work_state`); err != nil {
		t.Fatalf("work_state 정리 실패: %v", err)
	}
	r, err := db.NewRepo(context.Background(), testengine.ResolveDSN())
	if err != nil {
		t.Fatalf("real repo 생성 실패: %v", err)
	}
	defer r.Close()
	assertWorkStateScenario(t, r)
}
