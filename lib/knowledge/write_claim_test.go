package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

type claimGateStore struct {
	wire.HistoryStubs

	rowClaim string
	rowFound bool
	queryErr error

	approved       []int
	approvedClaims []string
	queries        []string
}

func (f *claimGateStore) Migrate(ctx context.Context) error { return nil }
func (f *claimGateStore) Ping(ctx context.Context) error    { return nil }
func (f *claimGateStore) Close()                            {}

func (f *claimGateStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	ids := make([]int, len(recs))
	for i := range ids {
		ids[i] = i + 1
	}
	return ids, nil
}

func (f *claimGateStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}

func (f *claimGateStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}

func (f *claimGateStore) Commit(ctx context.Context, msg string) (string, error) {
	return "cafe123", nil
}

func (f *claimGateStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if strings.Contains(sql, "SELECT claim FROM knowledge") {
		if !f.rowFound {
			return nil, nil
		}
		return [][]any{{f.rowClaim}}, nil
	}
	return nil, nil
}

func (f *claimGateStore) Approve(ctx context.Context, id int, claim string) error {
	f.approved = append(f.approved, id)
	f.approvedClaims = append(f.approvedClaims, claim)
	return nil
}

func mustRunApproveSt(t *testing.T, st wire.Store, args []string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunApprove(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestWriteWithoutClaimForcesPending(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"demo-claim-gate","summary":"synthetic demo summary for claim gate","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["status"]; got != "pending" {
		t.Errorf("status = %v, want pending (conf 0.9 무관 강제)", got)
	}

	fc := &fakeStore{}
	out, code = run(t, fc, []string{"knowledge"},
		`{"topic":"demo-claim-set","summary":"synthetic demo with claim","confidence":0.9,"claim":"decision"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if len(fc.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(fc.inserted))
	}
	if got := fc.inserted[0]["status"]; got != "active" {
		t.Errorf("status = %v, want active (claim 있으면 무변경)", got)
	}
	if got := fc.inserted[0]["claim"]; got != "decision" {
		t.Errorf("claim = %v, want decision (payload 보존)", got)
	}
}

func TestWriteClaimPreservesConfidenceRule(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"demo-claim-conf","summary":"synthetic demo claim with high confidence","confidence":0.95,"claim":"decision"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["status"]; got != "active" {
		t.Errorf("status = %v, want active (claim 지정 시 conf 0.95 규칙 보전)", got)
	}
	if m := decode(t, out); m["state"] != "active" {
		t.Errorf("state = %v, want active (출력 정합)", m["state"])
	}
}

func TestApproveBackfillEventRowExits3(t *testing.T) {
	st := &claimGateStore{rowFound: true, rowClaim: "event"}
	out, code := mustRunApproveSt(t, st, []string{"--id", "900001"})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"error":"usage"`) {
		t.Errorf("out = %q, want usage 에러", out)
	}
	if !strings.Contains(out, "--claim") {
		t.Errorf("out = %q, want --claim 재지정 유도", out)
	}
	if len(st.approved) != 0 {
		t.Errorf("승격 경로 호출 %d회, want 0", len(st.approved))
	}

	s, err := ApproveWithClaim(context.Background(), st, 900001, "")
	if err == nil {
		t.Fatal("err = nil, want claim 게이트 에러")
	}
	if s != "" {
		t.Errorf("status = %q, want 빈 문자열", s)
	}

	stEmpty := &claimGateStore{rowFound: true, rowClaim: ""}
	if _, err := ApproveWithClaim(context.Background(), stEmpty, 900002, ""); err == nil {
		t.Fatal("claim '' 행도 게이트 에러여야 함")
	}
}

func TestApproveWithClaimPromotes(t *testing.T) {
	st := &claimGateStore{rowFound: true, rowClaim: "event"}
	s, err := ApproveWithClaim(context.Background(), st, 900003, "decision")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if s != "promoted" {
		t.Errorf("status = %q, want promoted", s)
	}
	if len(st.approved) != 1 || st.approved[0] != 900003 {
		t.Errorf("approved = %v, want [900003]", st.approved)
	}

	st2 := &claimGateStore{rowFound: true, rowClaim: "event"}
	out, code := mustRunApproveSt(t, st2, []string{"--id", "900004", "--claim", "procedure"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"status":"approved"`) {
		t.Errorf("out = %q, want approved JSON", out)
	}
	if len(st2.approved) != 1 || st2.approved[0] != 900004 {
		t.Errorf("approved = %v, want [900004]", st2.approved)
	}

	stValid := &claimGateStore{rowFound: true, rowClaim: "decision"}
	out, code = mustRunApproveSt(t, stValid, []string{"--id", "900005"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}

	stBad := &claimGateStore{rowFound: true, rowClaim: "event"}
	out, code = mustRunApproveSt(t, stBad, []string{"--id", "900006", "--claim", "insight"})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
	}
	if len(stBad.approved) != 0 {
		t.Errorf("무효 claim으로 승격 호출 %d회, want 0", len(stBad.approved))
	}
}

func TestWriteRejectsSessionSummaryCopy(t *testing.T) {
	if err := RejectSessionSummaryCopy("Let me chr", "Let me chronologically recap the demo"); err == nil {
		t.Error("접두 일치는 거부되어야 함")
	}
	if err := RejectSessionSummaryCopy("same text", "same text"); err == nil {
		t.Error("동일 텍스트는 거부되어야 함")
	}
	if err := RejectSessionSummaryCopy("a different summary", "unrelated session text"); err != nil {
		t.Errorf("무관 텍스트 거부됨: %v", err)
	}
	if err := RejectSessionSummaryCopy("anything", ""); err != nil {
		t.Errorf("세션 요약 공백이면 통과해야 함: %v", err)
	}

	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{"Let me chronologically recap this demo session"}}, nil
		}
		return nil, nil
	}}
	out, code := run(t, f, []string{"knowledge", "--session", "demo-agent"},
		`{"topic":"demo-summary-copy","summary":"Let me chronologically recap this demo session","confidence":0.5}`)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"summary"`) {
		t.Errorf("out = %q, want summary 필드 에러", out)
	}
	if len(f.inserted) != 0 {
		t.Errorf("inserted = %d, want 0 (거부 경로)", len(f.inserted))
	}

	fOk := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{"Let me chronologically recap this demo session"}}, nil
		}
		return nil, nil
	}}
	out, code = run(t, fOk, []string{"knowledge", "--session", "demo-agent"},
		`{"topic":"demo-summary-distinct","summary":"synthetic demo knowledge distinct from session text","confidence":0.5}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
}

func TestApproveWithClaimPersistsClaim(t *testing.T) {
	st := &claimGateStore{rowFound: true, rowClaim: "event"}
	s, err := ApproveWithClaim(context.Background(), st, 900007, "decision")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if s != "promoted" {
		t.Errorf("status = %q, want promoted", s)
	}
	if len(st.approvedClaims) != 1 || st.approvedClaims[0] != "decision" {
		t.Errorf("승격 시 claim 전달=%v, want [decision]", st.approvedClaims)
	}

	stValid := &claimGateStore{rowFound: true, rowClaim: "decision"}
	if _, err := ApproveWithClaim(context.Background(), stValid, 900008, ""); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(stValid.approvedClaims) != 1 || stValid.approvedClaims[0] != "" {
		t.Errorf("무 claim 승격 시 행 claim 보존 전달=%v, want [\"\", nil]", stValid.approvedClaims)
	}
}

func TestWriteNormalizesClaimOnInsert(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"demo-claim-norm","summary":"synthetic demo claim normalization","confidence":0.9,"claim":" EVENT "}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["claim"]; got != "event" {
		t.Errorf("claim = %v, want event(정규화 저장)", got)
	}
}

func TestWriteRejectsInvalidClaimVocabulary(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"demo-claim-invalid","summary":"synthetic demo invalid claim","confidence":0.9,"claim":"foo"}`)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"claim"`) {
		t.Errorf("out = %q, want claim 필드 에러", out)
	}
	if len(f.inserted) != 0 {
		t.Errorf("inserted = %d, want 0(거부 경로)", len(f.inserted))
	}
}

func TestApproveWithClaimPersistsClaimRealSlow(t *testing.T) {
	if _, ok := seamRealDSN(); !ok {
		t.Skip("SUPERLITE_REAL=1 required")
	}
	ctx := context.Background()
	scratchDSN := testengine.Scratch(t)
	repo, err := db.NewRepo(ctx, scratchDSN)
	if err != nil {
		t.Skipf("repo unavailable: %v", err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, g := range db.SchemaGates() {
		if err := g.Ensure(ctx, repo); err != nil {
			t.Fatalf("schema gate v%d: %v", g.Version, err)
		}
	}
	pool, err := db.Connect(ctx, scratchDSN)
	if err != nil {
		t.Skipf("pool unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	st := &writeRealStore{Repo: repo, pool: pool}

	uniq := fmt.Sprintf("demo claim persist %d", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM knowledge WHERE topic = $1`, uniq); err != nil {
			t.Errorf("정리 실패: %v", err)
		}
	})
	ids, err := st.InsertBatch(ctx, []map[string]any{{
		"kind": "knowledge", "topic": uniq, "summary": "합성 데모 claim 영속 회귀",
		"confidence": 0.9, "content_hash": uniq, "status": "pending",
		"ingested_at": time.Now(),
	}})
	if err != nil {
		t.Fatalf("시딩 실패: %v", err)
	}
	id := ids[0]

	s, err := ApproveWithClaim(ctx, st, id, "decision")
	if err != nil || s != "promoted" {
		t.Fatalf("ApproveWithClaim = (%q, %v), want (promoted, nil)", s, err)
	}
	var claim, status string
	if err := pool.QueryRow(ctx,
		`SELECT claim, status FROM knowledge WHERE id = $1`, id).Scan(&claim, &status); err != nil {
		t.Fatalf("행 조회 실패: %v", err)
	}
	if claim != "decision" || status != "active" {
		t.Fatalf("claim/status=%q/%q, want decision/active", claim, status)
	}

	h, ok := wire.Lookup("read")
	if !ok {
		t.Fatal("read 미등록")
	}
	var out bytes.Buffer
	if code := h(ctx, st, []string{"--claim", "decision", uniq}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read exit=%d out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), uniq) {
		t.Errorf("read --claim decision 결과에 승격 행 미노출: %s", out.String())
	}
}
