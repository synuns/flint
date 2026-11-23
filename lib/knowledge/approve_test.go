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

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

func seamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	return testengine.ResolveDSN(), true
}

type fakeApprove struct {
	err     error
	gotID   int
	calledN int

	capErr           error
	approveIDs       []int
	superseded       []supersedeCall
	bumped           [][]int
	readEvents       []db.ReadEvent
	hotEvents        []string
	recordedUses     []countCall
	dupIDQueries     []string
	dupRowQueries    []string
	countedUses      []countCall
	staleAgents      []string
	playbookUses     []playbookUse
	sessionUpdates   []sessionUpdate
	recordedBytes    []bytesCall
	kreads           []kreadsCallV
	telemetryCmds    []string
	replayedRecall   []db.RecallReplay
	pendingSummaries []string
}

type kreadsCallV struct {
	ids     []int
	session string
	kind    string
}

type supersedeCall struct{ oldID, newID int }

type playbookUse struct {
	id int
	ok bool
}

type sessionUpdate struct {
	id             int
	summary, tasks string
}

func (f *fakeApprove) Approve(ctx context.Context, id int) error {
	f.approveIDs = append(f.approveIDs, id)
	return f.capErr
}

func (f *fakeApprove) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	f.superseded = append(f.superseded, supersedeCall{oldID, newID})
	return 0, f.capErr
}

func (f *fakeApprove) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	f.superseded = append(f.superseded, supersedeCall{oldID, newID})
	return 1, f.capErr
}

func (f *fakeApprove) BumpReads(ctx context.Context, ids []int) error {
	f.bumped = append(f.bumped, ids)
	return f.capErr
}

func (f *fakeApprove) BumpRecallScores(ctx context.Context, ids []int) error {
	return f.capErr
}

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

func (f *fakeApprove) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	f.pendingSummaries = append(f.pendingSummaries, fmt.Sprintf("%d:%s", id, summary))
	if f.capErr != nil {
		return 0, f.capErr
	}
	return 1, nil
}

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

			if !strings.Contains(out, `"error":"usage"`) {
				t.Errorf("out = %q, want error:usage", out)
			}
		})
	}
}

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

var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

func TestApproveUsesInjectedStore(t *testing.T) {
	f := &approveStoreFake{fakeStore: &fakeStore{}}

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

type originCounterFakeV struct {
	origin string
	cap    *counterCaptureV
}

func (o *originCounterFakeV) fn(sql string, args []any) ([][]any, error) {
	o.cap.queries = append(o.cap.queries, capturedQueryV{sql, args})
	if strings.Contains(sql, "SELECT origin FROM knowledge") {
		return [][]any{{o.origin}}, nil
	}
	return nil, nil
}

func TestApproveOriginCounters(t *testing.T) {
	today := counterToday()
	cases := []struct {
		name     string
		origin   string
		wantKind string
	}{
		{"correction 소스", "correction", "daily:correction_approved:" + today},
		{"gap 소스", "gap", "daily:gap_approved:" + today},
		{"비대상 소스(agent)", "agent", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &counterCaptureV{}
			o := &originCounterFakeV{origin: tc.origin, cap: cap}
			f := &approveStoreFake{fakeStore: &fakeStore{queryFn: o.fn}}
			var out bytes.Buffer
			if code := RunApprove(context.Background(), f, []string{"--id", "7"},
				strings.NewReader(""), &out); code != 0 {
				t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
			}
			if got := cap.sum("daily:approve_done:" + today); got != 1 {
				t.Errorf("approve_done 증분=%d, want 1(병행 발화)", got)
			}
			if tc.wantKind != "" {
				if got := cap.sum(tc.wantKind); got != 1 {
					t.Errorf("%s 증분=%d, want 1", tc.wantKind, got)
				}
			}
			other := "daily:correction_approved:" + today
			if tc.wantKind == other {
				other = "daily:gap_approved:" + today
			}
			if got := cap.sum(other); got != 0 {
				t.Errorf("비발화 키 %s 증분=%d, want 0", other, got)
			}
		})
	}
}

func TestApproveOriginCounterQuietOnLookupFailure(t *testing.T) {
	for name, st := range map[string]wire.Store{
		"쿼리 실패": &approveStoreFake{fakeStore: &fakeStore{queryErr: fmt.Errorf("db down")}},
		"행 부재":  &approveStoreFake{fakeStore: &fakeStore{}},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if code := RunApprove(context.Background(), st, []string{"--id", "7"},
				strings.NewReader(""), &out); code != 0 {
				t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
			}
			if !strings.Contains(out.String(), `"status":"approved"`) {
				t.Fatalf("out=%q, want approved JSON", out.String())
			}
		})
	}
}

func TestCurationCounterKindsV1170(t *testing.T) {

	if len(curationCounterKinds) != 16 {
		t.Fatalf("curationCounterKinds count=%d, want 16", len(curationCounterKinds))
	}
	for _, k := range []string{"correction_approved", "gap_approved", "survived_sessions"} {
		if !curationCounterKinds[k] {
			t.Errorf("curationCounterKinds에 %q 미등록", k)
		}
	}
	if curationCounterKinds["typo_kind"] {
		t.Error("미지원 종류가 등록된 것으로 집계됨")
	}
}
