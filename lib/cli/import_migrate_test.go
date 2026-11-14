// import_migrate_test.go — v1.3.0 T9: flint import --format jsonl|pam CLI
// 배선 테스트(합성 데모 데이터만). 계약: --format 유무 라우팅(기존 규칙
// import 불변)·cli 진입 시그니처(st, args, stdin, stdout)·실DB slow 왕복.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
)

// TestImportMigrateCLISignature — cli.RunImportMigrate 계약 시그니처
// (st, args, stdin, stdout) int 위임.
func TestImportMigrateCLISignature(t *testing.T) {
	f := &fakeStore{}
	var out strings.Builder
	code := RunImportMigrate(f, []string{"--format", "bogus"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
}

// TestImportFormatRoutingDispatch — 배선 레지스트리 경로: --format jsonl은
// migrate로, --verbose는 기존 경로 exit 3 불변(v0.36.1 관례 회귀).
func TestImportFormatRoutingDispatch(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	rows := []knowledge.ExportRow{{
		ID: 31, Topic: "demo dispatch alpha", Content: "합성 데모 dispatch 본문 — 배선 검증용.",
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
	// 기존 규칙 import 회귀 — 미확인 플래그 exit 3 불변.
	out, code = mustRun(t, []string{"import", "--verbose"}, "")
	if code != 3 {
		t.Fatalf("기존 경로 회귀 exit=%d, want 3; out=%s", code, out)
	}
}

// TestImportMigrateJSONLRealSlow — 실DB 왕복(slow): 적립→멱등 2회
// duplicate→계보 갱신 검증. SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만.
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
	t.Cleanup(st.Close) // LIFO — 행 정리가 close보다 먼저
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
	// 계보 검증 — beta가 alpha를 가리킨다.
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
	// 멱등 — 2회차 전 행 duplicate.
	out.Reset()
	if code := RunImportMigrate(st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out); code != 0 {
		t.Fatalf("2회차 exit=%d; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"imported":0`) || !strings.Contains(out.String(), `"duplicated":2`) {
		t.Fatalf("멱등 위반: %s", out.String())
	}
}

// fmtInt — pg 셀 값(int16/int32/int64) 정수 변환.
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
