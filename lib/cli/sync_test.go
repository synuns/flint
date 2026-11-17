// sync_test.go — `flint sync remote` CLI 계약 테스트.
// fast는 fakeStore.Query로 SQL을 stub — 로직(db.RemoteAdd/List/Remove
// 실구현)은 실코드 경유, 실행만 fake(SUPERLITE_REAL=1 seam은 실DB).
// 원격 url 원문·자격증명은 성공·오류·사용법
// 어느 출력에도 노출되지 않는다(list는 MaskRemoteURL 결과만).
package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// syncQueryFn — fakeStore.queryFn: 원격 쿼리 패턴별 stub.
// 존재조회(WHERE name)·add/remove(dolt_remote)·목록(FROM dolt_remotes
// ORDER BY)을 SQL 부문자열로 분기한다.
func syncQueryFn(existing map[string]bool, listRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM dolt_remotes WHERE name"):
			if existing != nil && existing[str(args[0])] {
				return [][]any{{1}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "dolt_remote('add'"):
			existing[str(args[0])] = true
			return [][]any{{0}}, nil
		case strings.Contains(sql, "dolt_remote('remove'"):
			delete(existing, str(args[0]))
			return [][]any{{0}}, nil
		case strings.Contains(sql, "FROM dolt_remotes"):
			if listRows == nil {
				return nil, nil
			}
			return listRows, nil
		}
		return nil, nil
	}
}

func str(v any) string { s, _ := v.(string); return s }

func runSyncArgs(args ...string) []string { return args }

// TestSyncRemoteAddOK — 신규 등록 exit 0·1행 JSON. 출력에 원문 url·
// 자격증명 미포함(이름·상태만 — url 자체가 민감 정보).
func TestSyncRemoteAddOK(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "add", "demo", "https://user:s3cret@rem-example.invalid/org/repo"),
		strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"remote":"demo"`) || !strings.Contains(out, `"status":"added"`) {
		t.Fatalf("출력 계약 불일치: %s", out)
	}
	for _, leak := range []string{"secret", "doltremoteapi.dolthub.com", "org/repo", "user:"} {
		if strings.Contains(out, leak) {
			t.Fatalf("출력에 url 성분 노출(%q): %s", leak, out)
		}
	}
}

// TestSyncRemoteAddDuplicate — 중복명 exit 3, 오류에 url 원문 미포함.
func TestSyncRemoteAddDuplicate(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{"demo": true}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "add", "demo", "https://user:s3cret@rem-example.invalid/org/repo"),
		strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("오류 JSON 아님: %s", out)
	}
	for _, leak := range []string{"secret", "doltremoteapi.dolthub.com"} {
		if strings.Contains(out, leak) {
			t.Fatalf("오류에 url 성분 노출(%q): %s", leak, out)
		}
	}
}

// TestSyncUsageErrors — 사용법 오류 전부 exit 3(인자 부족·무효 sub·
// 빈 name·빈 url·스킴 없는 url·`sync` 단독·`remote` 단독·위치 인자 과잉).
func TestSyncUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"remote"},
		{"push"},           // push 원격명 부재
		{"pull"},           // pull 원격명 부재
		{"remote", "push"}, // remote의 무효 sub
		{"remote", "add"},
		{"remote", "add", "only-name"},
		{"remote", "add", "", "https://h.example/r"},
		{"remote", "add", "demo", ""},
		{"remote", "add", "demo", "doltremoteapi.dolthub.com/org"}, // 스킴 없음
		{"remote", "remove"},
		{"remote", "remove", ""},
		{"remote", "list", "extra"},
		{"remote", "add", "demo", "https://h.example/r", "extra"},
	}
	for _, args := range cases {
		var stdout bytes.Buffer
		f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
		code := runSync(context.Background(), f, args, strings.NewReader(""), &stdout)
		if code != 3 {
			t.Fatalf("args %v: exit = %d, want 3, stdout = %s", args, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"error"`) {
			t.Fatalf("args %v: 오류 JSON 아님: %s", args, stdout.String())
		}
	}
}

// TestSyncServerDown — 서버다운 exit 2(ErrLine 한 줄·MapExit 관례).
func TestSyncServerDown(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{pingErr: context.DeadlineExceeded, queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2, stdout = %s", code, stdout.String())
	}
	// st nil도 서버다운과 동일 취급(export 관례 승계).
	stdout.Reset()
	if code := runSync(context.Background(), nil,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout); code != 2 {
		t.Fatalf("nil store exit = %d, want 2", code)
	}
}

// TestSyncRemoteListMasked — 목록은 마스킹된 url만(스킴+호스트 — 자격증명·
// 경로·포트 미노출). 원문 성분은 일절 미포함.
func TestSyncRemoteListMasked(t *testing.T) {
	var stdout bytes.Buffer
	listRows := [][]any{
		{"demo", "https://user:s3cret@rem-example.invalid/org/repo"},
		{"local", "file:///tmp/flint-remote-demo2"},
		{"pg", "postgres://flint:pw@db.example.invalid:5433/flint"},
	}
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, listRows)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{`"name":"demo"`, `"url":"https://rem-example.invalid"`, `"url":"file://"`, `"url":"postgres://db.example.invalid"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("마스킹 목록에 %q 부재: %s", want, out)
		}
	}
	for _, leak := range []string{"secret", "user:", "org/repo", "someone", "db.example.com:5433", "/.flint/remote", ":pw"} {
		if strings.Contains(out, leak) {
			t.Fatalf("목록에 url 성분 노출(%q): %s", leak, out)
		}
	}
}

// TestSyncRemoteListEmpty — 원격 0개는 {"remotes":[]} exit 0.
func TestSyncRemoteListEmpty(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	if strings.TrimSpace(stdout.String()) != `{"remotes":[]}` {
		t.Fatalf("빈 목록 계약 불일치: %s", stdout.String())
	}
}

// TestSyncRemoteRemoveOK — 삭제 exit 0·1행 JSON(이름·상태만).
func TestSyncRemoteRemoveOK(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{"demo": true}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "remove", "demo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"removed"`) {
		t.Fatalf("출력 계약 불일치: %s", stdout.String())
	}
}

// TestSyncRemoteRemoveMissing — 미존재명 exit 3.
func TestSyncRemoteRemoveMissing(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "remove", "ghost"), strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"error"`) {
		t.Fatalf("오류 JSON 아님: %s", stdout.String())
	}
}

// TestSyncAddQueryFailure — dolt_remote 실행 오류는 exit 1(원인 보존,
// url 원문 미노출 — 오류 래핑에 url을 실지 않는다).
func TestSyncAddQueryFailure(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "dolt_remote(") {
			return nil, context.Canceled
		}
		return nil, nil
	}}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "add", "demo", "https://user:s3cret@rem-example.invalid/org/repo"),
		strings.NewReader(""), &stdout)
	if code != 1 {
		t.Fatalf("exit = %d, want 1, stdout = %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "doltremoteapi.dolthub.com") {
		t.Fatalf("실패 오류에 url 노출: %s", stdout.String())
	}
}

// syncOpsQueryFn — push/pull용 fakeStore.queryFn: 원격명→url 사전
// 조회·head 해시 큐(호출순 pop)·push/fetch/merge stub·오류 주입.
func syncOpsQueryFn(remotes map[string]string, heads []string, pushErr, fetchErr, mergeErr error) func(string, []any) ([][]any, error) {
	headIdx := 0
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM dolt_remotes WHERE name"):
			if u, ok := remotes[str(args[0])]; ok {
				return [][]any{{str(args[0]), u}}, nil
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
}

// TestSyncPushOK — `sync push <remote>` exit 0·{remote,status,hash} JSON·
// url 원문 미노출(원격명만으로 실행 — url은 출력에 없다).
func TestSyncPushOK(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "https://user:s3cret@rem-example.invalid/org/repo"},
		[]string{"hash-1"}, nil, nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("push", "demo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{`"remote":"demo"`, `"status":"pushed"`, `"hash":"hash-1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("출력에 %q 부재: %s", want, out)
		}
	}
	for _, leak := range []string{"secret", "doltremoteapi", "org/repo"} {
		if strings.Contains(out, leak) {
			t.Fatalf("출력에 url 성분 노출(%q): %s", leak, out)
		}
	}
}

// TestSyncPullMerged — `sync pull <remote>` 해시 변화 시 status merged.
func TestSyncPullMerged(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"},
		[]string{"h-old", "h-new"}, nil, nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("pull", "demo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"status":"merged"`) || !strings.Contains(out, `"hash":"h-new"`) {
		t.Fatalf("출력 계약 불일치: %s", out)
	}
}

// TestSyncPullDry — `sync pull <remote> --dry`는 status fetched·적용 없음.
func TestSyncPullDry(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"},
		[]string{"h-local"}, nil, nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("pull", "demo", "--dry"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"fetched"`) {
		t.Fatalf("dry 출력 계약 불일치: %s", stdout.String())
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_merge(") {
			t.Fatalf("dry에서 merge 실행됨: %s", q)
		}
	}
}

// TestSyncPushPullRemoteMissing — 미등록 원격 push/pull 전부 exit 3.
func TestSyncPushPullRemoteMissing(t *testing.T) {
	for _, sub := range []string{"push", "pull"} {
		var stdout bytes.Buffer
		f := &fakeStore{queryFn: syncOpsQueryFn(map[string]string{}, []string{"h0"}, nil, nil, nil)}
		code := runSync(context.Background(), f,
			runSyncArgs(sub, "ghost"), strings.NewReader(""), &stdout)
		if code != 3 {
			t.Fatalf("%s exit = %d, want 3, stdout = %s", sub, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"error"`) {
			t.Fatalf("%s 오류 JSON 아님: %s", sub, stdout.String())
		}
	}
}

// TestSyncPushFailureMasksURL — push 실행 오류는 exit 1·url 원문 마스킹
// 치환(도달 불가 — 원인은 보존하되 스킴+호스트만).
func TestSyncPushFailureMasksURL(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/secret-remote"}, nil,
		errors.New("push to file:///tmp/secret-remote refused"), nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("push", "demo"), strings.NewReader(""), &stdout)
	if code != 1 {
		t.Fatalf("exit = %d, want 1, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, "/tmp/secret-remote") {
		t.Fatalf("오류에 url 원문 노출: %s", out)
	}
	if !strings.Contains(out, "file://") {
		t.Fatalf("마스킹된 원인 미보존: %s", out)
	}
}

// TestSyncPullConflict — merge 충돌 exit 1 + 수동 해결 안내(자동 해결
// 금지).
func TestSyncPullConflict(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"},
		[]string{"h0", "h0"}, nil, nil,
		errors.New("Merge conflict detected, @autocommit transaction rolled back"))}
	code := runSync(context.Background(), f,
		runSyncArgs("pull", "demo"), strings.NewReader(""), &stdout)
	if code != 1 {
		t.Fatalf("exit = %d, want 1, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "manually") {
		t.Fatalf("수동 해결 안내 부재: %s", out)
	}
	if strings.Contains(out, "/tmp/demo-remote") {
		t.Fatalf("충돌 오류에 url 노출: %s", out)
	}
}

// TestSyncPushPullUsageErrors — push/pull 사용법 오류 exit 3(인자 부족·
// 빈 이름·과잉·무효 플래그).
func TestSyncPushPullUsageErrors(t *testing.T) {
	cases := [][]string{
		{"push"},
		{"pull"},
		{"push", ""},
		{"pull", ""},
		{"push", "demo", "extra"},
		{"pull", "demo", "extra"},
		{"pull", "demo", "--bad"},
		{"pull", "demo", "--dry", "extra"},
	}
	for _, args := range cases {
		var stdout bytes.Buffer
		f := &fakeStore{queryFn: syncOpsQueryFn(map[string]string{}, nil, nil, nil, nil)}
		code := runSync(context.Background(), f, args, strings.NewReader(""), &stdout)
		if code != 3 {
			t.Fatalf("args %v: exit = %d, want 3, stdout = %s", args, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"error"`) {
			t.Fatalf("args %v: 오류 JSON 아님: %s", args, stdout.String())
		}
	}
}

// TestSyncPushPullRealSeam — seam 체크(SUPERLITE_REAL=1 + FLINT_TEST_DSN):
// 실DB에서 push(합성 file:// 원격)→pull up_to_date→dry→미등록 exit 3.
// 충돌 시나리오의 실측 커버는 lib/db TestSyncPushPullRoundTripRealSlow.
func TestSyncPushPullRealSeam(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("real seam disabled: set SUPERLITE_REAL=1 and FLINT_TEST_DSN")
	}
	st, err := newRealStore(context.Background(), dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer st.Close()

	name := "t5-seam"
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := runSync(context.Background(), st, args, strings.NewReader(""), &out)
		return code, out.String()
	}
	run("remote", "remove", name) // 선행 잔재 정리
	t.Cleanup(func() { run("remote", "remove", name) })

	rawURL := "file://" + t.TempDir() + "/seam-remote"
	if code, out := run("remote", "add", name, rawURL); code != 0 {
		t.Fatalf("add exit = %d, out = %s", code, out)
	}
	// push — status·hash·url 미노출.
	code, out := run("push", name)
	if code != 0 {
		t.Fatalf("push exit = %d, out = %s", code, out)
	}
	if !strings.Contains(out, `"status":"pushed"`) || !strings.Contains(out, `"hash":"`) {
		t.Fatalf("push 출력 계약 불일치: %s", out)
	}
	if strings.Contains(out, rawURL) {
		t.Fatalf("push 출력에 url 원문 노출: %s", out)
	}
	// pull — 자기 원격이므로 up_to_date.
	if code, out := run("pull", name); code != 0 || !strings.Contains(out, `"status":"up_to_date"`) {
		t.Fatalf("pull exit = %d, out = %s", code, out)
	}
	// dry — fetched·오류 없음.
	if code, out := run("pull", name, "--dry"); code != 0 || !strings.Contains(out, `"status":"fetched"`) {
		t.Fatalf("dry pull exit = %d, out = %s", code, out)
	}
	// 미등록 push exit 3.
	if code, _ := run("push", "ghost"); code != 3 {
		t.Fatalf("missing push exit = %d, want 3", code)
	}
}

// TestSyncRemoteRoundTripRealSeam — seam 체크(SUPERLITE_REAL=1 +
// FLINT_TEST_DSN): runSync 전체 경로를 실DB로 왕복 실측한다(등록→
// 마스킹 목록→중복 exit 3→삭제→미존재 exit 3). 미설정 환경은 스킵 —
// fast fake 계약 테스트가 위에서 동일 계약을 커버한다.
func TestSyncRemoteRoundTripRealSeam(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("real seam disabled: set SUPERLITE_REAL=1 and FLINT_TEST_DSN")
	}
	st, err := newRealStore(context.Background(), dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer st.Close()

	name := "t4-seam"
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := runSync(context.Background(), st, args, strings.NewReader(""), &out)
		return code, out.String()
	}
	run("remote", "remove", name) // 선행 잔재 정리
	t.Cleanup(func() { run("remote", "remove", name) })

	// 등록(file:// 합성 원격 — 원문은 목록 출력에 절대 나오지 않는다).
	rawURL := "file://" + t.TempDir() + "/seam-remote"
	if code, out := run("remote", "add", name, rawURL); code != 0 {
		t.Fatalf("add exit = %d, out = %s", code, out)
	}
	// 목록 — 마스킹된 url만.
	code, out := run("remote", "list")
	if code != 0 {
		t.Fatalf("list exit = %d, out = %s", code, out)
	}
	if !strings.Contains(out, `"name":"`+name+`"`) || !strings.Contains(out, `"url":"file://"`) {
		t.Fatalf("목록 마스킹 계약 불일치: %s", out)
	}
	// 중복 exit 3.
	if code, out := run("remote", "add", name, rawURL); code != 3 {
		t.Fatalf("duplicate add exit = %d, want 3, out = %s", code, out)
	}
	// 삭제 → 미존재 exit 3.
	if code, out := run("remote", "remove", name); code != 0 {
		t.Fatalf("remove exit = %d, out = %s", code, out)
	}
	if code, _ := run("remote", "remove", name); code != 3 {
		t.Fatalf("missing remove exit = %d, want 3", code)
	}
}
