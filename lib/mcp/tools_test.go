// tools_test.go — 도구 왕복 테스트(v0.32.0: 5종). runFn seam: 기본 fake,
// SUPERLITE_REAL=1이면 실 cli.Run 위임(인자 기록은 유지, 실DB 미기동 시 스킵).
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

// TestToolsManifest — 도구 5종 정의: 이름·설명·스키마 존재.
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

// TestHotManifestHasNoSessionID — v1.0.1 감사 F7: flint_hot의 session_id는
// 조회에 영향 없는 no-op(HotSQL·HotHeadlineSQL 전부 폐기)이므로 매니페스트
// 광고에서 제거된다(광고-폐기 불일치 — 실구현은 신규 기능으로 제외).
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

// TestToolManifestEnglish — v1.5.0 T4: 도구 description·파라미터
// description은 LLM 소비자 대상이라 영어다(한글 글자 부재 단언).
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

// TestDispatchReadRoundtrip — flint_read{query,tag,limit} → cli read 조립·출력/코드 전달.
func TestDispatchReadRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"rows":[]}`, 0)
	out, code := DispatchTool("flint_read", map[string]any{
		"query": "인코딩 테스트", "tag": "go", "limit": 3,
	})
	wantArgs(t, calls, []string{"read", "--source", "mcp", "--tag", "go", "--limit", "3", "--", "인코딩 테스트"})
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

// TestDispatchHotRoundtrip — flint_hot → cli hot 조립(v1.2.3 B6: session_id
// 잔여 제거 — 인자 없음).
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

// capStore — v1.6.2 R33-3 F1/F2 회귀 fake: wire.Store + 선택 capability
// 전분. 서버 수명 store(cli.RunWithStore injectedStore 래퍼)를 통과해
// capability 단언이 원본에 위임되는지 검증한다(v1.2.4 래퍼 도입 후
// approve 승격·duplicate 메타·read_events 계량이 래퍼에서 차단된 결함).
type capStore struct {
	existsHash bool
	dupMeta    knowledge.DupMeta
	approved   []int
	readEvents []db.ReadEvent
	usage      []string
	commits    int
}

func (f *capStore) Migrate(ctx context.Context) error              { return nil }
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
	return nil, nil // meta 조회 빈 행 → 기본 임계값 폴백
}
func (f *capStore) Close() {}

// 선택 capability 실구현(원본 위임 도달 검증용 캡처).
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

// installServerStore — 서버 수명 store 공급 seam을 capStore로 교체
// (runFn·runWithStoreFn은 기본 실구현 — 래퍼 경로 전체를 통과시킨다).
func installServerStore(t *testing.T, f *capStore) {
	t.Helper()
	resetStoreCache(t)
	prev := storeSupply
	storeSupply = func() (wire.Store, error) { return f, nil }
	t.Cleanup(func() { storeSupply = prev })
}

// TestDispatchApprovePromotesViaServerStore — F1: flint_approve가 래퍼
// 통과 주입 store의 Approve에 도달해 승격 응답을 낸다(현재 결함: 래퍼가
// approveStore 단언을 차단해 "does not implement Approve" 폴백 exit 1).
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

// TestDispatchWriteDuplicateEchoesMetaViaServerStore — F2: 동일 payload
// 재전송의 duplicate 응답이 기존 행 {id,topic,state}를 포함한다(래퍼가
// DupMetaStore 단언을 차단하면 맨몸 {"status":"duplicate"}로 퇴화).
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

// TestDispatchReadRecordsReadEventSourceMCP — F2: flint_read가 read_events
// 계층(RecordReadEvent)에 source=mcp로 도달한다(래퍼가 ReadEventStore
// 단언을 차단하면 계측이 조용히 사라진다 — v1.2.4~ 상실).
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
