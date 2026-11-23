package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

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

func TestSyncUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"remote"},
		{"push"},
		{"pull"},
		{"remote", "push"},
		{"remote", "add"},
		{"remote", "add", "only-name"},
		{"remote", "add", "", "https://h.example/r"},
		{"remote", "add", "demo", ""},
		{"remote", "add", "demo", "doltremoteapi.dolthub.com/org"},
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

func TestSyncServerDown(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{pingErr: context.DeadlineExceeded, queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2, stdout = %s", code, stdout.String())
	}

	stdout.Reset()
	if code := runSync(context.Background(), nil,
		runSyncArgs("remote", "list"), strings.NewReader(""), &stdout); code != 2 {
		t.Fatalf("nil store exit = %d, want 2", code)
	}
}

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

func syncOpsQueryFn(remotes map[string]string, heads []string, pushErr, fetchErr, mergeErr error) func(string, []any) ([][]any, error) {
	return syncOpsRowsQueryFn(remotes, heads, [][]any{{"(0,\"pushed\")"}}, pushErr, fetchErr, mergeErr)
}

func syncOpsRowsQueryFn(remotes map[string]string, heads []string, pushRows [][]any, pushErr, fetchErr, mergeErr error) func(string, []any) ([][]any, error) {
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
}

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

func TestSyncRemoteAddSchemeWhitelist(t *testing.T) {
	ok := []struct{ name, url string }{
		{"demo-h", "https://h.example.invalid/org/repo"},
		{"demo-p", "http://h.example.invalid/org/repo"},
		{"demo-f", "file:///tmp/flint-demo-remote"},
	}
	for _, tc := range ok {
		var stdout bytes.Buffer
		f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
		code := runSync(context.Background(), f,
			runSyncArgs("remote", "add", tc.name, tc.url), strings.NewReader(""), &stdout)
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0, stdout = %s", tc.url, code, stdout.String())
		}
	}
	bad := []string{
		"postgres://flint:pw@db.example.invalid:5433/flint",
		"postgresql://db.example.invalid/flint",
		"ftp://h.example.invalid/repo",
		"file:///tmp/x y",
	}
	for _, u := range bad {
		var stdout bytes.Buffer
		f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
		code := runSync(context.Background(), f,
			runSyncArgs("remote", "add", "demo", u), strings.NewReader(""), &stdout)
		if code != 3 {
			t.Fatalf("%s: exit = %d, want 3, stdout = %s", u, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"error"`) {
			t.Fatalf("%s: 오류 JSON 아님: %s", u, stdout.String())
		}
	}
}

func TestSyncRemoteAddFileNote(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncQueryFn(map[string]bool{}, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("remote", "add", "demo", "https://h.example.invalid/org/repo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "file:// paths resolve on the Doltgres server") {
		t.Fatalf("file:// 안내 행 부재: %s", stdout.String())
	}
}

func TestSyncPushNoOpReported(t *testing.T) {
	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsRowsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"},
		[]string{"hash-1"}, [][]any{{0, "Everything up-to-date"}}, nil, nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("push", "demo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"up_to_date"`) {
		t.Fatalf("no-op push 보고 계약 위반: %s", stdout.String())
	}
}

func TestSyncResetYesGate(t *testing.T) {

	var stdout bytes.Buffer
	f := &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"}, []string{"h-remote"}, nil, nil, nil)}
	code := runSync(context.Background(), f,
		runSyncArgs("reset", "--to-remote", "demo"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("dry-run exit = %d, want 0, stdout = %s", code, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{`"status":"dry_run"`, "discards", "--yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run 출력에 %q 부재: %s", want, out)
		}
	}
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_fetch(") || strings.Contains(q, "dolt_reset(") {
			t.Fatalf("dry-run에서 fetch/reset 실행됨: %s", q)
		}
	}

	stdout.Reset()
	f = &fakeStore{queryFn: syncOpsQueryFn(
		map[string]string{"demo": "file:///tmp/demo-remote"}, []string{"h-remote"}, nil, nil, nil)}
	code = runSync(context.Background(), f,
		runSyncArgs("reset", "--to-remote", "demo", "--yes"), strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("--yes exit = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"reset"`) || !strings.Contains(stdout.String(), `"hash":"h-remote"`) {
		t.Fatalf("reset 실행 출력 계약 위반: %s", stdout.String())
	}

	stdout.Reset()
	f = &fakeStore{queryFn: syncOpsQueryFn(map[string]string{}, []string{"h0"}, nil, nil, nil)}
	code = runSync(context.Background(), f,
		runSyncArgs("reset", "--to-remote", "ghost", "--yes"), strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("미등록 reset exit = %d, want 3, stdout = %s", code, stdout.String())
	}
}

func TestSyncResetUsageErrors(t *testing.T) {
	cases := [][]string{
		{"reset"},
		{"reset", "--yes"},
		{"reset", "--to-remote"},
		{"reset", "--to-remote", "demo", "--bad"},
		{"reset", "demo"},
		{"reset", "--to-remote", ""},
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
	run("remote", "remove", name)
	t.Cleanup(func() { run("remote", "remove", name) })

	rawURL := "file://" + t.TempDir() + "/seam-remote"
	if code, out := run("remote", "add", name, rawURL); code != 0 {
		t.Fatalf("add exit = %d, out = %s", code, out)
	}

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

	if code, out := run("pull", name); code != 0 || !strings.Contains(out, `"status":"up_to_date"`) {
		t.Fatalf("pull exit = %d, out = %s", code, out)
	}

	if code, out := run("pull", name, "--dry"); code != 0 || !strings.Contains(out, `"status":"fetched"`) {
		t.Fatalf("dry pull exit = %d, out = %s", code, out)
	}

	if code, _ := run("push", "ghost"); code != 3 {
		t.Fatalf("missing push exit = %d, want 3", code)
	}
}

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
	run("remote", "remove", name)
	t.Cleanup(func() { run("remote", "remove", name) })

	rawURL := "file://" + t.TempDir() + "/seam-remote"
	if code, out := run("remote", "add", name, rawURL); code != 0 {
		t.Fatalf("add exit = %d, out = %s", code, out)
	}

	code, out := run("remote", "list")
	if code != 0 {
		t.Fatalf("list exit = %d, out = %s", code, out)
	}
	if !strings.Contains(out, `"name":"`+name+`"`) || !strings.Contains(out, `"url":"file://"`) {
		t.Fatalf("목록 마스킹 계약 불일치: %s", out)
	}

	if code, out := run("remote", "add", name, rawURL); code != 3 {
		t.Fatalf("duplicate add exit = %d, want 3, out = %s", code, out)
	}

	if code, out := run("remote", "remove", name); code != 0 {
		t.Fatalf("remove exit = %d, out = %s", code, out)
	}
	if code, _ := run("remote", "remove", name); code != 3 {
		t.Fatalf("missing remove exit = %d, want 3", code)
	}
}
