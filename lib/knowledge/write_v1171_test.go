// write_v1171_test.go — v1.17.1 (r64-observability) regressions: the
// write-source origin tracking fix (correction·gap accruals store their
// source origin so the approve-time promotion counters can fire) and the
// echo scan-bound seam (one threshold load per write).
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// TestWriteCorrectionOriginDefault — a `write --source correction` line
// stores origin "correction" (not the Normalize "agent" fallback): without
// it the approve-time promotion counter correction_approved is dead (the
// funnel keys on the stored origin). A pending landing is asserted too
// (default confidence 0.7 < conf_floor 0.8).
func TestWriteCorrectionOriginDefault(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1171 교정 데모","summary":"합성 데모 교정 행"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["origin"]; got != "correction" {
		t.Fatalf("origin=%v, want correction(approve 관측 키의 원천)", got)
	}
	if got := f.inserted[0]["status"]; got != "pending" {
		t.Fatalf("status=%v, want pending(기본 confidence 0.7 < conf_floor)", got)
	}
}

// TestWriteCorrectionOriginExplicitRespected — an explicitly given origin
// survives the correction defaults (the confidence convention — the manual
// contract unchanged).
func TestWriteCorrectionOriginExplicitRespected(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1171 교정 데모","summary":"합성 데모 교정 행","origin":"human"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if got := f.inserted[0]["origin"]; got != "human" {
		t.Fatalf("origin=%v, want human(명시값 존중)", got)
	}
}

// TestGapCandidateOriginGap — an accrued gap candidate stores origin "gap"
// (not the Normalize "agent" fallback) so the approve-time gap_approved
// counter can fire; the N-invariant content_hash is unchanged (existing
// pending rows keep succeeding — the hash input stays origin-free).
func TestGapCandidateOriginGap(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["origin"]; got != "gap" {
		t.Fatalf("origin=%v, want gap(approve 관측 키의 원천)", got)
	}
	if got := f.inserted[0]["content_hash"]; got != gapCandidateHash(NormalizeGapQuery("데모 쿼리")) {
		t.Fatalf("content_hash=%v, want N-invariant gapCandidateHash(불변)", got)
	}
}

// TestWriteEchoNoExtraThresholdLoad — a single-line knowledge write loads
// the meta thresholds exactly twice (the stdin row cap + the write-time
// decision load); the echo scan rides the already-loaded values via the
// scan-bound seam and issues no third threshold query (v1.17.1 — the
// per-write duplicate LoadThresholds abolished).
func TestWriteEchoNoExtraThresholdLoad(t *testing.T) {
	t.Cleanup(func() { echoScanCap, echoDupThreshold = 0, 0 })
	const thSQL = "SELECT key, value FROM meta WHERE key IN ("
	loads := 0
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		// The vocab union query shares the "key IN (" shape — exclude it
		// (only threshold loads count).
		if strings.Contains(sql, thSQL) && !strings.Contains(sql, "vocab_") {
			loads++
		}
		return nil, nil
	}}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"v1171 에코 데모","summary":"합성 데모 행","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if loads != 2 {
		t.Fatalf("threshold 로드=%d회, want 2(행 상한+쓰기 결정 — 에코가 추가 로드하지 않음)", loads)
	}
}

// TestEchoScanBoundsSeamSkipsMetaLoad — with the scan bounds injected, the
// scan SQL carries the injected LIMIT and no meta threshold query fires;
// with the seam reset (<=0), the in-scan derivation (meta lookup →
// default) still answers standalone callers.
func TestEchoScanBoundsSeamSkipsMetaLoad(t *testing.T) {
	t.Cleanup(func() { echoScanCap, echoDupThreshold = 0, 0 })
	const thSQL = "SELECT key, value FROM meta WHERE key IN ("
	var queries []string
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		queries = append(queries, sql)
		return nil, nil
	}}
	SetEchoScanBounds(500, 0.6)
	if _, err := EchoScan(context.Background(), f, echoTestTopic, echoDupSummary); err != nil {
		t.Fatalf("EchoScan 에러: %v", err)
	}
	joined := strings.Join(queries, ";")
	if strings.Contains(joined, thSQL) {
		t.Fatalf("주입된 바운드에도 meta threshold 쿼리 발화: %q", joined)
	}
	if !strings.Contains(joined, "LIMIT 500") {
		t.Fatalf("스캔 SQL에 주입 LIMIT 500 부재: %q", joined)
	}
	// Seam reset: the standalone-caller derivation (meta best effort →
	// default LIMIT 5000) is kept.
	echoScanCap, echoDupThreshold = 0, 0
	queries = nil
	if _, err := EchoScan(context.Background(), f, echoTestTopic, echoDupSummary); err != nil {
		t.Fatalf("EchoScan(파생) 에러: %v", err)
	}
	if !strings.Contains(strings.Join(queries, ";"), "LIMIT 5000") {
		t.Fatalf("미주입 시 기본 LIMIT 5000 부재: %q", strings.Join(queries, ";"))
	}
}

// TestWriteCorrectionApproveFunnelRealSlow — slow (SUPERLITE_REAL=1 +
// FLINT_TEST_DSN): the live flow `write --source correction → approve`
// lands the row pending with origin "correction" stored, and the
// promotion bumps the daily:correction_approved meta counter (the r64
// observability fix measured end-to-end on a real DB).
func TestWriteCorrectionApproveFunnelRealSlow(t *testing.T) {
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	const marker = "v1171-r64-합성-"
	t.Cleanup(func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)
	})
	_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)

	// 1. write --source correction (synthetic demo payload, an explicit
	// low confidence so the row lands pending regardless of the verify
	// DB's conf_floor override) → pending.
	topic := marker + time.Now().UTC().Format("150405.000000000")
	var out strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge", "--source", "correction"},
		strings.NewReader(fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 행","confidence":0.1}`, topic)), &out); code != 0 {
		t.Fatalf("write exit=%d out=%s", code, out.String())
	}
	var w map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &w); err != nil {
		t.Fatalf("write 출력 파싱: %v (%s)", err, out.String())
	}
	if w["state"] != "pending" {
		t.Fatalf("write state=%v, want pending(명시 confidence 0.1)", w["state"])
	}
	id := int(w["id"].(float64))

	// 2. the stored row carries origin "correction" (the fix — "agent"
	// would deaden the funnel counter).
	rows, err := st.Query(ctx, `SELECT origin, status FROM knowledge WHERE id = $1`, id)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 조회 실패: %v rows=%d", err, len(rows))
	}
	if got := fmt.Sprint(rows[0][0]); got != "correction" {
		t.Fatalf("저장 origin=%q, want correction", got)
	}

	// 3. approve → the daily:correction_approved counter bumps by 1.
	today := time.Now().UTC().Format("2006-01-02")
	key := "daily:correction_approved:" + today
	before := metaCounterValue(ctx, t, st, key)
	var aout strings.Builder
	if code := RunApprove(ctx, st, []string{"--id", fmt.Sprint(id)},
		strings.NewReader(""), &aout); code != 0 {
		t.Fatalf("approve exit=%d out=%s", code, aout.String())
	}
	after := metaCounterValue(ctx, t, st, key)
	if after != before+1 {
		t.Fatalf("correction_approved 증분=%d, want 1(before=%d after=%d)", after-before, before, after)
	}
}

// metaCounterValue — the current integer value of a daily flat key
// (a missing key reads 0).
func metaCounterValue(ctx context.Context, t *testing.T, st wire.Store, key string) int {
	t.Helper()
	rows, err := st.Query(ctx, `SELECT value FROM meta WHERE key = $1`, key)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}
