package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/data"
	"github.com/synuns/flint/lib/db"
)

func withSeedStoreRecorder(t *testing.T, f *fakeStore) *[]string {
	t.Helper()
	var dsns []string
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		dsns = append(dsns, dsn)
		return f, nil
	}
	t.Cleanup(func() { newStore = prev })
	return &dsns
}

func withSeedWriteRecorder(t *testing.T, status string) *[]seedWriteCall {
	t.Helper()
	var calls []seedWriteCall
	prev := seedWriteFn
	seedWriteFn = func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
		b, _ := io.ReadAll(stdin)
		calls = append(calls, seedWriteCall{args: args, stdin: string(b)})
		fmt.Fprintf(stdout, `{"status":%q}`+"\n", status)
		return 0
	}
	t.Cleanup(func() { seedWriteFn = prev })
	return &calls
}

type seedWriteCall struct {
	args  []string
	stdin string
}

func withSeedEnsureRecorder(t *testing.T) *[][2]string {
	t.Helper()
	var got [][2]string
	prev := seedEnsureDBFn
	seedEnsureDBFn = func(ctx context.Context, adminDSN, dbName string) error {
		got = append(got, [2]string{adminDSN, dbName})
		return nil
	}
	t.Cleanup(func() { seedEnsureDBFn = prev })
	return &got
}

func runSeedArgs(args ...string) (string, int) {
	var b strings.Builder
	code := runSeed(context.Background(), nil, args, strings.NewReader(""), &b)
	return b.String(), code
}

func TestSeedRegistered(t *testing.T) {
	if _, ok := Lookup("seed"); !ok {
		t.Fatal("Lookup(\"seed\") 실패 — 미등록 커맨드")
	}
}

func TestDeriveDemoDSN(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "기본", in: "postgresql://flint:pw@localhost:5434/flint", want: "postgresql://flint:pw@localhost:5434/flint_demo"},
		{name: "쿼리 유지", in: "postgresql://flint:pw@h:5434/flint?sslmode=disable", want: "postgresql://flint:pw@h:5434/flint_demo?sslmode=disable"},
		{name: "db명 부재", in: "postgresql://flint:pw@localhost:5434", wantErr: true},
		{name: "빈 db명", in: "postgresql://flint:pw@localhost:5434/", wantErr: true},
		{name: "파싱 불가", in: "://<<", wantErr: true},

		{name: "키워드 형태", in: "host=localhost port=5434 dbname=flint", wantErr: true},
	}
	for _, c := range cases {
		got, err := deriveDemoDSN(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: 오류 기대, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSeedDeriveFailExit3(t *testing.T) {
	t.Setenv("FLINT_DEMO_DSN", "")
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@localhost:5434")
	out, code := runSeedArgs("--demo")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "FLINT_DEMO_DSN") {
		t.Fatalf("FLINT_DEMO_DSN 안내 없음: %s", out)
	}
}

func TestSeedKeywordDSNExit3(t *testing.T) {
	t.Setenv("FLINT_DEMO_DSN", "")
	t.Setenv("FLINT_DSN", "host=localhost port=5434 dbname=flint")
	out, code := runSeedArgs("--demo")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "FLINT_DEMO_DSN") {
		t.Fatalf("FLINT_DEMO_DSN 안내 없음: %s", out)
	}
}

func TestSeedOverrideSkipsEnsure(t *testing.T) {
	t.Setenv("FLINT_DEMO_DSN", "postgresql://flint:pw@other-host:5433/other_demo")
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@localhost:5434/flint")
	ensures := withSeedEnsureRecorder(t)
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { newStore = prev })
	out, code := runSeedArgs("--demo")
	if code == 0 {
		t.Fatalf("연결 실패가 exit 0으로 소실: out=%s", out)
	}
	if len(*ensures) != 0 {
		t.Fatalf("오버라이드 DSN에서 ensure 실행됨(해서는 안 됨): %v", *ensures)
	}
}

func TestSeedRequiresDemoFlag(t *testing.T) {
	for _, args := range [][]string{{}, {"--reset"}, {"--yes"}} {
		out, code := runSeedArgs(args...)
		if code != 3 {
			t.Fatalf("%v: exit=%d, want 3; out=%s", args, code, out)
		}
		decode(t, out)
	}
}

func TestSeedUnknownFlag(t *testing.T) {
	out, code := runSeedArgs("--demo", "--bogus")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)
}

func TestSeedDemoWritesPrefixedPayloads(t *testing.T) {
	t.Setenv("FLINT_DEMO_DSN", "")
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@localhost:5434/flint")
	f := &fakeStore{}
	dsns := withSeedStoreRecorder(t, f)
	calls := withSeedWriteRecorder(t, "committed")
	out, code := runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(*dsns) == 0 || (*dsns)[0] != "postgresql://flint:pw@localhost:5434/flint_demo" {
		t.Fatalf("데모 DSN 미사용: %v", *dsns)
	}
	if len(*calls) == 0 {
		t.Fatal("seedWriteFn 호출 없음")
	}

	sent := map[string]bool{}
	for _, c := range *calls {
		if len(c.args) == 0 {
			t.Fatalf("write 인자 없음: %v", c.args)
		}
		kind := c.args[0]
		for _, line := range strings.Split(strings.TrimSpace(c.stdin), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var p map[string]any
			if err := json.Unmarshal([]byte(line), &p); err != nil {
				t.Fatalf("전달 payload 파싱 실패: %v (%q)", err, line)
			}
			var id string
			if kind == "project" {
				id = fmt.Sprint(p["name"])
			} else {
				id = fmt.Sprint(p["topic"])
			}
			if !strings.HasPrefix(id, demoTopicPrefix) {
				t.Fatalf("데모 접두 누락: %q (kind %s)", id, kind)
			}
			sent[kind+"|"+id] = true
		}
	}
	want := 0
	for _, line := range strings.Split(string(data.SeedJSONL()), "\n") {
		if strings.TrimSpace(line) != "" {
			want++
		}
	}
	if len(sent) != want {
		t.Fatalf("전달 payload 수=%d, want %d (embed 전량)", len(sent), want)
	}

	found := false
	for _, q := range f.queries {
		if strings.Contains(q, "INSERT INTO meta") && strings.Contains(q, "ON CONFLICT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("demo_owner upsert 없음: %v", f.queries)
	}
	m := decode(t, out)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], out)
	}
	if m["database"] != "flint_demo" {
		t.Fatalf("database=%v, want flint_demo; out=%s", m["database"], out)
	}
}

func TestSeedDemoDSNOverride(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@localhost:5434/flint")
	t.Setenv("FLINT_DEMO_DSN", "postgresql://flint:pw@localhost:5434/other_demo")
	dsns := withSeedStoreRecorder(t, &fakeStore{})
	withSeedWriteRecorder(t, "committed")
	out, code := runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(*dsns) == 0 || (*dsns)[0] != "postgresql://flint:pw@localhost:5434/other_demo" {
		t.Fatalf("FLINT_DEMO_DSN 미반영: %v", *dsns)
	}
	if m := decode(t, out); m["database"] != "other_demo" {
		t.Fatalf("database=%v, want other_demo", m["database"])
	}
}

func TestSeedEnsureDBOnConnectFail(t *testing.T) {
	t.Setenv("FLINT_DEMO_DSN", "")
	t.Setenv("FLINT_DSN", "postgresql://flint:pw@localhost:5434/flint")
	ensures := withSeedEnsureRecorder(t)
	attempt := 0
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		attempt++
		if attempt == 1 {
			return nil, fmt.Errorf("database does not exist")
		}
		return &fakeStore{}, nil
	}
	t.Cleanup(func() { newStore = prev })
	withSeedWriteRecorder(t, "committed")
	out, code := runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(*ensures) != 1 || (*ensures)[0] != [2]string{"postgresql://flint:pw@localhost:5434/flint", "flint_demo"} {
		t.Fatalf("ensure 호출 불일치: %v", *ensures)
	}
}

func seedResetFake(t *testing.T) (*fakeStore, *[]any) {
	t.Helper()
	f := &fakeStore{}
	var argsSeen []any
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		argsSeen = append(argsSeen, args...)
		switch {
		case strings.Contains(sql, "FROM meta WHERE key"):
			if fmt.Sprint(args[0]) == demoOwnerKey {
				return [][]any{{demoOwnerValue}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			return [][]any{{int64(2)}}, nil
		case strings.Contains(sql, "count(*) FROM projects"):
			return [][]any{{int64(1)}}, nil
		}
		return nil, nil
	}
	return f, &argsSeen
}

func TestSeedResetOwnerMismatchExit3(t *testing.T) {
	for _, owner := range []string{"missing", "someone-else"} {
		f := &fakeStore{}
		val := owner
		f.queryFn = func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM meta WHERE key") && val != "missing" {
				return [][]any{{val}}, nil
			}
			return nil, nil
		}
		withSeedStoreRecorder(t, f)
		out, code := runSeedArgs("--demo", "--reset", "--yes")
		if code != 3 {
			t.Fatalf("owner=%q: exit=%d, want 3; out=%s", owner, code, out)
		}
		decode(t, out)
		for _, q := range f.queries {
			if strings.Contains(q, "DELETE") {
				t.Fatalf("owner 불일치인데 DELETE 실행: %s", q)
			}
		}
	}
}

func TestSeedResetDryRun(t *testing.T) {
	f, _ := seedResetFake(t)
	withSeedStoreRecorder(t, f)
	out, code := runSeedArgs("--demo", "--reset")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "dry_run" {
		t.Fatalf("status=%v, want dry_run; out=%s", m["status"], out)
	}
	if m["targets"] != float64(3) {
		t.Fatalf("targets=%v, want 3; out=%s", m["targets"], out)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "DELETE") {
			t.Fatalf("dry-run에서 DELETE 실행: %s", q)
		}
	}
}

func TestSeedResetYesDeletes(t *testing.T) {
	f, argsSeen := seedResetFake(t)
	withSeedStoreRecorder(t, f)
	out, code := runSeedArgs("--demo", "--reset", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], out)
	}
	var delKnowledge, delProjects, delMeta, prefixArg bool
	for _, q := range f.queries {
		switch {
		case strings.Contains(q, "DELETE FROM knowledge"):
			delKnowledge = true
		case strings.Contains(q, "DELETE FROM projects"):
			delProjects = true
		case strings.Contains(q, "DELETE FROM meta"):
			delMeta = true
		}
	}
	for _, a := range *argsSeen {
		if s, ok := a.(string); ok && strings.HasPrefix(s, demoTopicPrefix) {
			prefixArg = true
		}
	}
	if !delKnowledge || !delProjects || !delMeta || !prefixArg {
		t.Fatalf("삭제 대상 식별 불완전: knowledge=%v projects=%v meta=%v prefixArg=%v",
			delKnowledge, delProjects, delMeta, prefixArg)
	}
}

func TestSetupNextMentionsSeedDemo(t *testing.T) {
	steps := SetupSteps()
	found := false
	for _, s := range steps {
		if strings.Contains(s, "make seed") {
			t.Fatalf("단계에 make seed 잔존: %q", s)
		}
		if strings.Contains(s, "flint seed --demo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("단계에 flint seed --demo 없음: %v", steps)
	}
	for _, n := range setupNext {
		if strings.Contains(n, "make seed") {
			t.Fatalf("next 안내에 make seed 잔존: %q", n)
		}
	}
}

func TestSeedDemoRealSlow(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()

	t.Setenv("FLINT_DSN", dsn)
	t.Setenv("FLINT_DEMO_DSN", "")
	demoDSN, dbName, err := resolveDemoDSN()
	if err != nil {
		t.Fatalf("파생 실패: %v", err)
	}
	admin, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("test server unavailable: %v", err)
	}
	t.Cleanup(admin.Close)
	dropDemo := func() {
		_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+dbName)
	}
	dropDemo()
	t.Cleanup(dropDemo)

	out, code := runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("1회차 exit=%d; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "ok" || m["database"] != dbName {
		t.Fatalf("1회차 요약: %s", out)
	}

	out, code = runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("2회차 exit=%d, want 0; out=%s", code, out)
	}
	m = decode(t, out)
	results, _ := m["results"].(map[string]any)
	if len(results) == 0 {
		t.Fatalf("2회차 결과 없음: %s", out)
	}
	for kind, st := range results {
		if st != "duplicate" {
			t.Fatalf("2회차 %s status=%v, want duplicate; out=%s", kind, st, out)
		}
	}

	st, err := newStore(ctx, demoDSN)
	if err != nil {
		t.Fatalf("데모 DB 접속: %v", err)
	}
	rows, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE topic LIKE $1`, demoTopicPrefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || fmt.Sprint(rows[0][0]) == "0" {
		t.Fatalf("데모 knowledge 미적립: %v", rows)
	}
	rows, err = st.Query(ctx, `SELECT count(*) FROM projects WHERE name LIKE $1`, demoTopicPrefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || fmt.Sprint(rows[0][0]) == "0" {
		t.Fatalf("데모 projects 미적립: %v", rows)
	}
	rows, err = st.Query(ctx, `SELECT value FROM meta WHERE key = $1`, demoOwnerKey)
	if err != nil || len(rows) == 0 || fmt.Sprint(rows[0][0]) != demoOwnerValue {
		t.Fatalf("demo_owner 마커 불일치: %v %v", rows, err)
	}
	st.Close()

	out, code = runSeedArgs("--demo", "--reset")
	if code != 0 || decode(t, out)["status"] != "dry_run" {
		t.Fatalf("reset dry-run 실패: exit=%d out=%s", code, out)
	}

	out, code = runSeedArgs("--demo", "--reset", "--yes")
	if code != 0 {
		t.Fatalf("reset --yes exit=%d; out=%s", code, out)
	}

	out, code = runSeedArgs("--demo", "--reset", "--yes")
	if code != 3 {
		t.Fatalf("마커 소멸 후 reset exit=%d, want 3; out=%s", code, out)
	}
}
