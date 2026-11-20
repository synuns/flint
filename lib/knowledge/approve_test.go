// approve_test.go — the approve command contract (exit mapping·injected
// st rewiring).
package knowledge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// seamRealDSN — the DSN resolution of the SUPERLITE_REAL=1 real
// implementation seam (moved from the cli/cli_test.go convention, N1):
// real-DB connections are allowed only when FLINT_TEST_DSN is set.
func seamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

// fakeApprove — the admin Approve fake. Success when err is unset; with
// SUPERLITE_REAL=1 it delegates to the real Repo (skipped when the real
// DB is down). (fake-equivalence guard): also serves as the parity fake
// for all the knowledge capability interfaces — unimplemented methods
// get the canned implementations below (call-recording slices·optional
// capErr injection); fake_parity_test.go carries the assertions.
type fakeApprove struct {
	err     error
	gotID   int
	calledN int

	// canned common errors (a no-op when unset — success·0 rows·absence).
	capErr           error
	approveIDs       []int // Approve
	superseded       []supersedeCall
	bumped           [][]int        // BumpReads
	readEvents       []db.ReadEvent // RecordReadEvent
	hotEvents        []string       // RecordHotEvent
	recordedUses     []countCall    // RecordUsage
	dupIDQueries     []string       // DupIDByHash query hashes
	dupRowQueries    []string       // DupRowByHash query hashes
	countedUses      []countCall    // RecordUsageCount
	staleAgents      []string       // StaleSessionRuns
	playbookUses     []playbookUse  // LogPlaybookUse
	sessionUpdates   []sessionUpdate
	recordedBytes    []bytesCall       // RecordBytes (G5 byte metering)
	kreads           []kreadsCallV     // RecordKnowledgeReads (recall history)
	telemetryCmds    []string          // CommitTelemetry (the separated metering commit)
	replayedRecall   []db.RecallReplay // ReplayRecallEvents (R42-A score-transfer replay)
	pendingSummaries []string          // UpdatePendingSummary (v1.12.0 T3 gap-candidate refresh)
}

// kreadsCallV — a canned RecordKnowledgeReads call record (the same shape
// as kreadsCall — a fake-owned local definition).
type kreadsCallV struct {
	ids     []int
	session string
	kind    string
}

// supersedeCall — a canned Supersede call record.
type supersedeCall struct{ oldID, newID int }

// playbookUse — a canned LogPlaybookUse call record.
type playbookUse struct {
	id int
	ok bool
}

// sessionUpdate — a canned UpdateSessionSummary call record.
type sessionUpdate struct {
	id             int
	summary, tasks string
}

// Parity canned implementations — all record the call then return capErr
// (nil when unset); value returns are no-ops (rows 0·exists false·runs
// 0). The capability path — not a fallback — is guaranteed at compile
// time (spec §1).
func (f *fakeApprove) Approve(ctx context.Context, id int) error {
	f.approveIDs = append(f.approveIDs, id)
	return f.capErr
}

func (f *fakeApprove) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	f.superseded = append(f.superseded, supersedeCall{oldID, newID})
	return 0, f.capErr
}

// SetSupersededRef — a canned lineage-reference-restoration
// implementation (parity — reuses the superseded slice, records the
// call).
func (f *fakeApprove) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	f.superseded = append(f.superseded, supersedeCall{oldID, newID})
	return 1, f.capErr
}

func (f *fakeApprove) BumpReads(ctx context.Context, ids []int) error {
	f.bumped = append(f.bumped, ids)
	return f.capErr
}

// BumpRecallScores — a parity canned (for the RecallBumper assertions).
func (f *fakeApprove) BumpRecallScores(ctx context.Context, ids []int) error {
	return f.capErr
}

// ReplayRecallEvents — a parity canned (for the recallReplayStore
// assertions — the R42-A migration score replay. Leaves a call record so
// the round-trip unit tests can consume the replay contract).
func (f *fakeApprove) ReplayRecallEvents(ctx context.Context, items []db.RecallReplay) error {
	f.replayedRecall = append(f.replayedRecall, items...)
	return f.capErr
}

func (f *fakeApprove) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	f.readEvents = append(f.readEvents, ev)
	return f.capErr
}

func (f *fakeApprove) RecordHotEvent(ctx context.Context, ids string) error {
	f.hotEvents = append(f.hotEvents, ids)
	return f.capErr
}

func (f *fakeApprove) RecordUsage(ctx context.Context, kind, source string) error {
	f.recordedUses = append(f.recordedUses, countCall{kind, source, 1})
	return f.capErr
}

func (f *fakeApprove) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	f.dupIDQueries = append(f.dupIDQueries, hash)
	return 0, false, f.capErr
}

func (f *fakeApprove) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	f.dupRowQueries = append(f.dupRowQueries, hash)
	return DupMeta{}, false, f.capErr
}

func (f *fakeApprove) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	f.countedUses = append(f.countedUses, countCall{kind, source, count})
	return f.capErr
}

func (f *fakeApprove) RecordBytes(ctx context.Context, kind string, n int) error {
	f.recordedBytes = append(f.recordedBytes, bytesCall{kind, n})
	return f.capErr
}

func (f *fakeApprove) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	f.kreads = append(f.kreads, kreadsCallV{ids, session, kind})
	return f.capErr
}

func (f *fakeApprove) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	f.staleAgents = append(f.staleAgents, agent)
	return 0, f.capErr
}

func (f *fakeApprove) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	f.playbookUses = append(f.playbookUses, playbookUse{id, ok})
	return f.capErr
}

func (f *fakeApprove) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	f.sessionUpdates = append(f.sessionUpdates, sessionUpdate{id, summary, tasks})
	return 0, f.capErr
}

func (f *fakeApprove) CommitTelemetry(ctx context.Context, cmd string) error {
	f.telemetryCmds = append(f.telemetryCmds, cmd)
	return f.capErr
}

// UpdatePendingSummary — canned pending-row summary refresh recording
// (v1.12.0 T3 — the gap-candidate N update seam).
func (f *fakeApprove) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	f.pendingSummaries = append(f.pendingSummaries, fmt.Sprintf("%d:%s", id, summary))
	if f.capErr != nil {
		return 0, f.capErr
	}
	return 1, nil
}

// withApproveFake — swaps the ApproveFn seam. Base fake; with
// FLINT_TEST_DSN set + SUPERLITE_REAL=1, delegates unset stubs to the
// real implementation (the delegation DSN is also FLINT_TEST_DSN).
// Without FLINT_TEST_DSN, no real-implementation fallback
// (seamRealDSN).
func withApproveFake(t *testing.T, f *fakeApprove) {
	t.Helper()
	prev := ApproveFn
	dsn, real := seamRealDSN()
	if real {
		repo, err := db.NewRepo(context.Background(), dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		repo.Close()
	}
	ApproveFn = func(ctx context.Context, id int) error {
		f.calledN++
		f.gotID = id
		if f.err != nil {
			return f.err
		}
		if !real {
			return nil
		}
		repo, err := db.NewRepo(ctx, dsn)
		if err != nil {
			return err
		}
		defer repo.Close()
		return repo.Approve(ctx, id)
	}
	t.Cleanup(func() { ApproveFn = prev })
}

// mustApprove — runs via the wire.Lookup("approve") path (verifying
// registration·dispatch together).
func mustApprove(t *testing.T, args []string) (string, int) {
	t.Helper()
	h, ok := wire.Lookup("approve")
	if !ok {
		t.Fatal("approve 미등록")
	}
	var out bytes.Buffer
	code := h(context.Background(), nil, args, strings.NewReader(""), &out)
	return out.String(), code
}

// TestApproveOK — on success {"status":"approved","id":N} exit 0 (exit
// mapping). The real-DB path (SUPERLITE_REAL=1) self-seeds — accrues one
// synthetic demo pending row (a unique content_hash) via InsertBatch and
// asserts the promotion with the returned id. Defense against the defect
// where, depending on state, not_found occurred due to the implicit
// dependence on other tests' seeding (id=7) on the shared DB. The seeded
// row is deleted at the end (preventing shared-DB pollution).
func TestApproveOK(t *testing.T) {
	f := &fakeApprove{}
	withApproveFake(t, f)
	id := 7
	if dsn, real := seamRealDSN(); real {
		repo, err := db.NewRepo(context.Background(), dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		defer repo.Close()
		uniq := fmt.Sprintf("approve-ok-%d", time.Now().UnixNano())
		ids, err := repo.InsertBatch(context.Background(), []map[string]any{{
			"kind": "knowledge", "topic": uniq, "summary": "approve 승격 회귀(합성 데모)",
			"tags": ":approve:", "confidence": 0.9, "content_hash": uniq,
			"status": "pending", "ingested_at": time.Now(),
		}})
		if err != nil {
			t.Skipf("real seeding unavailable: %v", err)
		}
		id = ids[0]
		pool, err := db.Connect(context.Background(), dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM knowledge WHERE topic = $1`, uniq); err != nil {
				t.Errorf("시딩 행 정리 실패: %v", err)
			}
			pool.Close()
		})
	}
	out, code := mustApprove(t, []string{"--id", strconv.Itoa(id)})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if want := fmt.Sprintf(`{"status":"approved","id":%d}`, id); strings.TrimSpace(out) != want {
		t.Errorf("out = %q, want %s", out, want)
	}
	if f.gotID != id || f.calledN != 1 {
		t.Errorf("Approve 호출 (id=%d, n=%d), want (%d, 1)", f.gotID, f.calledN, id)
	}
}

// TestApproveMissing — ErrNotFound → a one-line JSON error, exit 1.
func TestApproveMissing(t *testing.T) {
	withApproveFake(t, &fakeApprove{err: db.ErrNotFound})
	out, code := mustApprove(t, []string{"--id", "999"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("한 줄이어야 함: %q", out)
	}
	if !strings.Contains(lines[0], `"error":"not_found"`) {
		t.Errorf("out = %q, want not_found 에러", out)
	}
}

// TestApproveUsage — missing --id·non-integer·unknown flag → exit 1.
// The argument-validation paths get the invalid-arg exit 3 (unknown
// flag·missing value·invalid id·missing --id — aligned with mcp
// dispatchApprove).
func TestApproveUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no flag", nil},
		{"missing value", []string{"--id"}},
		{"non int", []string{"--id", "abc"}},
		{"unknown flag", []string{"--wat", "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withApproveFake(t, &fakeApprove{})
			out, code := mustApprove(t, tc.args)
			if code != 3 {
				t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
			}
			// An argument-validation (usage) error carries error:"usage"
			// (only the value replaced from the previous internal —
			// structure unchanged).
			if !strings.Contains(out, `"error":"usage"`) {
				t.Errorf("out = %q, want error:usage", out)
			}
		})
	}
}

// TestApproveUsageHint — the argument-validation error message includes
// the usage hint `--id N [--supersede OLD]` — a mistyped flag can be
// corrected from the guidance.
func TestApproveUsageHint(t *testing.T) {
	withApproveFake(t, &fakeApprove{})
	out, code := mustApprove(t, []string{"--wat", "1"})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"error":"usage"`) {
		t.Errorf("out = %q, want error:usage", out)
	}
	if !strings.Contains(out, "--id N [--supersede OLD]") {
		t.Errorf("out = %q, want 사용법 안내(--id N [--supersede OLD]) 포함", out)
	}
}

// TestApproveOtherError — ordinary errors delegate to errLine·MapExit
// exit 1.
func TestApproveOtherError(t *testing.T) {
	withApproveFake(t, &fakeApprove{err: errors.New("boom")})
	out, code := mustApprove(t, []string{"--id", "3"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("out = %q, want 에러 JSON", out)
	}
}

// approveStoreFake — a fakeStore with the Approve promotion stub layered
// on. The promotion action (the real-DB UPDATE) belongs to the ApproveFn
// real seam — only the wiring (uses the injected st·never opens its own
// connection) is the contract here.
type approveStoreFake struct {
	*fakeStore

	approveN   int
	gotID      int
	approveErr error
}

func (a *approveStoreFake) Approve(ctx context.Context, id int) error {
	a.approveN++
	a.gotID = id
	return a.approveErr
}

// errSelfConnectDisabled — a sentinel-only error (a path unreachable
// after the rewiring).
var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

// TestApproveUsesInjectedStore — with a fake st injected, the
// self-connection path (db.NewRepo of the ApproveFn default real
// implementation) is never called·the promotion goes through the injected
// st.
func TestApproveUsesInjectedStore(t *testing.T) {
	f := &approveStoreFake{fakeStore: &fakeStore{}}
	// sentinel — before the rewiring, RunApprove ignored st and called
	// this seam, so the call itself is a failure signal (safely via a
	// call counter instead of the default real implementation).
	prev := ApproveFn
	selfConn := 0
	ApproveFn = func(ctx context.Context, id int) error {
		selfConn++
		return errSelfConnectDisabled
	}
	t.Cleanup(func() { ApproveFn = prev })

	var out bytes.Buffer
	code := RunApprove(context.Background(), f,
		[]string{"--id", "7"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s", code, out.String())
	}
	if selfConn != 0 {
		t.Fatalf("자체 연결 경로(ApproveFn) 호출 %d회, want 0 — st 재배선 미적용", selfConn)
	}
	if f.approveN != 1 || f.gotID != 7 {
		t.Fatalf("주입 st Approve 호출 (n=%d, id=%d), want (1, 7)", f.approveN, f.gotID)
	}
	if !strings.Contains(out.String(), `"status":"approved"`) {
		t.Fatalf("out=%q, want approved JSON", out.String())
	}
}
