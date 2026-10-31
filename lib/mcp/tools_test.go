// tools_test.go — 도구 3종 왕복 테스트. runFn seam: 기본 fake,
// SUPERLITE_REAL=1이면 실 cli.Run 위임(인자 기록은 유지, 실DB 미기동 시 스킵).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"flint/lib/db"
)

// call — runFn 호출 기록(조립된 CLI 인자 + stdin).
type call struct {
	args  []string
	stdin string
}

// installRun — runFn 교체. 기본 fake(고정 출력·코드 반환), SUPERLITE_REAL=1이면
// 실 cli.Run을 감싸 인자만 기록. 반환값 real은 출력 단정 가능 여부.
func installRun(t *testing.T, out string, code int) (*[]call, bool) {
	t.Helper()
	var calls []call
	real := os.Getenv("SUPERLITE_REAL") == "1"
	if real {
		pool, err := db.Connect(context.Background(), os.Getenv("FLINT_DSN"))
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		pool.Close()
	}
	prev := runFn
	runFn = func(args []string, stdin io.Reader, stdout io.Writer) int {
		b, _ := io.ReadAll(stdin)
		calls = append(calls, call{args: args, stdin: string(b)})
		if real {
			return prev(args, bytes.NewReader(b), stdout)
		}
		fmt.Fprint(stdout, out)
		return code
	}
	t.Cleanup(func() { runFn = prev })
	return &calls, real
}

// wantArgs — 기록된 첫 호출의 CLI 인자 단정.
func wantArgs(t *testing.T, calls *[]call, want []string) {
	t.Helper()
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	if got := (*calls)[0].args; !equal(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestToolsManifest — 도구 3종 정의: 이름·설명·스키마 존재.
func TestToolsManifest(t *testing.T) {
	tools := ToolsManifest()
	if len(tools) != 3 {
		t.Fatalf("len(ToolsManifest) = %d, want 3", len(tools))
	}
	want := []string{"flint_read", "flint_write", "flint_hot"}
	for i, td := range tools {
		if td.Name != want[i] {
			t.Errorf("tools[%d].Name = %q, want %q", i, td.Name, want[i])
		}
		if td.Description == "" {
			t.Errorf("tools[%d].Description empty", i)
		}
		if td.InputSchema == nil {
			t.Errorf("tools[%d].InputSchema nil", i)
		}
	}
}

// TestDispatchReadRoundtrip — flint_read{query,tag,limit} → cli read 조립·출력/코드 전달.
func TestDispatchReadRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"rows":[]}`, 0)
	out, code := DispatchTool("flint_read", map[string]any{
		"query": "인코딩 테스트", "tag": "go", "limit": 3,
	})
	wantArgs(t, calls, []string{"read", "--tag", "go", "--limit", "3", "--", "인코딩 테스트"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != `{"rows":[]}` {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchReadLimitBudget — 토큰 예산: limit 상한 50이 도구에도 적용된다.
func TestDispatchReadLimitBudget(t *testing.T) {
	calls, _ := installRun(t, "", 0)
	DispatchTool("flint_read", map[string]any{"query": "x", "limit": 100})
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	for i, a := range (*calls)[0].args {
		if a == "--limit" {
			if got := (*calls)[0].args[i+1]; got != "50" {
				t.Errorf("clamped limit = %q, want \"50\"", got)
			}
			return
		}
	}
	t.Error("--limit not passed")
}

// TestDispatchWriteRoundtrip — flint_write{kind,payload} → cli write 조립·stdin JSON.
func TestDispatchWriteRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"status":"committed","id":1}`, 0)
	out, code := DispatchTool("flint_write", map[string]any{
		"kind": "knowledge",
		"payload": map[string]any{
			"topic": "t", "summary": "s", "confidence": 0.8,
		},
	})
	wantArgs(t, calls, []string{"write", "knowledge", "--source", "mcp"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != `{"status":"committed","id":1}` {
		t.Errorf("out = %q, want fake passthrough", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte((*calls)[0].stdin), &got); err != nil {
		t.Fatalf("stdin not JSON: %v", err)
	}
	if got["topic"] != "t" {
		t.Errorf("stdin topic = %v, want t", got["topic"])
	}
}

// TestDispatchWriteIdempotent — 동일 도구 호출은 동일 CLI 조립(멱등 위임).
func TestDispatchWriteIdempotent(t *testing.T) {
	calls, _ := installRun(t, `{"status":"duplicate"}`, 0)
	args := map[string]any{
		"kind":    "knowledge",
		"payload": map[string]any{"topic": "t", "summary": "s"},
	}
	DispatchTool("flint_write", args)
	DispatchTool("flint_write", args)
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(*calls))
	}
	if !equal((*calls)[0].args, (*calls)[1].args) || (*calls)[0].stdin != (*calls)[1].stdin {
		t.Error("repeat dispatch assembled different CLI args/stdin")
	}
}

// TestDispatchWriteMissingKind — kind 누락은 스키마 오류(exit 3).
func TestDispatchWriteMissingKind(t *testing.T) {
	installRun(t, "", 0)
	out, code := DispatchTool("flint_write", map[string]any{"payload": map[string]any{}})
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
	if !strings.Contains(out, "kind") {
		t.Errorf("out = %q, want kind field error", out)
	}
}

// TestDispatchHotRoundtrip — flint_hot{session_id} → cli hot 조립.
func TestDispatchHotRoundtrip(t *testing.T) {
	calls, real := installRun(t, "hot summary", 0)
	out, code := DispatchTool("flint_hot", map[string]any{"session_id": "s1"})
	wantArgs(t, calls, []string{"hot", "--session", "s1"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != "hot summary" {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchUnknownTool — 미정의 도구는 한 줄 에러 JSON·exit 1.
func TestDispatchUnknownTool(t *testing.T) {
	installRun(t, "", 0)
	out, code := DispatchTool("flint_nope", nil)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("out = %q, want unknown tool error", out)
	}
}

// simpleQuery — fakeSimpleStore가 기록한 쿼리 1건.
type simpleQuery struct {
	sql  string
	args []any
}

// fakeSimpleStore — connectSimple fake: DSN·쿼리·Close 기록, 고정 행 반환.
type fakeSimpleStore struct {
	dsn     string
	rows    [][]any
	closed  bool
	queries []simpleQuery
}

func (f *fakeSimpleStore) Query(_ context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, simpleQuery{sql: sql, args: args})
	return f.rows, nil
}

func (f *fakeSimpleStore) Close() { f.closed = true }

// seamRealDSN — 실구현 seam의 DSN 해결(N11): FLINT_TEST_DSN 설정 시만
// 실DB 접속을 허용한다. 미설정이면 운영 기본 DSN으로 폴백하지 않고
// ("", false) — 호출원은 fake 경로로 내려간다.
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

// installSimple — connectSimple 교체. 기본 fake(DSN·쿼리 기록),
// FLINT_TEST_DSN 설정 + SUPERLITE_REAL=1이면 실구현에 DSN만 위임 기록
// (실DB 미기동 시 스킵). FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(N11).
func installSimple(t *testing.T, rows [][]any) (*fakeSimpleStore, bool) {
	t.Helper()
	fake := &fakeSimpleStore{rows: rows}
	dsn, real := seamRealDSN()
	if real {
		pool, err := db.Connect(context.Background(), dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		pool.Close()
	}
	prev := connectSimple
	connectSimple = func(ctx context.Context, dsn string) (Store, error) {
		fake.dsn = dsn
		if real {
			return prev(ctx, dsn)
		}
		return fake, nil
	}
	t.Cleanup(func() { connectSimple = prev })
	return fake, real
}

// TestSimpleStoreQuery — NewSimpleStore는 DSN에 simple protocol 파라미터를
// 붙이고(장수 세션 뷰 describe 캐시 우회), placeholder 인자($1)가 그대로 전달된다.
// 실DB는 FLINT_TEST_DSN 설정 시만 — 미설정 시 스킵(운영 기본 접속 금지, N11).
func TestSimpleStoreQuery(t *testing.T) {
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Skip("FLINT_TEST_DSN 미설정 — 운영 기본 DSN 접속 금지(N11)")
	}
	fake, real := installSimple(t, [][]any{{1}})
	st, err := NewSimpleStore(dsn)
	if err != nil {
		t.Fatalf("NewSimpleStore: %v", err)
	}
	if !strings.Contains(fake.dsn, "default_query_exec_mode=simple_protocol") {
		t.Errorf("dsn = %q, want simple_protocol param", fake.dsn)
	}
	if _, err := st.Query(context.Background(),
		`SELECT id FROM active_knowledge WHERE topic = $1`, "e2e-mcp"); err != nil {
		t.Fatalf("Query placeholder in simple mode: %v", err)
	}
	st.Close()
	if !real {
		if len(fake.queries) != 1 || len(fake.queries[0].args) != 1 ||
			fake.queries[0].args[0] != "e2e-mcp" {
			t.Errorf("queries = %+v, want placeholder arg passthrough", fake.queries)
		}
		if !fake.closed {
			t.Error("Close not recorded")
		}
	}
}

// TestSimpleStoreDSNSeparator — DSN에 이미 쿼리 파라미터가 있으면 &로 붙인다.
// 순수 문자열 테스트: 실접속 없음(SUPERLITE_REAL과 무관).
func TestSimpleStoreDSNSeparator(t *testing.T) {
	var got string
	prev := connectSimple
	connectSimple = func(_ context.Context, dsn string) (Store, error) {
		got = dsn
		return &fakeSimpleStore{}, nil
	}
	t.Cleanup(func() { connectSimple = prev })
	if _, err := NewSimpleStore("postgresql://u:p@h:5433/d?sslmode=disable"); err != nil {
		t.Fatalf("NewSimpleStore: %v", err)
	}
	want := "postgresql://u:p@h:5433/d?sslmode=disable&default_query_exec_mode=simple_protocol"
	if got != want {
		t.Errorf("dsn = %q, want %q", got, want)
	}
}
