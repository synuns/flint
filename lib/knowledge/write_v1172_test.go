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

func TestWriteCorrectionResendDedupPreV1171Row(t *testing.T) {

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

	var out1 strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge", "--source", "correction"},
		strings.NewReader(fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 행","confidence":0.1,"origin":"agent"}`, topic)), &out1); code != 0 {
		t.Fatalf("write(1) exit=%d out=%s", code, out1.String())
	}
	w1 := decode(t, out1.String())
	id1 := int(w1["id"].(float64))

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

	rows, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE topic = $1`, topic)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 수 조회 실패: %v", err)
	}
	if n := fmt.Sprint(rows[0][0]); n != "1" {
		t.Fatalf("행 수=%s, want 1(재전송 ADD는 r65 결함)", n)
	}
}

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
