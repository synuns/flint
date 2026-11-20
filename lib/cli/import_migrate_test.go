// import_migrate_test.go — flint import --format jsonl|pam CLI
// wiring tests (synthetic demo data only). Contracts: --format routing (the existing rules
// import unchanged), the cli entry signature (st, args, stdin, stdout), and a real-DB slow round trip.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
)

// TestImportMigrateCLISignature — the cli.RunImportMigrate contract signature
// (st, args, stdin, stdout) int delegation.
func TestImportMigrateCLISignature(t *testing.T) {
	f := &fakeStore{}
	var out strings.Builder
	code := RunImportMigrate(f, []string{"--format", "bogus"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
}

// TestImportFormatRoutingDispatch — the wiring registry path: --format jsonl goes to
// migrate; --verbose keeps the existing path's exit 3.
func TestImportFormatRoutingDispatch(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	// the accrual passes content_hash dedup and the PK (the shared real-DB residue ID 31) —
	// so the topic residue is cleaned at the start boundary, and each run uses unique Content (test name +
	// timestamp) to avoid residue-hash collisions, keeping imported:1 idempotent.
	cleanupRealResidue(`DELETE FROM knowledge WHERE topic = 'demo dispatch alpha'`)
	rows := []knowledge.ExportRow{{
		ID: 31, Topic: "demo dispatch alpha",
		Content: fmt.Sprintf("합성 데모 dispatch 본문 — 배선 검증용. (%s %d)",
			t.Name(), time.Now().UnixNano()),
		Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
		Domain: "main", Source: "agent",
		CreatedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
	}}
	data, err := knowledge.FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mustRun(t, []string{"import", "--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("dispatch exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"ok"`) || !strings.Contains(out, `"imported":1`) {
		t.Fatalf("dispatch 요약: %s", out)
	}
	// existing rules import regression — an unrecognized flag keeps exit 3.
	out, code = mustRun(t, []string{"import", "--verbose"}, "")
	if code != 3 {
		t.Fatalf("기존 경로 회귀 exit=%d, want 3; out=%s", code, out)
	}
}

// TestImportMigrateJSONLRealSlow — a real-DB round trip (slow): accrual -> a 2nd idempotent
// duplicate -> lineage update verification. Only when SUPERLITE_REAL=1 + FLINT_TEST_DSN are set.
func TestImportMigrateJSONLRealSlow(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	st, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(st.Close) // LIFO — row cleanup runs before close
	const marker = "demo-import-migrate-slow"
	cleanup := func() {
		if _, err := st.Query(ctx, `DELETE FROM knowledge WHERE origin = $1`, marker); err != nil {
			t.Fatalf("정리 실패: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	sup := 910011
	rows := []knowledge.ExportRow{
		{ID: 910011, Topic: "demo slow alpha", Content: "합성 데모 slow 본문 alpha — 실DB 왕복용.",
			Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
			Domain: "main", Source: marker, CreatedAt: t0, UpdatedAt: t0},
		{ID: 910022, Topic: "demo slow beta", Content: "합성 데모 slow 본문 beta — 실DB 계보용.",
			Kind: "knowledge", Status: "active", Confidence: 0.8, Tags: []string{"demo"},
			Domain: "main", Source: marker, CreatedAt: t0, UpdatedAt: t0, SupersededBy: &sup},
	}
	data, err := knowledge.FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if code := RunImportMigrate(st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out); code != 0 {
		t.Fatalf("1회차 exit=%d; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"imported":2`) || !strings.Contains(out.String(), `"superseded":1`) {
		t.Fatalf("1회차 요약: %s", out.String())
	}
	// lineage check — beta points at alpha.
	got, err := st.Query(ctx, `SELECT superseded_by FROM knowledge WHERE id = 910022`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0][0] == nil {
		t.Fatalf("계보 미갱신: %v", got)
	}
	if fmtInt(got[0][0]) != 910011 {
		t.Fatalf("superseded_by=%v, want 910011", got[0][0])
	}
	// idempotent — the 2nd run duplicates every row.
	out.Reset()
	if code := RunImportMigrate(st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out); code != 0 {
		t.Fatalf("2회차 exit=%d; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"imported":0`) || !strings.Contains(out.String(), `"duplicated":2`) {
		t.Fatalf("멱등 위반: %s", out.String())
	}
}

// fmtInt — integer conversion of pg cell values (int16/int32/int64).
func fmtInt(v any) int {
	switch n := v.(type) {
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	}
	return -1
}
