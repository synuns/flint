// tools_test.go — tool roundtrip tests (the five tools). runFn seam: a
// fake by default; SUPERLITE_REAL=1 delegates to the real cli.Run
// (argument recording is kept; skipped when the real DB is not running).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// call — a runFn call record (assembled CLI args + stdin).
type call struct {
	args  []string
	stdin string
}

// installRun — swaps runFn. A fake by default (returns fixed output and
// code); with SUPERLITE_REAL=1 it wraps the real cli.Run and records only
// the arguments. The returned real reports whether output can be asserted.
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

// wantArgs — asserts the CLI args of the first recorded call.
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

// TestToolsManifest — the five tool definitions: name, description, and schema present.
func TestToolsManifest(t *testing.T) {
	tools := ToolsManifest()
	if len(tools) != 5 {
		t.Fatalf("len(ToolsManifest) = %d, want 5", len(tools))
	}
	want := []string{"flint_read", "flint_write", "flint_hot", "flint_pending", "flint_approve"}
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

// TestHotManifestHasNoSessionID — flint_hot's session_id is a query-neutral
// no-op (HotSQL and HotHeadlineSQL are both gone), so it is removed from
// the manifest advertisement (advertised-but-dropped mismatch — the real
// implementation excludes it as a new feature).
func TestHotManifestHasNoSessionID(t *testing.T) {
	for _, td := range ToolsManifest() {
		if td.Name != "flint_hot" {
			continue
		}
		props, ok := td.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("flint_hot InputSchema.properties가 맵 아님: %#v", td.InputSchema["properties"])
		}
		if _, has := props["session_id"]; has {
			t.Error("flint_hot 매니페스트에 session_id 광고 잔존 — no-op 파라미터")
		}
		if strings.Contains(td.Description, "session_id") {
			t.Errorf("flint_hot 설명에 session_id 언급 잔존: %q", td.Description)
		}
		return
	}
	t.Fatal("flint_hot 도구 정의 없음")
}

// TestToolManifestEnglish — tool descriptions and parameter descriptions
// target LLM consumers, so they are English (asserts no Hangul
// characters).
func TestToolManifestEnglish(t *testing.T) {
	hangul := regexp.MustCompile(`[가-힣]`)
	for _, td := range ToolsManifest() {
		if hangul.MatchString(td.Description) {
			t.Errorf("%s Description에 한글 잔존: %q", td.Name, td.Description)
		}
		props, ok := td.InputSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		for key, p := range props {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if d, ok := m["description"].(string); ok && hangul.MatchString(d) {
				t.Errorf("%s 파라미터 %q description에 한글 잔존: %q", td.Name, key, d)
			}
		}
	}
}

// TestDispatchReadRoundtrip — flint_read{query,tag,limit} -> cli read
// assembly and output/code passthrough.
func TestDispatchReadRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"rows":[]}`, 0)
	out, code := DispatchTool("flint_read", map[string]any{
		"query": "인코딩 테스트", "tag": "go", "limit": 3,
	})
	wantArgs(t, calls, []string{"read", "--json", "--source", "mcp", "--tag", "go", "--limit", "3", "--", "인코딩 테스트"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != `{"rows":[]}` {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchReadLimitBudget — token budget: the limit cap of 50 applies to the tool too.
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

// TestDispatchWriteRoundtrip — flint_write{kind,payload} -> cli write
// assembly and stdin JSON.
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

// TestDispatchWriteIdempotent — identical tool calls assemble an identical CLI invocation (idempotent delegation).
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

// TestDispatchWriteMissingKind — a missing kind is a schema error (exit 3).
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

// TestDispatchHotRoundtrip — flint_hot -> cli hot assembly (no arguments).
func TestDispatchHotRoundtrip(t *testing.T) {
	calls, real := installRun(t, "hot summary", 0)
	out, code := DispatchTool("flint_hot", nil)
	wantArgs(t, calls, []string{"hot"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != "hot summary" {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchUnknownTool — an unknown tool yields one error JSON line and exit 1.
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

// v1.18.0 T1 — ToolDef.OutputSchema: all five tools declare an outputSchema
// describing the ACTUAL output shape (not the aspirational one).
func TestToolsManifestOutputSchemas(t *testing.T) {
	// Per-tool property keys each schema must declare (hot advertises only
	// the minimal meta — its output is non-JSON text).
	wantProps := map[string][]string{
		"flint_write":   {"status", "hash", "state", "id"},
		"flint_hot":     {},
		"flint_pending": {"status", "items", "counts", "next"},
		"flint_approve": {"status", "id"},
	}
	for _, td := range ToolsManifest() {
		if len(td.OutputSchema) == 0 {
			t.Fatalf("%s OutputSchema 없음", td.Name)
		}
		var s map[string]any
		if err := json.Unmarshal(td.OutputSchema, &s); err != nil {
			t.Fatalf("%s outputSchema not valid JSON: %v — %s", td.Name, err, td.OutputSchema)
		}
		if _, hasType := s["type"]; !hasType {
			t.Errorf("%s outputSchema에 type 없음: %s", td.Name, td.OutputSchema)
		}
		if td.Name == "flint_read" {
			// The read structuredContent is an object wrapping {results:[...]} (R67).
			if s["type"] != "object" {
				t.Errorf("flint_read outputSchema.type = %v, want object (results wrapper)", s["type"])
			}
			props, ok := s["properties"].(map[string]any)
			if !ok {
				t.Fatalf("flint_read outputSchema.properties 없음: %s", td.OutputSchema)
			}
			results, ok := props["results"].(map[string]any)
			if !ok {
				t.Fatalf("flint_read outputSchema.properties.results 없음: %s", td.OutputSchema)
			}
			rowProps, _ := results["items"].(map[string]any)["properties"].(map[string]any)
			for _, k := range []string{"id", "topic", "summary"} {
				if _, ok := rowProps[k]; !ok {
					t.Errorf("flint_read results.items.properties에 %s 누락: %s", k, td.OutputSchema)
				}
			}
			continue
		}
		props, _ := s["properties"].(map[string]any)
		for _, k := range wantProps[td.Name] {
			if _, ok := props[k]; !ok {
				t.Errorf("%s outputSchema.properties에 %s 누락: %s", td.Name, k, td.OutputSchema)
			}
		}
	}
}

// v1.18.0 T2 — a single-line JSON object output (write/approve/pending
// shape) is attached as structuredContent with the content text kept as-is.
func TestToolsCallStructuredContentJSONObject(t *testing.T) {
	raw := `{"status":"committed","hash":"abc123","state":"active","id":1}`
	_, real := installRun(t, raw, 0)
	if real {
		t.Skip("real runFn: 실DB 출력값 의존 단언은 fake 경로 담당")
	}
	res, err := handleRPC("tools/call", map[string]any{
		"name":      "flint_write",
		"arguments": map[string]any{"kind": "knowledge", "payload": map[string]any{"topic": "t", "summary": "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	sc, ok := m["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent 누락: %#v", m)
	}
	if sc["status"] != "committed" || sc["hash"] != "abc123" || sc["id"] != float64(1) {
		t.Errorf("structuredContent = %v, want committed 객체", sc)
	}
	content, _ := m["content"].([]map[string]any)
	if len(content) != 1 || content[0]["text"] != raw {
		t.Errorf("content text 원문 유지 위반: %#v", m["content"])
	}
	if m["isError"] != false {
		t.Errorf("isError = %v, want false", m["isError"])
	}
}

// v1.18.0 T2 — a multi-line JSONL output is parsed per line, assembled into
// an array, and wrapped as {results:[...]} (the read rows shape).
func TestToolsCallStructuredContentJSONLWrapped(t *testing.T) {
	raw := `{"id":"1","topic":"demo one","summary":"s1"}` + "\n" +
		`{"id":"2","topic":"demo two","summary":"s2"}`
	_, real := installRun(t, raw, 0)
	if real {
		t.Skip("real runFn: 실DB 검색 결과 의존 단언은 fake 경로 담당")
	}
	res, err := handleRPC("tools/call", map[string]any{
		"name":      "flint_read",
		"arguments": map[string]any{"query": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	sc, ok := m["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent 누락: %#v", m)
	}
	results, ok := sc["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 2행 배열", sc["results"])
	}
	if row := results[0].(map[string]any); row["topic"] != "demo one" {
		t.Errorf("results[0].topic = %v, want demo one", row["topic"])
	}
	content, _ := m["content"].([]map[string]any)
	if len(content) != 1 || content[0]["text"] != raw {
		t.Errorf("content text 원문(JSONL) 유지 위반: %#v", m["content"])
	}
}

// v1.18.0 T2 — a non-JSON output (flint_hot text) omits structuredContent;
// the content text alone is returned (spec SHOULD compatibility).
func TestToolsCallStructuredContentOmittedForNonJSON(t *testing.T) {
	installRun(t, "hot 요약 데모 (비JSON 텍스트)", 0)
	res, err := handleRPC("tools/call", map[string]any{
		"name":      "flint_hot",
		"arguments": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if _, has := m["structuredContent"]; has {
		t.Errorf("비JSON 출력에 structuredContent 존재: %v", m["structuredContent"])
	}
	if m["isError"] != false {
		t.Errorf("isError = %v, want false", m["isError"])
	}
}

// v1.18.0 T2 — parse failures and non-object JSON (arrays, scalars) omit
// structuredContent (the spec requires an object; content text alone).
func TestStructuredContentNonObjectOmitted(t *testing.T) {
	for _, tc := range []struct {
		tool string
		out  string
	}{
		{"flint_hot", "[1,2]"},                           // single-line array
		{"flint_hot", `"scalar"`},                        // single-line scalar
		{"flint_read", `{"id":"1"}` + "\n" + "not-json"}, // one broken line
		{"flint_read", ""},                               // empty (zero-hit silence)
	} {
		if sc := structuredContent(tc.tool, tc.out); sc != nil {
			t.Errorf("structuredContent(%q, %q) = %v, want nil", tc.tool, tc.out, sc)
		}
	}
}

// v1.18.0 T2 — the isError mapping is unchanged by structuredContent: an
// error output (exit 3 + one JSON error line) is still isError:true.
func TestToolsCallIsErrorInvariant(t *testing.T) {
	raw := `{"error":"schema","field":"id","expect":"positive integer"}`
	_, real := installRun(t, raw, 3)
	if real {
		t.Skip("real runFn: 실DB 종료코드 의존 단언은 fake 경로 담당")
	}
	res, err := handleRPC("tools/call", map[string]any{
		"name":      "flint_read",
		"arguments": map[string]any{"query": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["isError"] != true {
		t.Errorf("isError = %v, want true(exit 3 + 출력)", m["isError"])
	}
	if sc, ok := m["structuredContent"].(map[string]any); !ok || sc["field"] != "id" {
		t.Errorf("structuredContent = %v, want 파싱된 에러 객체", m["structuredContent"])
	}
}

// v1.18.0 T3 — dispatchRead assembles --json internally (the MCP read path
// always returns machine-readable rows).
func TestDispatchReadAssemblesJSONFlag(t *testing.T) {
	calls, _ := installRun(t, `{"hits":0}`, 0)
	DispatchTool("flint_read", map[string]any{"query": "데모"})
	wantArgs(t, calls, []string{"read", "--json", "--source", "mcp", "--", "데모"})
}

// v1.18.0 T3 — the read roundtrip keeps the content text as the raw JSONL
// and structuredContent assembles {results:[rows]}.
func TestDispatchReadStructuredRoundtrip(t *testing.T) {
	raw := `{"id":"1","topic":"데모 주제","summary":"합성 요약"}` + "\n" +
		`{"id":"2","topic":"데모 두번째","summary":"합성 요약2"}`
	_, real := installRun(t, raw, 0)
	if real {
		t.Skip("real runFn: 실DB 검색 결과 의존 단언은 fake 경로 담당")
	}
	out, code := DispatchTool("flint_read", map[string]any{"query": "데모"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out != raw {
		t.Errorf("content(JSONL 원문) 불변 위반: %q", out)
	}
	sc := structuredContent("flint_read", out)
	if sc == nil {
		t.Fatal("structuredContent nil")
	}
	results, ok := sc["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 2행", sc["results"])
	}
	if row := results[1].(map[string]any); row["topic"] != "데모 두번째" {
		t.Errorf("results[1].topic = %v, want 데모 두번째", row["topic"])
	}
}

// v1.18.0 T3 — a one-row read output still wraps into {results:[row]} (the
// read outputSchema advertises an array of rows).
func TestStructuredContentReadSingleRowWrapped(t *testing.T) {
	sc := structuredContent("flint_read", `{"id":"9","topic":"데모 유일행","summary":"s"}`)
	results, ok := sc["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %v, want 1행 래핑", sc)
	}
	if row := results[0].(map[string]any); row["id"] != "9" {
		t.Errorf("results[0].id = %v, want 9", row["id"])
	}
}

// v1.18.0 T3 — the zero-hit line {"hits":0} stays an object as-is (no
// results wrapping).
func TestStructuredContentReadZeroHitStaysObject(t *testing.T) {
	sc := structuredContent("flint_read", `{"hits":0}`)
	if sc == nil || sc["hits"] != float64(0) {
		t.Errorf("zero-hit structuredContent = %v, want {hits:0} 그대로", sc)
	}
	if _, has := sc["results"]; has {
		t.Errorf("zero-hit에 results 래핑 잘못 적용: %v", sc)
	}
}

// capStore — regression fake: a wire.Store plus all optional capabilities
// implemented. Verifies, through the server-lifetime store (the
// cli.RunWithStore injectedStore wrapper), that capability assertions
// delegate to the original.
type capStore struct {
	wire.HistoryStubs
	existsHash bool
	dupMeta    knowledge.DupMeta
	approved   []int
	readEvents []db.ReadEvent
	usage      []string
	commits    int
}

func (f *capStore) Migrate(ctx context.Context) error { return nil }
func (f *capStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return []int{5}, nil
}
func (f *capStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return f.existsHash, nil
}
func (f *capStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *capStore) Commit(ctx context.Context, msg string) (string, error) {
	f.commits++
	return "abc123", nil
}
func (f *capStore) Ping(ctx context.Context) error { return nil }
func (f *capStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return nil, nil // empty meta rows -> default-threshold fallback
}
func (f *capStore) Close() {}

// Real implementations of the optional capabilities (captures for verifying delegation reaches the original).
func (f *capStore) Approve(ctx context.Context, id int) error {
	f.approved = append(f.approved, id)
	return nil
}
func (f *capStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {
	return f.dupMeta, f.existsHash, nil
}
func (f *capStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return f.dupMeta.ID, f.existsHash, nil
}
func (f *capStore) RecordUsage(ctx context.Context, kind, source string) error {
	f.usage = append(f.usage, kind+"/"+source)
	return nil
}
func (f *capStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	f.readEvents = append(f.readEvents, ev)
	return nil
}

var _ wire.Store = (*capStore)(nil)

// installServerStore — swaps the server-lifetime store supply seam to a
// capStore (runFn and runWithStoreFn stay at the real implementations —
// the whole wrapper path is exercised).
func installServerStore(t *testing.T, f *capStore) {
	t.Helper()
	resetStoreCache(t)
	prev := storeSupply
	storeSupply = func() (wire.Store, error) { return f, nil }
	t.Cleanup(func() { storeSupply = prev })
}

// TestDispatchApprovePromotesViaServerStore — F1: flint_approve reaches the
// Approve of the store injected through the wrapper and produces the
// promotion response (the failure mode this guards: the wrapper blocked
// the approveStore assertion and fell back to "does not implement Approve"
// exit 1).
func TestDispatchApprovePromotesViaServerStore(t *testing.T) {
	f := &capStore{}
	installServerStore(t, f)
	out, code := DispatchTool("flint_approve", map[string]any{"id": 7})
	if code != 0 {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	if out != `{"status":"approved","id":7}` {
		t.Fatalf("out = %q, want approved JSON", out)
	}
	if len(f.approved) != 1 || f.approved[0] != 7 {
		t.Fatalf("Approve 위임 미도달: %v", f.approved)
	}
}

// TestDispatchWriteDuplicateEchoesMetaViaServerStore — F2: the duplicate
// response for a re-sent identical payload includes the existing row's
// {id,topic,state} (if the wrapper blocked the DupMetaStore assertion, it
// would degrade to a bare {"status":"duplicate"}).
func TestDispatchWriteDuplicateEchoesMetaViaServerStore(t *testing.T) {
	f := &capStore{}
	installServerStore(t, f)
	payload := map[string]any{
		"topic": "demo mcp wrapper", "summary": "합성 데모 — 래퍼 위임 회귀", "confidence": 0.9,
	}
	out, code := DispatchTool("flint_write", map[string]any{"kind": "knowledge", "payload": payload})
	if code != 0 || !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("1회차 code=%d out=%q, want committed", code, out)
	}
	f.existsHash = true
	f.dupMeta = knowledge.DupMeta{ID: 5, Topic: "demo mcp wrapper", State: "active"}
	out, code = DispatchTool("flint_write", map[string]any{"kind": "knowledge", "payload": payload})
	if code != 0 {
		t.Fatalf("2회차 code = %d, want 0; out=%s", code, out)
	}
	for _, want := range []string{`"status":"duplicate"`, `"id":5`, `"topic":"demo mcp wrapper"`, `"state":"active"`} {
		if !strings.Contains(out, want) {
			t.Errorf("duplicate 응답에 %s 누락: %q", want, out)
		}
	}
}

// TestDispatchReadRecordsReadEventSourceMCP — flint_read reaches the
// read_events metering (RecordReadEvent) with source=mcp (if the wrapper
// blocked the ReadEventStore assertion, the metering would silently
// disappear).
func TestDispatchReadRecordsReadEventSourceMCP(t *testing.T) {
	f := &capStore{}
	installServerStore(t, f)
	out, code := DispatchTool("flint_read", map[string]any{"query": "demo"})
	if code != 0 {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	if len(f.readEvents) != 1 {
		t.Fatalf("RecordReadEvent 위임 미도달: %d건", len(f.readEvents))
	}
	if got := f.readEvents[0].Source; got != "mcp" {
		t.Fatalf("read_events.source = %q, want mcp", got)
	}
}
