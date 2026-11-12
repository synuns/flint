// approve_test.go — approve 커맨드 계약(exit 매핑·주입 st 재배선).
// v0.39.0 T4: lib/cli에서 이동(cli/approve_test.go 원본 + cli/
// wire_approve_store_test.go의 TestApproveUsesInjectedStore — approveFn
// seam 교체는 구현 소유 패키지에서만 전파되므로 함께 이동).
package knowledge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// seamRealDSN — SUPERLITE_REAL=1 실구현 seam의 DSN 해결(cli/cli_test.go
// 관례 이동분, N1): FLINT_TEST_DSN 설정 시만 실DB 접속을 허용한다.
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

// fakeApprove — T17 admin Approve fake. errErr 미지정 시 성공,
// SUPERLITE_REAL=1이면 실 Repo에 위임(실DB 미기동 시 스킵).
// v0.43.0 T1(fake 동질 가드): knowledge capability 인터페이스 전부의
// 패리티 fake를 겸한다 — 미구현 메서드는 아래 canned 구현(호출 기록
// 슬라이스·선택적 capErr 주입), 단언은 fake_parity_test.go가 든다.
type fakeApprove struct {
	err     error
	gotID   int
	calledN int

	// canned 공통 오류(미지정 시 영 동작 — 성공·0행·부재).
	capErr         error
	approveIDs     []int // Approve
	superseded     []supersedeCall
	bumped         [][]int        // BumpReads
	readEvents     []db.ReadEvent // RecordReadEvent
	hotEvents      []string       // RecordHotEvent
	recordedUses   []countCall    // RecordUsage
	dupIDQueries   []string       // DupIDByHash 질의 해시
	dupRowQueries  []string       // DupRowByHash 질의 해시
	countedUses    []countCall    // RecordUsageCount
	staleAgents    []string       // StaleSessionRuns
	playbookUses   []playbookUse  // LogPlaybookUse
	sessionUpdates []sessionUpdate
}

// supersedeCall — Supersede canned 호출 기록.
type supersedeCall struct{ oldID, newID int }

// playbookUse — LogPlaybookUse canned 호출 기록.
type playbookUse struct {
	id int
	ok bool
}

// sessionUpdate — UpdateSessionSummary canned 호출 기록.
type sessionUpdate struct {
	id             int
	summary, tasks string
}

// 패리티 canned 구현 — 전부 기록 후 capErr(미지정 nil) 반환, 값 반환은
// 영 동작(rows 0·exists false·runs 0). 폴백이 아닌 capability 경로가
// 컴파일 시점에 보장된다(스펙 §1).
func (f *fakeApprove) Approve(ctx context.Context, id int) error {
	f.approveIDs = append(f.approveIDs, id)
	return f.capErr
}

func (f *fakeApprove) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	f.superseded = append(f.superseded, supersedeCall{oldID, newID})
	return 0, f.capErr
}

func (f *fakeApprove) BumpReads(ctx context.Context, ids []int) error {
	f.bumped = append(f.bumped, ids)
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

// withApproveFake — ApproveFn seam 교체. 기본 fake, FLINT_TEST_DSN 설정 +
// SUPERLITE_REAL=1이면 stub 미설정 시 실구현 위임(위임 DSN도 FLINT_TEST_DSN).
// FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(N1 — seamRealDSN).
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

// mustApprove — wire.Lookup("approve") 경로 실행(등록·디스패치 동시 검증).
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

// TestApproveOK — 성공 시 {"status":"approved","id":N} exit 0(Review Focus: exit 매핑).
func TestApproveOK(t *testing.T) {
	f := &fakeApprove{}
	withApproveFake(t, f)
	out, code := mustApprove(t, []string{"--id", "7"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"approved","id":7}` {
		t.Errorf("out = %q, want approved JSON", out)
	}
	if f.gotID != 7 || f.calledN != 1 {
		t.Errorf("Approve 호출 (id=%d, n=%d), want (7, 1)", f.gotID, f.calledN)
	}
}

// TestApproveMissing — ErrNotFound → 한 줄 JSON 에러 exit 1.
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

// TestApproveUsage — --id 누락·비정수·알 수 없는 플래그 → exit 1.
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
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
			}
			if !strings.Contains(out, `"error"`) {
				t.Errorf("out = %q, want 에러 JSON", out)
			}
		})
	}
}

// TestApproveOtherError — 일반 에러는 errLine 위임·MapExit exit 1.
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

// approveStoreFake — fakeStore에 Approve 승격 스텁을 얹은 fake.
// 승격 행위(실DB UPDATE)는 ApproveFn real seam이 담당 — 여기는
// 배선(주입 st 사용·자체 연결 미발동)만 계약으로 삼는다.
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

// errSelfConnectDisabled — sentinel 전용 에러(재배선 후 도달 불가 경로).
var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

// TestApproveUsesInjectedStore — Review Focus: fake st 주입 시 자체 연결
// 경로(ApproveFn 기본 실구현의 db.NewRepo) 미호출·승격은 주입 st 경유.
func TestApproveUsesInjectedStore(t *testing.T) {
	f := &approveStoreFake{fakeStore: &fakeStore{}}
	// sentinel — 재배선 전 RunApprove는 st를 무시하고 이 seam을 부르므로
	// 호출 자체가 실패 신호다(기본 실구현 대신 호출 카운터로 안전하게).
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
