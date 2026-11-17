// sync_test.go — v1.7.0 T4: sync 원격 등록(dolt_remote 래핑) 테스트.
// fast는 fake Querier로 SQL·오류 계약, slow(FLINT_TEST_DSN)는 격리
// Doltgres에서 dolt_remote add/list/remove 실측(file:// 원격).
// Review Focus(security): 원격 url 원문은 어떤 경로로도 노출되지
// 않는다 — 마스킹은 MaskRemoteURL 단일 경로.
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

// fakeSyncQuerier — Querier fake. sqlMatch가 참을 반환한 쿼리만 rows를
// 반환하고 나머지는 (nil, nil). 실행된 쿼리·인자는 전부 기록한다.
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

// TestMaskRemoteURL — 마스킹 계약: 스킴+호스트만 노출(자격증명·포트·
// 경로·쿼리 미노출). file://(호스트 없음)은 스킴만. 파싱 불가·스킴
// 부재 원문은 전체 미노출(고정 토큰).
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

// TestRemoteAddDuplicateRejected — 중복명은 dolt_remote('add') 실행 없이
// ErrRemoteExists(실측: dolt_remote는 중복명을 조용히 덮어쓴다 — 사전
// 조회로 차단이 계약).
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

// TestRemoteAddRunsDoltRemote — 신규명은 dolt_remote('add', name, url)을
// 인자 순서대로 실행한다.
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

// TestRemoteListParsesRows — dolt_remotes 행을 Remote로 파싱한다(정렬은
// SQL ORDER BY 소유 — Go 이중 정렬 없음, 아래 쿼리 검증 참조).
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

// TestRemoteListRequiresDoltRemotes — 목록은 dolt_remotes 시스템 테이블
// 조회 경로·이름순 정렬(실측: dolt_remote() 인자 없음 호출은 오류).
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

// TestRemoteRemoveMissingRejected — 미존재명은 dolt_remote('remove') 실행
// 없이 ErrRemoteNotFound(오류에 url 원문 없음).
func TestRemoteRemoveMissingRejected(t *testing.T) {
	f := &fakeSyncQuerier{ // 존재조회 0행 → sqlMatch nil이면 (nil,nil)
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

// TestRemoteRemoveRunsDoltRemote — 존재명은 dolt_remote('remove', name) 실행.
func TestRemoteRemoveRunsDoltRemote(t *testing.T) {
	f := &fakeSyncQuerier{
		rows:     [][]any{{1}}, // 존재조회 hit
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

// TestSyncRemoteRoundTripRealSlow — slow(FLINT_TEST_DSN): 격리 Doltgres에서
// file:// 원격 실등록·목록·삭제·중복/미존재 오류 실측.
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

	// 1) 신규 등록 → dolt_remotes에 실등록.
	if err := RemoteAdd(context.Background(), q, name, rawURL); err != nil {
		t.Fatalf("RemoteAdd: %v", err)
	}
	// 2) 목록 — 원문 url 회수(마스킹은 CLI 출력 계약, 여기선 원천 보존 확인).
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
	// 3) 중복 등록 차단(실측: dolt_remote는 조용히 덮어씀 — 사전 조회 계약).
	if err := RemoteAdd(context.Background(), q, name, rawURL); !errors.Is(err, ErrRemoteExists) {
		t.Fatalf("중복 add err = %v, want ErrRemoteExists", err)
	}
	// 4) 삭제 → 미존재 오류.
	if err := RemoteRemove(context.Background(), q, name); err != nil {
		t.Fatalf("RemoteRemove: %v", err)
	}
	if err := RemoteRemove(context.Background(), q, name); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("미존재 remove err = %v, want ErrRemoteNotFound", err)
	}
}

// fakeSyncScriptQuerier — T5 push/pull용 Querier fake: 쿼리별 stub 함수
// (사전 조회·push·fetch·merge·head 해시를 SQL 부문자열로 분기). 실행된
// 쿼리·인자는 전부 기록한다.
type fakeSyncScriptQuerier struct {
	fn      func(sql string, args []any) ([][]any, error)
	queries []string
	allArgs [][]any
}

func (f *fakeSyncScriptQuerier) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	cp := make([]any, len(args))
	copy(cp, args)
	f.allArgs = append(f.allArgs, cp)
	return f.fn(sql, args)
}

// newSyncScript — 표준 T5 스크립트 fake: 원격 조회(name→url)·head 해시
// 큐(호출순 pop)·push/fetch/merge 결과·오류 주입. mergeErr·fetchErr·
// pushErr는 해당 쿼리 오류, heads는 dolt_branches 조회 결과 큐.
func newSyncScript(remotes map[string]string, heads []string, pushErr, fetchErr, mergeErr error) *fakeSyncScriptQuerier {
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
			return [][]any{{"(0,\"pushed\")"}}, nil
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
		}
		return nil, nil
	}
	return f
}

func strAny(v any) string { s, _ := v.(string); return s }

// TestSyncPushRemoteNotFound — push 대상 원격이 미등록이면 dolt_push 실행
// 없이 ErrRemoteNotFound(사전 조회 계약 — T4 관례 승계).
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

// TestSyncPushRunsAndReportsHash — 등록 원격은 dolt_push(remote,'main')을
// 실행하고 head 해시를 결과로 보고한다(브랜치 'main' 고정 계약).
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

// TestSyncPushErrorMasksURL — push 실행 오류 메시지에 원격 url 원문이
// 실리면 마스킹으로 치환한다(도달 불가 오류 — Review Focus security).
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

// TestSyncPullFetchMergeOrder — pull은 원격 조회→fetch→head→merge 순으로,
// merge 인자는 <remote>/main, 해시 변화 시 status merged.
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

// TestSyncPullUpToDate — merge 후 해시 불변이면 status up_to_date.
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

// TestSyncPullDryFetchOnly — dry는 fetch까지만: merge 미실행·적용 없음.
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

// TestSyncPullRemoteNotFound — pull 대상 미등록도 사전 조회로 ErrRemoteNotFound.
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

// TestSyncPullConflictGuidance — merge 충돌은 ErrMergeConflict + 수동 해결
// 안내(자동 해결 금지 — Review Focus state). 실측 오류 문구 사본으로 판정.
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

// TestSyncPushPullRoundTripRealSlow — slow(FLINT_TEST_DSN): 격리 Doltgres
// 2데이터베이스(file:// 원격) 왕복 실측 — A push→B(dolt_clone) pull→B에서
// read 확인·충돌 시나리오(양측 동일 id 다른 내용)→ErrMergeConflict·
// dry는 fetch까지만·충돌 후 B 작업집합 보존.
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

	// A: 원격 등록·합성 시드 커밋·push.
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

	// B: dolt_clone으로 A 원격에서 생성(기기 이동 시나리오).
	if _, err := base.Exec(ctx, "SELECT dolt_clone($1, $2)", rawURL, strings.Trim(bName, `"`)); err != nil {
		t.Fatalf("dolt_clone: %v", err)
	}
	poolB, err := Connect(ctx, dsnForDatabase(t, dsn, bName))
	if err != nil {
		t.Fatalf("Connect B: %v", err)
	}
	defer poolB.Close()
	qb := poolQuerier{poolB}

	// A: 추가 커밋 push → B pull(merged) → B에서 read 확인.
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

	// 충돌: A는 gamma push, B는 delta 커밋 → pull은 ErrMergeConflict·
	// 자동 해결 없음·B 작업집합(delta) 보존.
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
	// dry: fetch까지만 — 오류 없음·데이터 불변.
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

// dsnForDatabase — DSN의 데이터베이스명 경로만 교체(격리 A/B 접속용).
func dsnForDatabase(tb testing.TB, dsn, database string) string {
	tb.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		tb.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + strings.Trim(database, `"`)
	return u.String()
}

// poolQuerier — slow 테스트용: pgxpool을 db.Querier로 격하(realStore.Query
// 와 동일한 rows→[][]any 수집). 프로덕션 경로는 CLI가 wire.Store를 직접
// Querier로 넘긴다(구조적 만족).
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
