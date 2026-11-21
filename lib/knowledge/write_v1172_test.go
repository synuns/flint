// write_v1172_test.go — v1.17.2 (r65-dedup) regressions: the correction
// origin default must never enter the content-hash input — the v1.17.1
// pre-hash injection changed the hash, so an identical correction payload
// resend against a pre-v1.17.1 row was judged new (ADD), not duplicate.
package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

// TestWriteCorrectionResendDedupPreV1171Row — a row shaped exactly as
// v1.17.0 stored it (correction defaults applied, no origin in the
// payload — the hash carries the Normalize "agent" fallback) must still
// succeed the duplicate ruling for a `write --source correction` resend
// of the same payload (the r65 fix: the origin stamp sits after the
// hash). Without the fix the injected origin "correction" changes the
// hash input and the resend becomes a new ADD.
func TestWriteCorrectionResendDedupPreV1171Row(t *testing.T) {
	// The pre-v1.17.1 hash input, built explicitly (NOT via
	// applyCorrectionDefaults — that would just mirror the current code):
	// correction defaults (confidence 0.7 + the correction tag) with NO
	// origin key, so Normalize's "agent" fallback enters the hash — the
	// exact bytes a v1.17.0 write stored.
	p := map[string]any{
		"topic":      "v1172 교정 데모",
		"summary":    "합성 데모 교정 행",
		"confidence": 0.7,
		"tags":       []any{"correction"},
	}
	h := store.ContentHash(store.HashCanonical(store.Normalize("knowledge", p)))
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{
		h: {ID: 21, Topic: "v1172 교정 데모", State: "pending"},
	}}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1172 교정 데모","summary":"합성 데모 교정 행"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("삽입=%d건, want 0(동일 페이로드 재전송은 duplicate — 미판정 시 ADD가 r65 결함)", len(f.inserted))
	}
	m := decode(t, out)
	if m["status"] != "duplicate" {
		t.Fatalf("status=%v, want duplicate", m["status"])
	}
	if id, _ := m["id"].(float64); int(id) != 21 {
		t.Fatalf("id=%v, want 21(기존 행 메타 노출)", m["id"])
	}
}

// TestWriteCorrectionResendDedupRealSlow — slow (SUPERLITE_REAL=1 +
// FLINT_TEST_DSN): the live two-send flow — (1) a first write whose
// stored row is byte-identical to the pre-v1.17.1 shape (an explicit
// origin "agent": the payload without the v1.17.1 origin default hashed
// the Normalize "agent" fallback and stored origin "agent"), (2) the
// identical payload minus the origin field via `--source correction` —
// must judge the second send duplicate (one row, the existing id echoed).
func TestWriteCorrectionResendDedupRealSlow(t *testing.T) {
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
	const marker = "v1172-r65-합성-"
	t.Cleanup(func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)
	})
	_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)
	topic := marker + time.Now().UTC().Format("150405.000000000")

	// 1. the pre-v1.17.1-shaped row: an explicit origin "agent" is exactly
	// what the v1.17.0 path stored for this payload (same hash, same
	// stored origin). An explicit low confidence keeps it pending.
	var out1 strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge", "--source", "correction"},
		strings.NewReader(fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 행","confidence":0.1,"origin":"agent"}`, topic)), &out1); code != 0 {
		t.Fatalf("write(1) exit=%d out=%s", code, out1.String())
	}
	w1 := decode(t, out1.String())
	id1 := int(w1["id"].(float64))

	// 2. the resend — identical payload minus the origin field (the
	// v1.17.1+ default path) → duplicate, not a new ADD.
	var out2 strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge", "--source", "correction"},
		strings.NewReader(fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 행","confidence":0.1}`, topic)), &out2); code != 0 {
		t.Fatalf("write(2) exit=%d out=%s", code, out2.String())
	}
	w2 := decode(t, out2.String())
	if w2["status"] != "duplicate" {
		t.Fatalf("재전송 status=%v, want duplicate(out=%s)", w2["status"], out2.String())
	}
	if id2, _ := w2["id"].(float64); int(id2) != id1 {
		t.Fatalf("재전송 id=%v, want %d(기존 행)", w2["id"], id1)
	}

	// 3. exactly one row exists for the topic (no ADD on the resend).
	rows, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE topic = $1`, topic)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 수 조회 실패: %v", err)
	}
	if n := fmt.Sprint(rows[0][0]); n != "1" {
		t.Fatalf("행 수=%s, want 1(재전송 ADD는 r65 결함)", n)
	}
}

// TestGapApproveFunnelRealSlow — slow (SUPERLITE_REAL=1 + FLINT_TEST_DSN):
// the live gap funnel `accrue → approve` stores origin "gap" on the
// candidate and the promotion bumps the daily:gap_approved meta counter
// (the r64 gap-side observability fix measured end-to-end on a real DB —
// the correction-side twin of TestWriteCorrectionApproveFunnelRealSlow).
func TestGapApproveFunnelRealSlow(t *testing.T) {
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
	query := "v1172 갭 데모 쿼리"
	hash := gapCandidateHash(NormalizeGapQuery(query))
	t.Cleanup(func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE content_hash = $1`, hash)
	})
	_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE content_hash = $1`, hash)

	// 1. accrue one candidate (a count well past the threshold — the
	// accrual branch itself is the target here).
	maybeAccrueGapCandidate(ctx, st, query, 999)
	rows, err := st.Query(ctx, `SELECT id, origin, status FROM knowledge WHERE content_hash = $1`, hash)
	if err != nil || len(rows) != 1 {
		t.Fatalf("후보 행 조회 실패: %v rows=%d", err, len(rows))
	}
	id := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); err != nil {
		t.Fatalf("id 파싱 실패: %v (%v)", err, rows[0][0])
	}
	if got := fmt.Sprint(rows[0][1]); got != "gap" {
		t.Fatalf("저장 origin=%q, want gap", got)
	}
	if got := fmt.Sprint(rows[0][2]); got != "pending" {
		t.Fatalf("status=%q, want pending", got)
	}

	// 2. approve → the daily:gap_approved counter bumps by 1.
	today := time.Now().UTC().Format("2006-01-02")
	key := "daily:gap_approved:" + today
	before := metaCounterValue(ctx, t, st, key)
	var aout strings.Builder
	if code := RunApprove(ctx, st, []string{"--id", fmt.Sprint(id)},
		strings.NewReader(""), &aout); code != 0 {
		t.Fatalf("approve exit=%d out=%s", code, aout.String())
	}
	after := metaCounterValue(ctx, t, st, key)
	if after != before+1 {
		t.Fatalf("gap_approved 증분=%d, want 1(before=%d after=%d)", after-before, before, after)
	}
}
