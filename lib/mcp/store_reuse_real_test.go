// store_reuse_real_test.go — real-store slow regression: verifies that MCP
// tool calls, through the server-lifetime store (the injectedStore
// wrapper) and against a real DB, complete (1) a low-confidence write ->
// pending accrual, (2) flint_approve promotion (active transition),
// (3) a re-sent identical payload echoing duplicate {id,topic,state}, and
// (4) flint_read metering read_events source='mcp'. The existing fake-seam
// tests (tools_test.go) assert the wrapper itself and missed this defect —
// only the real path through the wrapper catches it. Skipped when
// FLINT_TEST_DSN (an isolated test DB) is unset.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/synuns/flint/lib/cli"
)

// realDBLock — mutual exclusion (file lock) for slow tests using the
// shared Doltgres test DB. go test runs package binaries in parallel, so
// without the lock this test's roundtrip rows can overlap with the
// `DELETE FROM knowledge` full wipe of lib/db newTestRepo (repo_test.go)
// and intermittently fail approve with not_found. The lock file path must
// match lib/db sharedDBLock and lib/backup drillLock for the exclusion to
// hold.
func realDBLock(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}
	t.Cleanup(func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// realTestDSN — the isolated test DB DSN (accepts FLINT_TEST_DSN only —
// no production FLINT_DSN fallback, per lib/db testutil convention).
func realTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Skip("no test DSN: set FLINT_TEST_DSN to an isolated test database DSN")
	}
	return dsn
}

// TestMCPCapabilityRoundtripRealSlow — a real-store roundtrip through the
// wrapper (slow). Uses synthetic demo data only (no personal data — the
// repository's top-level principle).
func TestMCPCapabilityRoundtripRealSlow(t *testing.T) {
	dsn := realTestDSN(t)
	realDBLock(t) // serializes the whole shared-test-DB usage window (blocks full-wipe races)
	t.Setenv("FLINT_DSN", dsn)
	resetStoreCache(t) // the default seam path (storeSupply = cli.OpenServerStore)

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

	// (1) Low-confidence (0.1 < conf_floor 0.8) write -> pending accrual.
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

	// (2) flint_approve promotion — if the wrapper blocked approveStore, exit 1.
	out, code = DispatchTool("flint_approve", map[string]any{"id": id})
	if code != 0 || !strings.Contains(out, `"status":"approved"`) || !strings.Contains(out, fmt.Sprintf(`"id":%d`, id)) {
		t.Fatalf("② approve code=%d out=%q, want approved id=%d", code, out, id)
	}
	rows, err := st.Query(ctx, `SELECT status FROM knowledge WHERE id = $1`, id)
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0][0]) != "active" {
		t.Fatalf("② 승격 미반영: %v (%v)", rows, err)
	}

	// (3) Re-send of the identical payload -> duplicate {id,topic,state} echo.
	out, code = DispatchTool("flint_write", map[string]any{"kind": "knowledge", "payload": payload})
	if code != 0 {
		t.Fatalf("③ 재전송 code=%d out=%q, want 0", code, out)
	}
	for _, want := range []string{`"status":"duplicate"`, fmt.Sprintf(`"id":%d`, id), `"topic":` + jsonStr(t, uniq), `"state":"active"`} {
		if !strings.Contains(out, want) {
			t.Errorf("③ duplicate 응답에 %s 누락: %q", want, out)
		}
	}

	// (4) flint_read -> read_events source='mcp' metering restored (no increase if the wrapper blocks it).
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

// countReadEvents — the number of read_events rows with source='mcp'.
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

// jsonStr — a JSON string literal for assertions.
func jsonStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
