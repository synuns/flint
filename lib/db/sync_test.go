// sync_test.go — sync remote-registration (dolt_remote wrapping)
// tests. Fast covers the SQL·error contracts with a fake Querier;
// slow (FLINT_TEST_DSN) measures dolt_remote add/list/remove on an
// isolated Doltgres (a file:// remote). The remote url raw value is
// never exposed on any path — masking has the single path
// MaskRemoteURL.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeSyncQuerier — a Querier fake. Only queries where sqlMatch returns
// true return rows; everything else (nil, nil). Records every executed
// query·argument.
type fakeSyncQuerier struct {
	rows     [][]any
	err      error
	sqlMatch func(sql string) bool
	queries  []string
	allArgs  [][]any
}

func (f *fakeSyncQuerier) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	cp := make([]any, len(args))
	copy(cp, args)
	f.allArgs = append(f.allArgs, cp)
	if f.err != nil {
		return nil, f.err
	}
	if f.sqlMatch != nil && f.sqlMatch(sql) {
		return f.rows, nil
	}
	return nil, nil
}

// TestMaskRemoteURL — the masking contract: exposes scheme+host only
// (credentials·port·path·query hidden). file:// (no host) yields the
// scheme only. Unparsable·scheme-less raw values are entirely hidden (a
// fixed token).
func TestMaskRemoteURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"https 자격증명+경로", "https://user:s3cret@rem-example.invalid/org/repo", "https://rem-example.invalid"},
		{"postgres 포트+경로", "postgres://flint:pw@db.example.invalid:5433/flint", "postgres://db.example.invalid"},
		{"file 원격(호스트 없음)", "file:///tmp/flint-remote-demo", "file://"},
		{"http+쿼리", "http://host.example/path?token=abc", "http://host.example"},
		{"스킴 부재 원문 미노출", "doltremoteapi.dolthub.com/org/repo", "(unparsable)"},
		{"빈 값", "", "(unparsable)"},
		{"제어문자 파손", "https://h\nost.example/x", "(unparsable)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskRemoteURL(tc.in)
			if got != tc.want {
				t.Fatalf("MaskRemoteURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.want == "(unparsable)" && got == tc.in {
				t.Fatalf("파손 url 원문이 그대로 노출됨: %q", got)
			}
		})
	}
}

// TestRemoteAddDuplicateRejected — a duplicate name yields
// ErrRemoteExists without running dolt_remote('add') (measured:
// dolt_remote quietly overwrites duplicates — pre-lookup blocking is
// the contract).
func TestRemoteAddDuplicateRejected(t *testing.T) {
	f := &fakeSyncQuerier{
		rows:     [][]any{{1}},
		sqlMatch: func(sql string) bool { return strings.Contains(sql, "FROM dolt_remotes") },
	}
	err := RemoteAdd(context.Background(), f, "origin", "file:///tmp/r")
	if !errors.Is(err, ErrRemoteExists) {
		t.Fatalf("err = %v, want ErrRemoteExists", err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_remote(") {
			t.Fatalf("중복 판정 시 dolt_remote 실행됨: %s", q)
		}
	}
	if strings.Contains(err.Error(), "file:///tmp/r") {
		t.Fatalf("오류 메시지에 원격 url 원문 노출: %v", err)
	}
}

// TestRemoteAddRunsDoltRemote — a new name runs dolt_remote('add',
// name, url) with the arguments in order.
func TestRemoteAddRunsDoltRemote(t *testing.T) {
	f := &fakeSyncQuerier{
		rows:     [][]any{{0}},
		sqlMatch: func(sql string) bool { return strings.Contains(sql, "dolt_remote(") },
	}
	if err := RemoteAdd(context.Background(), f, "origin", "file:///tmp/r"); err != nil {
		t.Fatalf("RemoteAdd err = %v", err)
	}
	if len(f.queries) != 2 {
		t.Fatalf("쿼리 2회(중복조회+add) 기대, got %d: %v", len(f.queries), f.queries)
	}
	add := f.queries[1]
	if !strings.Contains(add, "dolt_remote('add', $1, $2)") {
		t.Fatalf("add 쿼리 형식 불일치: %s", add)
	}
	if len(f.allArgs[1]) != 2 || f.allArgs[1][0] != "origin" || f.allArgs[1][1] != "file:///tmp/r" {
		t.Fatalf("add 인자 = %v, want [origin file:///tmp/r]", f.allArgs[1])
	}
}

// TestRemoteListParsesRows — parses dolt_remotes rows into Remotes
// (sorting belongs to the SQL ORDER BY — no Go re-sorting; see the
// query verification below).
func TestRemoteListParsesRows(t *testing.T) {
	f := &fakeSyncQuerier{
		rows: [][]any{
			{"alpha", "file:///tmp/r"},
			{"zeta", "https://h2.example/org/r"},
		},
		sqlMatch: func(sql string) bool { return strings.Contains(sql, "FROM dolt_remotes") },
	}
	got, err := RemoteList(context.Background(), f)
	if err != nil {
		t.Fatalf("RemoteList err = %v", err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("RemoteList 파싱 실패: %+v", got)
	}
	if got[1].URL != "https://h2.example/org/r" {
		t.Fatalf("URL 파싱 실패: %+v", got[1])
	}
}

// TestRemoteListRequiresDoltRemotes — the list takes the dolt_remotes
// system-table query path·name-ordered sorting (measured: calling
// dolt_remote() with no arguments errors).
func TestRemoteListRequiresDoltRemotes(t *testing.T) {
	f := &fakeSyncQuerier{}
	if _, err := RemoteList(context.Background(), f); err != nil {
		t.Fatalf("RemoteList err = %v", err)
	}
	if len(f.queries) != 1 || !strings.Contains(f.queries[0], "FROM dolt_remotes") {
		t.Fatalf("목록 쿼리가 dolt_remotes 조회가 아님: %v", f.queries)
	}
	if !strings.Contains(f.queries[0], "ORDER BY name") {
		t.Fatalf("목록 쿼리에 이름순 정렬 부재: %s", f.queries[0])
	}
}

// TestRemoteRemoveMissingRejected — a missing name yields
// ErrRemoteNotFound without running dolt_remote('remove') (no url raw
// value in the error).
func TestRemoteRemoveMissingRejected(t *testing.T) {
	f := &fakeSyncQuerier{ // existence check 0 rows → with sqlMatch nil, (nil,nil)
		err: nil,
	}
	err := RemoteRemove(context.Background(), f, "ghost")
	if !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("err = %v, want ErrRemoteNotFound", err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_remote(") {
			t.Fatalf("미존재 판정 시 dolt_remote 실행됨: %s", q)
		}
	}
}

// TestRemoteRemoveRunsDoltRemote — an existing name runs
// dolt_remote('remove', name).
func TestRemoteRemoveRunsDoltRemote(t *testing.T) {
	f := &fakeSyncQuerier{
		rows:     [][]any{{1}}, // existence-check hit
		sqlMatch: func(sql string) bool { return true },
	}
	if err := RemoteRemove(context.Background(), f, "origin"); err != nil {
		t.Fatalf("RemoteRemove err = %v", err)
	}
	if len(f.queries) != 2 || !strings.Contains(f.queries[1], "dolt_remote('remove', $1)") {
		t.Fatalf("remove 쿼리 경로 불일치: %v", f.queries)
	}
	if len(f.allArgs[1]) != 1 || f.allArgs[1][0] != "origin" {
		t.Fatalf("remove 인자 = %v, want [origin]", f.allArgs[1])
	}
}

// TestSyncRemoteRoundTripRealSlow — slow (FLINT_TEST_DSN): measures
// real file:// remote registration·listing·removal·duplicate/missing
// errors on an isolated Doltgres.
func TestSyncRemoteRoundTripRealSlow(t *testing.T) {
	dsn := testDSN(t)
	pool, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()
	q := poolQuerier{pool}

	name := "t4-probe"
	_ = RemoteRemove(context.Background(), q, name)
	t.Cleanup(func() { _ = RemoteRemove(context.Background(), q, name) })

	remoteDir := filepath.Join(t.TempDir(), "t4-remote")
	rawURL := "file://" + remoteDir

	// 1) New registration → actually registered in dolt_remotes.
	if err := RemoteAdd(context.Background(), q, name, rawURL); err != nil {
		t.Fatalf("RemoteAdd: %v", err)
	}
	// 2) List — the raw url retrieved (masking is the CLI output
	// contract; here the source-preservation check).
	remotes, err := RemoteList(context.Background(), q)
	if err != nil {
		t.Fatalf("RemoteList: %v", err)
	}
	found := false
	for _, r := range remotes {
		if r.Name == name {
			found = true
			if !strings.HasPrefix(r.URL, "file://") {
				t.Fatalf("등록된 url 원문 불일치: %q", r.URL)
			}
		}
	}
	if !found {
		t.Fatalf("dolt_remotes에 %q 미등록: %+v", name, remotes)
	}
	// 3) Duplicate registration blocked (measured: dolt_remote quietly
	// overwrites — the pre-lookup contract).
	if err := RemoteAdd(context.Background(), q, name, rawURL); !errors.Is(err, ErrRemoteExists) {
		t.Fatalf("중복 add err = %v, want ErrRemoteExists", err)
	}
	// 4) Remove → missing error.
	if err := RemoteRemove(context.Background(), q, name); err != nil {
		t.Fatalf("RemoteRemove: %v", err)
	}
	if err := RemoteRemove(context.Background(), q, name); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("미존재 remove err = %v, want ErrRemoteNotFound", err)
	}
}

// fakeSyncScriptQuerier — the Querier fake for push/pull: a stub
// function per query (branching the pre-lookup·push·fetch·merge·head
// hash by SQL substring). Records every executed query·argument.
// v1.7.2 T3 (S3): also records whether a deadline was set (for the
// ctx.WithTimeout application assertion).
type fakeSyncScriptQuerier struct {
	fn        func(sql string, args []any) ([][]any, error)
	queries   []string
	allArgs   [][]any
	deadlines []time.Time // the deadline times of calls whose ctx.Deadline() is set
}

func (f *fakeSyncScriptQuerier) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if dl, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, dl)
	}
	cp := make([]any, len(args))
	copy(cp, args)
	f.allArgs = append(f.allArgs, cp)
	return f.fn(sql, args)
}

// newSyncScript — the standard scripted fake: remote lookup (name→url)·
// a head-hash queue (popped in call order)·push/fetch/merge results·
// error injection. mergeErr·fetchErr·pushErr are the respective query
// errors; heads is the dolt_branches query-result queue.
func newSyncScript(remotes map[string]string, heads []string, pushErr, fetchErr, mergeErr error) *fakeSyncScriptQuerier {
	return newSyncScriptRows(remotes, heads, [][]any{{"(0,\"pushed\")"}}, pushErr, fetchErr, mergeErr)
}

// newSyncScriptRows — the newSyncScript variant specifying the push
// result rows (the v1.7.2 T3 S6 no-op push verdict tests). Includes a
// dolt_reset stub (S2).
func newSyncScriptRows(remotes map[string]string, heads []string, pushRows [][]any, pushErr, fetchErr, mergeErr error) *fakeSyncScriptQuerier {
	f := &fakeSyncScriptQuerier{}
	headIdx := 0
	f.fn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM dolt_remotes WHERE name"):
			if u, ok := remotes[strAny(args[0])]; ok {
				return [][]any{{strAny(args[0]), u}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "FROM dolt_branches"):
			if headIdx < len(heads) {
				h := heads[headIdx]
				headIdx++
				return [][]any{{h}}, nil
			}
			return [][]any{{"h-final"}}, nil
		case strings.Contains(sql, "dolt_push("):
			if pushErr != nil {
				return nil, pushErr
			}
			return pushRows, nil
		case strings.Contains(sql, "dolt_fetch("):
			if fetchErr != nil {
				return nil, fetchErr
			}
			return [][]any{{0}}, nil
		case strings.Contains(sql, "dolt_merge("):
			if mergeErr != nil {
				return nil, mergeErr
			}
			return [][]any{{"(\"h\",0,1,\"merge successful\")"}}, nil
		case strings.Contains(sql, "dolt_reset("):
			return [][]any{{0}}, nil
		}
		return nil, nil
	}
	return f
}

func strAny(v any) string { s, _ := v.(string); return s }

// TestSyncPushRemoteNotFound — an unregistered push target yields
// ErrRemoteNotFound without running dolt_push (the pre-lookup contract
// — convention inherited).
func TestSyncPushRemoteNotFound(t *testing.T) {
	f := newSyncScript(map[string]string{}, []string{"h0"}, nil, nil, nil)
	_, err := SyncPush(context.Background(), f, "ghost")
	if !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("err = %v, want ErrRemoteNotFound", err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_push(") {
			t.Fatalf("미등록 판정 시 dolt_push 실행됨: %s", q)
		}
	}
}

// TestSyncPushRunsAndReportsHash — a registered remote runs
// dolt_push(remote,'main') and reports the head hash as the result (the
// branch-'main'-fixed contract).
func TestSyncPushRunsAndReportsHash(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"hash-1"}, nil, nil, nil)
	res, err := SyncPush(context.Background(), f, "origin")
	if err != nil {
		t.Fatalf("SyncPush err = %v", err)
	}
	if res.Status != "pushed" || res.Hash != "hash-1" {
		t.Fatalf("result = %+v, want {pushed hash-1}", res)
	}
	pushed := false
	for i, q := range f.queries {
		if strings.Contains(q, "dolt_push(") {
			pushed = true
			if !strings.Contains(q, "'main'") {
				t.Fatalf("push 쿼리에 main 브랜치 부재: %s", q)
			}
			if len(f.allArgs[i]) != 1 || f.allArgs[i][0] != "origin" {
				t.Fatalf("push 인자 = %v, want [origin]", f.allArgs[i])
			}
		}
	}
	if !pushed {
		t.Fatalf("dolt_push 미실행: %v", f.queries)
	}
}

// TestSyncPushErrorMasksURL — when a push execution error message
// carries the remote url raw value, it is replaced with the masked form
// (an unreachable error).
func TestSyncPushErrorMasksURL(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/secret-remote"}, nil,
		errors.New("dial: push to file:///tmp/secret-remote refused"), nil, nil)
	_, err := SyncPush(context.Background(), f, "origin")
	if err == nil {
		t.Fatal("오류 기대")
	}
	if strings.Contains(err.Error(), "/tmp/secret-remote") {
		t.Fatalf("오류에 url 원문 노출: %v", err)
	}
	if !strings.Contains(err.Error(), "file://") {
		t.Fatalf("마스킹된 스킴도 미노출(원인 보존 위반): %v", err)
	}
}

// TestSyncPullFetchMergeOrder — pull runs remote lookup→fetch→head→
// merge in order, the merge argument is <remote>/main, and a hash
// change yields status merged.
func TestSyncPullFetchMergeOrder(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h-old", "h-new"}, nil, nil, nil)
	res, err := SyncPull(context.Background(), f, "origin", false)
	if err != nil {
		t.Fatalf("SyncPull err = %v", err)
	}
	if res.Status != "merged" || res.Hash != "h-new" {
		t.Fatalf("result = %+v, want {merged h-new}", res)
	}
	var fetchIdx, mergeIdx = -1, -1
	for i, q := range f.queries {
		if strings.Contains(q, "dolt_fetch(") {
			fetchIdx = i
			if len(f.allArgs[i]) != 2 || f.allArgs[i][0] != "origin" || f.allArgs[i][1] != "main" {
				t.Fatalf("fetch 인자 = %v, want [origin main]", f.allArgs[i])
			}
		}
		if strings.Contains(q, "dolt_merge(") {
			mergeIdx = i
			if len(f.allArgs[i]) != 1 || f.allArgs[i][0] != "origin/main" {
				t.Fatalf("merge 인자 = %v, want [origin/main]", f.allArgs[i])
			}
		}
	}
	if fetchIdx < 0 || mergeIdx < 0 || fetchIdx > mergeIdx {
		t.Fatalf("fetch→merge 순서 위반: %v", f.queries)
	}
}

// TestSyncPullUpToDate — an unchanged hash after merge yields status
// up_to_date.
func TestSyncPullUpToDate(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h-same", "h-same"}, nil, nil, nil)
	res, err := SyncPull(context.Background(), f, "origin", false)
	if err != nil {
		t.Fatalf("SyncPull err = %v", err)
	}
	if res.Status != "up_to_date" || res.Hash != "h-same" {
		t.Fatalf("result = %+v, want {up_to_date h-same}", res)
	}
}

// TestSyncPullDryFetchOnly — dry stops at fetch: merge not run·nothing
// applied.
func TestSyncPullDryFetchOnly(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h-local"}, nil, nil, nil)
	res, err := SyncPull(context.Background(), f, "origin", true)
	if err != nil {
		t.Fatalf("SyncPull dry err = %v", err)
	}
	if res.Status != "fetched" || res.Hash != "h-local" {
		t.Fatalf("result = %+v, want {fetched h-local}", res)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_merge(") {
			t.Fatalf("dry에서 merge 실행됨: %s", q)
		}
	}
}

// TestSyncPullRemoteNotFound — an unregistered pull target also yields
// ErrRemoteNotFound via the pre-lookup.
func TestSyncPullRemoteNotFound(t *testing.T) {
	f := newSyncScript(map[string]string{}, []string{"h0"}, nil, nil, nil)
	_, err := SyncPull(context.Background(), f, "ghost", false)
	if !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("err = %v, want ErrRemoteNotFound", err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_fetch(") {
			t.Fatalf("미등록 판정 시 dolt_fetch 실행됨: %s", q)
		}
	}
}

// TestSyncPullConflictGuidance — a merge conflict yields
// ErrMergeConflict + the manual-resolution guidance (no automatic
// resolution). Judged by a copy of the measured error wording.
func TestSyncPullConflictGuidance(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h0", "h0"}, nil, nil,
		errors.New("Merge conflict detected, @autocommit transaction rolled back"))
	_, err := SyncPull(context.Background(), f, "origin", false)
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("err = %v, want ErrMergeConflict", err)
	}
	if !strings.Contains(err.Error(), "manually") {
		t.Fatalf("수동 해결 안내 부재: %v", err)
	}
}

// TestSyncPushUpToDateReportsNoOp — v1.7.2 T3 S6: a no-op push
// (dolt_push result rows "Everything up-to-date") reports status
// up_to_date. The result rows are used for the verdict only and never
// carried to any output (an internal-handling contract).
func TestSyncPushUpToDateReportsNoOp(t *testing.T) {
	f := newSyncScriptRows(map[string]string{"origin": "file:///tmp/rr"}, []string{"hash-1"},
		[][]any{{0, "Everything up-to-date"}}, nil, nil, nil)
	res, err := SyncPush(context.Background(), f, "origin")
	if err != nil {
		t.Fatalf("SyncPush err = %v", err)
	}
	if res.Status != "up_to_date" || res.Hash != "hash-1" {
		t.Fatalf("result = %+v, want {up_to_date hash-1}", res)
	}
}

// TestSyncPushDivergedGuide — v1.7.2 T3 S6: a diverged push (a rejected
// error) yields ErrPushDiverged + the 'flint sync pull' first guidance
// (not dolt pull — a wording contract).
func TestSyncPushDivergedGuide(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h0"},
		errors.New("error: failed to push some refs: updates were rejected because the tip of 'main' is behind"), nil, nil)
	_, err := SyncPush(context.Background(), f, "origin")
	if !errors.Is(err, ErrPushDiverged) {
		t.Fatalf("err = %v, want ErrPushDiverged", err)
	}
	if !strings.Contains(err.Error(), "flint sync pull") {
		t.Fatalf("분기 push 안내에 'flint sync pull' 부재: %v", err)
	}
}

// TestSyncPushTimeoutBudget — v1.7.2 T3 S3: the 30s cap deadline
// propagates to all of SyncPush's queries (ctx.WithTimeout — the
// remote-network cap).
func TestSyncPushTimeoutBudget(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h0"}, nil, nil, nil)
	start := time.Now()
	if _, err := SyncPush(context.Background(), f, "origin"); err != nil {
		t.Fatalf("SyncPush err = %v", err)
	}
	assertTimeoutBudget(t, f, start)
}

// TestSyncPullTimeoutBudget — v1.7.2 T3 S3: the same for SyncPull (the
// 30s cap propagates).
func TestSyncPullTimeoutBudget(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h0", "h1"}, nil, nil, nil)
	start := time.Now()
	if _, err := SyncPull(context.Background(), f, "origin", false); err != nil {
		t.Fatalf("SyncPull err = %v", err)
	}
	assertTimeoutBudget(t, f, start)
}

// assertTimeoutBudget — whether the fake-recorded deadlines are (a) set
// on every query and (b) within 30s of the call time (the remote-cap
// contract).
func assertTimeoutBudget(t *testing.T, f *fakeSyncScriptQuerier, start time.Time) {
	t.Helper()
	if len(f.deadlines) != len(f.queries) {
		t.Fatalf("deadline 미설정 쿼리 존재: %d/%d", len(f.deadlines), len(f.queries))
	}
	for _, dl := range f.deadlines {
		rem := dl.Sub(start)
		// A cap accounting for the minute delay between the start-time
		// measurement and the deadline setting (a 30s + 0.5s window — a
		// cap growing unbounded still fails).
		if rem <= 0 || rem > 30*time.Second+500*time.Millisecond {
			t.Fatalf("deadline 상한 위반(30s 기대): %v", rem)
		}
	}
}

// TestSyncPullPanicGuidance — v1.7.2 T3 S1: when a fetch error contains
// "DoltgresHandler caught panic", ErrRemotePanic + the dedicated
// guidance (concurrent multi-device use unsupported·single active
// device·reset --to-remote recovery).
func TestSyncPullPanicGuidance(t *testing.T) {
	for name, inject := range map[string]struct{ fetchErr, mergeErr error }{
		"fetch 경로": {errors.New("DoltgresHandler caught panic: runtime error"), nil},
		"merge 경로": {nil, errors.New("DoltgresHandler caught panic: runtime error")},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h0", "h0"},
				nil, inject.fetchErr, inject.mergeErr)
			_, err := SyncPull(context.Background(), f, "origin", false)
			if !errors.Is(err, ErrRemotePanic) {
				t.Fatalf("err = %v, want ErrRemotePanic", err)
			}
			for _, want := range []string{"multi-device", "single active device", "reset --to-remote"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("패닉 안내에 %q 부재: %v", want, err)
				}
			}
		})
	}
}

// TestSyncHeadMissingWording — v1.7.2 T3 S6: the missing-main error
// wording — guides with both the cause and the action (the 'main'
// branch required).
func TestSyncHeadMissingWording(t *testing.T) {
	f := &fakeSyncScriptQuerier{fn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM dolt_remotes WHERE name"):
			return [][]any{{"origin", "file:///tmp/rr"}}, nil
		case strings.Contains(sql, "dolt_push("):
			return [][]any{{0, "pushed"}}, nil
		case strings.Contains(sql, "FROM dolt_branches"):
			return nil, nil // no main branch
		}
		return nil, nil
	}}
	_, err := SyncPush(context.Background(), f, "origin")
	if err == nil {
		t.Fatal("오류 기대")
	}
	if !strings.Contains(err.Error(), "main branch not found") || !strings.Contains(err.Error(), "flint") {
		t.Fatalf("main 부재 문구 계약 위반: %v", err)
	}
}

// TestSyncResetRemoteNotFound — v1.7.2 T3 S2: an unregistered reset
// target yields ErrRemoteNotFound without running fetch·reset
// (inheriting the pre-lookup contract).
func TestSyncResetRemoteNotFound(t *testing.T) {
	f := newSyncScript(map[string]string{}, []string{"h0"}, nil, nil, nil)
	_, err := SyncReset(context.Background(), f, "ghost")
	if !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("err = %v, want ErrRemoteNotFound", err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_fetch(") || strings.Contains(q, "dolt_reset(") {
			t.Fatalf("미등록 판정 시 fetch/reset 실행됨: %s", q)
		}
	}
}

// TestSyncResetRunsFetchThenHardReset — v1.7.2 T3 S2: a registered
// remote runs fetch(remote,'main') → dolt_reset('--hard', <remote>/
// main) in order, with result status "reset" + the post-reset head
// hash. The 30s cap also applies to reset (S3).
func TestSyncResetRunsFetchThenHardReset(t *testing.T) {
	f := newSyncScript(map[string]string{"origin": "file:///tmp/rr"}, []string{"h-remote"}, nil, nil, nil)
	start := time.Now()
	res, err := SyncReset(context.Background(), f, "origin")
	if err != nil {
		t.Fatalf("SyncReset err = %v", err)
	}
	if res.Status != "reset" || res.Hash != "h-remote" {
		t.Fatalf("result = %+v, want {reset h-remote}", res)
	}
	var fetchIdx, resetIdx = -1, -1
	for i, q := range f.queries {
		if strings.Contains(q, "dolt_fetch(") {
			fetchIdx = i
			if len(f.allArgs[i]) != 2 || f.allArgs[i][0] != "origin" || f.allArgs[i][1] != "main" {
				t.Fatalf("reset fetch 인자 = %v, want [origin main]", f.allArgs[i])
			}
		}
		if strings.Contains(q, "dolt_reset(") {
			resetIdx = i
			if len(f.allArgs[i]) != 1 || f.allArgs[i][0] != "origin/main" {
				t.Fatalf("reset 인자 = %v, want [origin/main]", f.allArgs[i])
			}
		}
	}
	if fetchIdx < 0 || resetIdx < 0 || fetchIdx > resetIdx {
		t.Fatalf("fetch→reset 순서 위반: %v", f.queries)
	}
	assertTimeoutBudget(t, f, start)
}

// TestSyncPushPullRoundTripRealSlow — slow (FLINT_TEST_DSN): a round
// trip measured on two isolated Doltgres databases (a file:// remote) —
// A push → B (dolt_clone) pull → read verification on B·the conflict
// scenario (both sides same id, different content) → ErrMergeConflict·
// dry stops at fetch·B's working set preserved after the conflict.
func TestSyncPushPullRoundTripRealSlow(t *testing.T) {
	ctx := context.Background()
	dsn := testDSN(t)
	base, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer base.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	aName := pgx.Identifier{"t5_a_" + suffix}.Sanitize()
	bName := pgx.Identifier{"t5_b_" + suffix}.Sanitize()
	t.Cleanup(func() {
		_, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+aName)
		_, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+bName)
	})
	if _, err := base.Exec(ctx, "CREATE DATABASE "+aName); err != nil {
		t.Fatalf("CREATE DATABASE A: %v", err)
	}

	rawURL := "file://" + filepath.Join(t.TempDir(), "remote")

	// A: remote registration·synthetic seed commit·push.
	poolA, err := Connect(ctx, dsnForDatabase(t, dsn, aName))
	if err != nil {
		t.Fatalf("Connect A: %v", err)
	}
	defer poolA.Close()
	qa := poolQuerier{poolA}
	if err := RemoteAdd(ctx, qa, "origin", rawURL); err != nil {
		t.Fatalf("RemoteAdd: %v", err)
	}
	seedA := []string{
		`CREATE TABLE t5_note (id text PRIMARY KEY, body text)`,
		`INSERT INTO t5_note VALUES ('n1','alpha')`,
	}
	for _, q := range seedA {
		if _, err := qa.Query(ctx, q); err != nil {
			t.Fatalf("seed A %q: %v", q, err)
		}
	}
	commitA := func(msg string) {
		t.Helper()
		if _, err := qa.Query(ctx, `SELECT dolt_add('-A')`); err != nil {
			t.Fatalf("dolt_add: %v", err)
		}
		if _, err := qa.Query(ctx, `SELECT dolt_commit('-m', $1)`, msg); err != nil {
			t.Fatalf("dolt_commit: %v", err)
		}
	}
	commitA("seed n1")
	res, err := SyncPush(ctx, qa, "origin")
	if err != nil {
		t.Fatalf("SyncPush: %v", err)
	}
	if res.Status != "pushed" || res.Hash == "" {
		t.Fatalf("push 결과 = %+v", res)
	}

	// B: created from A's remote via dolt_clone (the device-move
	// scenario).
	if _, err := base.Exec(ctx, "SELECT dolt_clone($1, $2)", rawURL, strings.Trim(bName, `"`)); err != nil {
		t.Fatalf("dolt_clone: %v", err)
	}
	poolB, err := Connect(ctx, dsnForDatabase(t, dsn, bName))
	if err != nil {
		t.Fatalf("Connect B: %v", err)
	}
	defer poolB.Close()
	qb := poolQuerier{poolB}

	// A: an additional commit push → B pull (merged) → read verification
	// on B.
	if _, err := qa.Query(ctx, `UPDATE t5_note SET body='beta' WHERE id='n1'`); err != nil {
		t.Fatalf("A update: %v", err)
	}
	commitA("A edits n1")
	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("SyncPush 2: %v", err)
	}
	res, err = SyncPull(ctx, qb, "origin", false)
	if err != nil {
		t.Fatalf("SyncPull: %v", err)
	}
	if res.Status != "merged" {
		t.Fatalf("pull status = %q, want merged", res.Status)
	}
	rows, err := qb.Query(ctx, `SELECT body FROM t5_note WHERE id='n1'`)
	if err != nil || len(rows) != 1 || strAny(rows[0][0]) != "beta" {
		t.Fatalf("B read after pull: rows=%v err=%v, want beta", rows, err)
	}

	// Conflict: A pushes gamma, B commits delta → pull yields
	// ErrMergeConflict·no automatic resolution·B's working set (delta)
	// preserved.
	if _, err := qa.Query(ctx, `UPDATE t5_note SET body='gamma' WHERE id='n1'`); err != nil {
		t.Fatalf("A update 2: %v", err)
	}
	commitA("A edits n1 again")
	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("SyncPush 3: %v", err)
	}
	if _, err := qb.Query(ctx, `UPDATE t5_note SET body='delta' WHERE id='n1'`); err != nil {
		t.Fatalf("B update: %v", err)
	}
	if _, err := qb.Query(ctx, `SELECT dolt_add('-A')`); err != nil {
		t.Fatalf("B dolt_add: %v", err)
	}
	if _, err := qb.Query(ctx, `SELECT dolt_commit('-m', $1)`, "B edits n1"); err != nil {
		t.Fatalf("B dolt_commit: %v", err)
	}
	if _, err := SyncPull(ctx, qb, "origin", false); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("충돌 pull err = %v, want ErrMergeConflict", err)
	}
	rows, err = qb.Query(ctx, `SELECT body FROM t5_note WHERE id='n1'`)
	if err != nil || len(rows) != 1 || strAny(rows[0][0]) != "delta" {
		t.Fatalf("충돌 후 B 작업집합 미보존: rows=%v err=%v, want delta", rows, err)
	}
	// dry: fetch only — no error·data unchanged.
	dryRes, err := SyncPull(ctx, qb, "origin", true)
	if err != nil {
		t.Fatalf("dry pull err = %v", err)
	}
	if dryRes.Status != "fetched" {
		t.Fatalf("dry status = %q, want fetched", dryRes.Status)
	}
	rows, err = qb.Query(ctx, `SELECT body FROM t5_note WHERE id='n1'`)
	if err != nil || len(rows) != 1 || strAny(rows[0][0]) != "delta" {
		t.Fatalf("dry 후 B 데이터 변형: rows=%v err=%v, want delta", rows, err)
	}
}

// TestSyncResetRealSlow — v1.7.2 T3 S2 slow (FLINT_TEST_DSN): reset
// measured on an isolated Doltgres — seed commit push → add a
// local-only commit → SyncReset rolls main back to the remote head and
// discards the local changes.
func TestSyncResetRealSlow(t *testing.T) {
	ctx := context.Background()
	dsn := testDSN(t)
	base, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer base.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	dbName := pgx.Identifier{"t3r_" + suffix}.Sanitize()
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName) })
	if _, err := base.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	pool, err := Connect(ctx, dsnForDatabase(t, dsn, dbName))
	if err != nil {
		t.Fatalf("Connect DB: %v", err)
	}
	defer pool.Close()
	q := poolQuerier{pool}

	name := "t3-reset"
	_ = RemoteRemove(ctx, q, name)
	t.Cleanup(func() { _ = RemoteRemove(ctx, q, name) })
	rawURL := "file://" + filepath.Join(t.TempDir(), "reset-remote")
	if err := RemoteAdd(ctx, q, name, rawURL); err != nil {
		t.Fatalf("RemoteAdd: %v", err)
	}

	commit := func(msg string) {
		t.Helper()
		if _, err := q.Query(ctx, `SELECT dolt_add('-A')`); err != nil {
			t.Fatalf("dolt_add: %v", err)
		}
		if _, err := q.Query(ctx, `SELECT dolt_commit('-m', $1)`, msg); err != nil {
			t.Fatalf("dolt_commit: %v", err)
		}
	}

	// Seed commit → push to the remote (fixing the remote head).
	for _, s := range []string{
		`CREATE TABLE t3_note (id text PRIMARY KEY, body text)`,
		`INSERT INTO t3_note VALUES ('n1','alpha')`,
	} {
		if _, err := q.Query(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	commit("seed alpha")
	if _, err := SyncPush(ctx, q, name); err != nil {
		t.Fatalf("SyncPush: %v", err)
	}
	remoteHead, err := syncHeadHash(ctx, q)
	if err != nil {
		t.Fatalf("remote head: %v", err)
	}

	// The no-op push measured (S6): with the remote already current, the
	// status is up_to_date.
	noop, err := SyncPush(ctx, q, name)
	if err != nil {
		t.Fatalf("no-op SyncPush: %v", err)
	}
	if noop.Status != "up_to_date" {
		t.Fatalf("no-op push status = %q, want up_to_date", noop.Status)
	}

	// A local-only commit (absent on the remote) — the reset's lost
	// portion.
	if _, err := q.Query(ctx, `UPDATE t3_note SET body='local-only' WHERE id='n1'`); err != nil {
		t.Fatalf("local edit: %v", err)
	}
	commit("local only edit")

	// reset — main rolls back to the remote head and the local-only
	// commit is discarded.
	res, err := SyncReset(ctx, q, name)
	if err != nil {
		t.Fatalf("SyncReset: %v", err)
	}
	if res.Status != "reset" || res.Hash != remoteHead {
		t.Fatalf("reset 결과 = %+v, want {reset %s}", res, remoteHead)
	}
	rows, err := q.Query(ctx, `SELECT body FROM t3_note WHERE id='n1'`)
	if err != nil || len(rows) != 1 || strAny(rows[0][0]) != "alpha" {
		t.Fatalf("reset 후 데이터 미복원: rows=%v err=%v, want alpha", rows, err)
	}
}

// dsnForDatabase — replaces only the DSN's database-name path (for the
// isolated A/B connections).
func dsnForDatabase(tb testing.TB, dsn, database string) string {
	tb.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		tb.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + strings.Trim(database, `"`)
	return u.String()
}

// poolQuerier — for the slow tests: demotes a pgxpool to a db.Querier
// (the same rows→[][]any collection as realStore.Query). The production
// path passes the CLI's wire.Store directly as the Querier
// (structurally satisfied).
type poolQuerier struct{ pool *pgxpool.Pool }

func (q poolQuerier) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := q.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, verr := rows.Values()
		if verr != nil {
			return nil, verr
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}
