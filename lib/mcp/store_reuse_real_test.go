// store_reuse_real_test.go — v1.6.2 R33-3 F2 실스토어 slow 회귀:
// MCP 도구 호출이 서버 수명 store(injectedStore 래퍼)를 통과해 실DB에서
// ① 저신뢰 write → pending 적립 ② flint_approve 승격(active 전이)
// ③ 동일 payload 재전송 duplicate {id,topic,state} 에코
// ④ flint_read read_events source='mcp' 계측까지 완결되는지 검증한다.
// 기존 fake seam 테스트(tools_test.go)는 래퍼 자체 단언이라 이 결함을
// 놓쳤다 — 래퍼 통과 실경로만 잡는다. FLINT_TEST_DSN(격리 테스트 DB)
// 미설정 시 스킵.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/cli"
)

// realTestDSN — 격리 테스트 DB DSN(FLINT_TEST_DSN만 수용 — 운영
// FLINT_DSN 폴백 없음, lib/db testutil 관례).
func realTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Skip("no test DSN: set FLINT_TEST_DSN to an isolated test database DSN")
	}
	return dsn
}

// TestMCPCapabilityRoundtripRealSlow — 래퍼 통과 실스토어 왕복(slow).
// 합성 데모 데이터만 사용(개인 데이터 금지 — 저장소 최상위 원칙).
func TestMCPCapabilityRoundtripRealSlow(t *testing.T) {
	dsn := realTestDSN(t)
	t.Setenv("FLINT_DSN", dsn)
	resetStoreCache(t) // 기본 seam(storeSupply=cli.OpenServerStore) 경로

	ctx := context.Background()
	st, err := cli.OpenServerStore()
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(st.Close)

	uniq := fmt.Sprintf("demo mcp wrapper slow %d", time.Now().UnixNano())
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic = $1`, uniq)
	}
	cleanup()
	t.Cleanup(cleanup)

	payload := map[string]any{
		"topic": uniq, "summary": "합성 데모 slow 본문 — 래퍼 실경로 회귀", "confidence": 0.1,
	}

	// ① 저신뢰(0.1 < conf_floor 0.8) write → pending 적립.
	out, code := DispatchTool("flint_write", map[string]any{"kind": "knowledge", "payload": payload})
	if code != 0 || !strings.Contains(out, `"status":"committed"`) || !strings.Contains(out, `"state":"pending"`) {
		t.Fatalf("① write code=%d out=%q, want committed/pending", code, out)
	}
	var committed struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &committed); err != nil || committed.ID == 0 {
		t.Fatalf("① write 응답 id 파싱 실패: %v (%s)", err, out)
	}
	id := committed.ID

	// ② flint_approve 승격 — 래퍼가 approveStore를 차단하면 exit 1.
	out, code = DispatchTool("flint_approve", map[string]any{"id": id})
	if code != 0 || !strings.Contains(out, `"status":"approved"`) || !strings.Contains(out, fmt.Sprintf(`"id":%d`, id)) {
		t.Fatalf("② approve code=%d out=%q, want approved id=%d", code, out, id)
	}
	rows, err := st.Query(ctx, `SELECT status FROM knowledge WHERE id = $1`, id)
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0][0]) != "active" {
		t.Fatalf("② 승격 미반영: %v (%v)", rows, err)
	}

	// ③ 동일 payload 재전송 → duplicate {id,topic,state} 에코.
	out, code = DispatchTool("flint_write", map[string]any{"kind": "knowledge", "payload": payload})
	if code != 0 {
		t.Fatalf("③ 재전송 code=%d out=%q, want 0", code, out)
	}
	for _, want := range []string{`"status":"duplicate"`, fmt.Sprintf(`"id":%d`, id), `"topic":` + jsonStr(t, uniq), `"state":"active"`} {
		if !strings.Contains(out, want) {
			t.Errorf("③ duplicate 응답에 %s 누락: %q", want, out)
		}
	}

	// ④ flint_read → read_events source='mcp' 계측 부활(래퍼 차단 시 증가 없음).
	before := countReadEvents(t, st, ctx)
	out, code = DispatchTool("flint_read", map[string]any{"query": uniq})
	if code != 0 {
		t.Fatalf("④ read code=%d out=%q, want 0", code, out)
	}
	after := countReadEvents(t, st, ctx)
	if after <= before {
		t.Fatalf("④ read_events source=mcp 미증가: before=%d after=%d (래퍼가 RecordReadEvent 차단)", before, after)
	}
}

// countReadEvents — source='mcp' read_events 행 수.
func countReadEvents(t *testing.T, st interface {
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
}, ctx context.Context) int {
	t.Helper()
	rows, err := st.Query(ctx, `SELECT count(*) FROM read_events WHERE source = 'mcp'`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read_events 조회 실패: %v (%v)", rows, err)
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		t.Fatalf("count 파싱 실패: %v", err)
	}
	return n
}

// jsonStr — 단정용 JSON 문자열 리터럴.
func jsonStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
