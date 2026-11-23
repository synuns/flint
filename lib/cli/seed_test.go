// seed_test.go — `flint seed --demo [--reset] [--yes]` (v1.26.0 T2).
// Fast tests cover the isolated-DB derivation (file contract), the --reset
// ownership guard (state contract), and the setup wording swap; the real-DB
// slow test covers the seeding idempotence (state contract) end to end on a
// dedicated scratch demo database that is dropped on exit.
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

// withSeedStoreRecorder — swaps newStore, records the DSN each call received,
// and returns the fake store handed out (the seed command must connect to the
// derived demo DSN, never the production one).
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

// withSeedWriteRecorder — swaps seedWriteFn; each call's args and stdin are
// recorded and the fake prints one {"status":...} JSON line per call.
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

// seedWriteCall — one captured seedWriteFn invocation.
type seedWriteCall struct {
	args  []string
	stdin string
}

// withSeedEnsureRecorder — swaps seedEnsureDBFn, recording (adminDSN, dbName).
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

// runSeedArgs — direct handler invocation (the command is store-free at the
// dispatch layer; it opens its own demo store through the newStore seam).
func runSeedArgs(args ...string) (string, int) {
	var b strings.Builder
	code := runSeed(context.Background(), nil, args, strings.NewReader(""), &b)
	return b.String(), code
}

// TestSeedRegistered — the init() Register (the usage-surface contract).
func TestSeedRegistered(t *testing.T) {
	if _, ok := Lookup("seed"); !ok {
		t.Fatal("Lookup(\"seed\") 실패 — 미등록 커맨드")
	}
}

// TestDeriveDemoDSN — the isolated-DB derivation (file contract): the
// production DSN's database name gets the fixed _demo suffix; query params
// survive; a missing database name or an unparseable DSN is an error (exit 3
// at the command layer).
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

// TestSeedDeriveFailExit3 — derivation failure (no db name) is invalid-arg
// exit 3 and the message points at the FLINT_DEMO_DSN override.
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

// TestSeedRequiresDemoFlag — bare `seed` (or --reset without --demo) is a
// usage error exit 3 (the Makefile seed target keeps working; the CLI entry
// is explicitly --demo).
func TestSeedRequiresDemoFlag(t *testing.T) {
	for _, args := range [][]string{{}, {"--reset"}, {"--yes"}} {
		out, code := runSeedArgs(args...)
		if code != 3 {
			t.Fatalf("%v: exit=%d, want 3; out=%s", args, code, out)
		}
		decode(t, out)
	}
}

// TestSeedUnknownFlag — unknown flags are invalid-arg exit 3.
func TestSeedUnknownFlag(t *testing.T) {
	out, code := runSeedArgs("--demo", "--bogus")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)
}

// TestSeedDemoWritesPrefixedPayloads — the happy path: the store comes from
// the derived demo DSN, the embedded lines are seeded through the single
// RunWrite path with the demo/ identifier prefix applied to every topic and
// project name, and the demo_owner marker meta is written (the --reset
// ownership guard depends on it).
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
	// every embedded line is passed down exactly once, kind-grouped, prefixed.
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
	// the demo_owner marker upsert went through the single meta KV entry.
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

// TestSeedDemoDSNOverride — FLINT_DEMO_DSN replaces the derivation verbatim
// (the isolation ruling's escape hatch).
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

// TestSeedEnsureDBOnConnectFail — when the demo database does not exist yet,
// the ensure seam runs against the base DSN with the derived demo database
// name and the connection is retried once.
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

// seedResetFake — a fakeStore answering the demo_owner marker lookup and the
// two target-count lookups (2 knowledge rows + 1 project row with the demo
// prefix). DELETEs return empty rows; every query argument is recorded.
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

// TestSeedResetOwnerMismatchExit3 — a database without the demo_owner marker
// (or with a foreign value) is not ours: --reset refuses with exit 3 even
// with --yes (the destructive guard).
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

// TestSeedResetDryRun — --reset without --yes reports the prefix-identified
// targets only (status dry_run, exit 0, no DELETE issued).
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

// TestSeedResetYesDeletes — with --yes only the fixed demo identifiers are
// deleted: knowledge topics and project names carrying the demo/ prefix, plus
// the demo_owner marker itself. exit 0.
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

// TestSetupNextMentionsSeedDemo — the setup guidance swap: the step and
// next-step wording point at `flint seed --demo`, not `make seed` (the
// binary-user demo route is now the CLI itself).
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

// TestSeedDemoRealSlow — real-DB slow round trip (SUPERLITE_REAL=1 +
// FLINT_TEST_DSN required): a dedicated scratch demo database derived from
// the test DSN (never the production DSN, never a shared database) is created,
// seeded twice (the second run must be all-duplicate exit 0 — the idempotence
// contract through the single RunWrite path), reset via the ownership guard,
// and dropped on exit.
func TestSeedDemoRealSlow(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	// the derivation base is the scratch test DSN — the production DSN is
	// never consulted (FLINT_DSN override, FLINT_DEMO_DSN cleared).
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
		_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+dbName) // dbName derived from the DSN, no separators
	}
	dropDemo() // pre-clean residue from an aborted earlier run
	t.Cleanup(dropDemo)

	// 1st run — full seed on the fresh demo database.
	out, code := runSeedArgs("--demo")
	if code != 0 {
		t.Fatalf("1회차 exit=%d; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "ok" || m["database"] != dbName {
		t.Fatalf("1회차 요약: %s", out)
	}

	// 2nd run — idempotent: every kind reports duplicate, exit 0.
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

	// content check through a store on the demo DSN.
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

	// reset dry-run — no deletion.
	out, code = runSeedArgs("--demo", "--reset")
	if code != 0 || decode(t, out)["status"] != "dry_run" {
		t.Fatalf("reset dry-run 실패: exit=%d out=%s", code, out)
	}
	// reset --yes — prefix-identified rows deleted, marker cleared.
	out, code = runSeedArgs("--demo", "--reset", "--yes")
	if code != 0 {
		t.Fatalf("reset --yes exit=%d; out=%s", code, out)
	}
	// reset again — the marker is gone: owner mismatch exit 3.
	out, code = runSeedArgs("--demo", "--reset", "--yes")
	if code != 3 {
		t.Fatalf("마커 소멸 후 reset exit=%d, want 3; out=%s", code, out)
	}
}
