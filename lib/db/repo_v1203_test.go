// repo_v1203_test.go — v1.20.3 (r89) regressions: Merge·
// SetSupersededRef join the supersede advisory-lock domain. The fast
// test pins the seam contract (the precheck·guarded UPDATE·steward
// lookup all ride the lock-held connection — zero pool hops, the same
// one-connection rule as r88); the slow tests drive the patched race
// itself (Supersede(A,B) ∥ Merge(B,A), Merge↔Merge, SetSupersededRef∥
// SetSupersededRef — the pre-patch shape let both sides pass their
// out-of-domain prechecks and both land, planting a 2-cycle).
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// fakeSupersedeRow — the fake pgx.Row behind the lock seam's query
// return: the zero value scans the cycle-check shape as
// no-cycle/no-truncation (the inert default the shared fakes use);
// the v1203 tests script cycle·steward-status·ErrNoRows via fields.
type fakeSupersedeRow struct {
	cyc, truncated bool
	scanStatus     bool   // route Scan to the single-string steward shape
	status         string // the steward status when scanStatus
	err            error  // returned from Scan verbatim (ErrNoRows etc.)
}

func (f fakeSupersedeRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if f.scanStatus {
		if len(dest) > 0 {
			p, ok := dest[0].(*string)
			if !ok {
				return fmt.Errorf("fakeSupersedeRow: dest[0] %T, want *string", dest[0])
			}
			*p = f.status
		}
		return nil
	}
	if len(dest) > 0 {
		p, ok := dest[0].(*bool)
		if !ok {
			return fmt.Errorf("fakeSupersedeRow: dest[0] %T, want *bool", dest[0])
		}
		*p = f.cyc
	}
	if len(dest) > 1 {
		p, ok := dest[1].(*bool)
		if !ok {
			return fmt.Errorf("fakeSupersedeRow: dest[1] %T, want *bool", dest[1])
		}
		*p = f.truncated
	}
	return nil
}

// v1203LockFake — the lock-seam swap recording exec·query calls (FIFO
// scripted rows-affected·query rows; both fall back to 1/the inert row
// once a script runs dry) plus the pool-based transitionExecFn
// recorder (must stay EMPTY — a pool hop under the lock is the r88
// deadlock shape).
type v1203LockFake struct {
	execCalls  []transitionCall
	queryCalls []transitionCall
	execN      []int64
	rows       []fakeSupersedeRow
	poolExec   []transitionCall
}

func (f *v1203LockFake) install(t *testing.T) {
	t.Helper()
	prevLock, prevTrans := supersedeLockFn, transitionExecFn
	supersedeLockFn = func(ctx context.Context, r *Repo) (supersedeExecFn, supersedeQueryFn, func(), error) {
		exec := func(ctx context.Context, query string, args ...any) (int64, error) {
			f.execCalls = append(f.execCalls, transitionCall{sql: query, args: args})
			if len(f.execN) > 0 {
				n := f.execN[0]
				f.execN = f.execN[1:]
				return n, nil
			}
			return 1, nil
		}
		query := func(ctx context.Context, q string, args ...any) pgx.Row {
			f.queryCalls = append(f.queryCalls, transitionCall{sql: q, args: args})
			if len(f.rows) > 0 {
				row := f.rows[0]
				f.rows = f.rows[1:]
				return row
			}
			return fakeSupersedeRow{}
		}
		return exec, query, func() {}, nil
	}
	transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
		f.poolExec = append(f.poolExec, transitionCall{sql: query, args: args})
		return 1, nil
	}
	t.Cleanup(func() {
		supersedeLockFn = prevLock
		transitionExecFn = prevTrans
	})
}

// TestSetRefMergeJoinSupersedeLockDomainV1203 — the fast seam contract:
// inside the lock domain, the cycle precheck runs via the lock-held
// connection's query, the guarded UPDATE rides the same connection's
// exec (with the NOT EXISTS cycle predicate embedded), the steward
// lookup never hops the pool, and the pool-based transitionExecFn
// records ZERO calls (the one-connection rule of r88 extended to the
// whole supersede family).
func TestSetRefMergeJoinSupersedeLockDomainV1203(t *testing.T) {
	ctx := context.Background()

	// SetSupersededRef — precheck query + guarded ref UPDATE, both on
	// the lock connection.
	f := &v1203LockFake{}
	f.install(t)
	if n, err := (&Repo{}).SetSupersededRef(ctx, 5, 9); err != nil || n != 1 {
		t.Fatalf("SetSupersededRef = (%d, %v), want (1, nil)", n, err)
	}
	if len(f.queryCalls) != 1 || !strings.Contains(f.queryCalls[0].sql, "WITH RECURSIVE") {
		t.Fatalf("precheck query calls=%d — the cycle precheck must ride the lock conn's query", len(f.queryCalls))
	}
	if len(f.execCalls) != 1 {
		t.Fatalf("lock-held 실행 %d회, want 1", len(f.execCalls))
	}
	if sqlText := f.execCalls[0].sql; !strings.Contains(sqlText, "SET superseded_by = $2") ||
		!strings.Contains(sqlText, "superseded_by IS NULL") ||
		!strings.Contains(sqlText, "NOT EXISTS") {
		t.Errorf("ref UPDATE 가드 미병기: %q", sqlText)
	}
	if len(f.execCalls[0].args) != 3 || f.execCalls[0].args[0] != 5 || f.execCalls[0].args[1] != 9 {
		t.Errorf("ref UPDATE 파라미터=%v, want [5 9 <depth>]", f.execCalls[0].args)
	}
	if len(f.poolExec) != 0 {
		t.Fatalf("pool 경유 실행 %d건 — 락 도메인 내 풀 홉은 r88 교착 형상: %v", len(f.poolExec), f.poolExec)
	}
	// 0 rows keeps the not-an-error contract (already referenced·absent).
	f2 := &v1203LockFake{execN: []int64{0}}
	f2.install(t)
	if n, err := (&Repo{}).SetSupersededRef(ctx, 5, 9); err != nil || n != 0 {
		t.Fatalf("SetSupersededRef 0행 = (%d, %v), want (0, nil)", n, err)
	}

	// Merge — the cycle precheck refused: no tx starts, nothing hops the pool.
	f3 := &v1203LockFake{rows: []fakeSupersedeRow{{cyc: true}}}
	f3.install(t)
	err := (&Repo{}).Merge(ctx, 1, 2)
	if err == nil || !strings.Contains(err.Error(), "would create a supersede cycle") {
		t.Fatalf("Merge cycle 거부 오류 = %v, want cycle diagnostic", err)
	}
	if len(f3.execCalls) != 0 {
		t.Fatalf("cycle 거부 후 exec %d회, want 0 (tx 미개시): %v", len(f3.execCalls), f3.execCalls)
	}
	if len(f3.poolExec) != 0 {
		t.Fatalf("pool 경유 실행 잔여 %d건: %v", len(f3.poolExec), f3.poolExec)
	}

	// Merge — the guarded UPDATE lands 0 rows (not active): the steward
	// lookup rides the lock conn's query, the tx rolls back on the same
	// conn, and Merge returns before any pool use.
	f4 := &v1203LockFake{
		execN: []int64{1, 0}, // BEGIN, guarded UPDATE (0 rows)
		rows: []fakeSupersedeRow{
			{},                                    // precheck: no cycle
			{scanStatus: true, status: "pending"}, // steward: pending from
		},
	}
	f4.install(t)
	err = (&Repo{}).Merge(ctx, 1, 2)
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "cannot merge") {
		t.Fatalf("Merge 0행 steward 오류 = %v, want ErrNotFound (cannot merge)", err)
	}
	if len(f4.execCalls) != 3 ||
		strings.ToUpper(f4.execCalls[0].sql) != "BEGIN" ||
		!strings.Contains(f4.execCalls[1].sql, "superseded_by = $2") ||
		!strings.Contains(f4.execCalls[1].sql, "NOT EXISTS") ||
		strings.ToUpper(f4.execCalls[2].sql) != "ROLLBACK" {
		t.Fatalf("Merge 실행열 미충족(BEGIN→가드 UPDATE→ROLLBACK): %v", f4.execCalls)
	}
	if len(f4.queryCalls) != 2 || !strings.Contains(f4.queryCalls[1].sql, "SELECT status FROM knowledge") {
		t.Fatalf("steward 조회가 락 커넥션 query를 타지 않음: %v", f4.queryCalls)
	}
	if len(f4.poolExec) != 0 {
		t.Fatalf("pool 경유 실행 잔여 %d건: %v", len(f4.poolExec), f4.poolExec)
	}
}

// TestSlowSupersedeMergeCrossRaceV1203 — the patched defect itself: 20
// rounds of Supersede(A,B) ∥ Merge(B,A) on fresh active rows. The
// single lock domain must yield EXACTLY ONE landed write per round
// (the loser: the cycle diagnostic or the 0-rows ErrNotFound steward)
// — both landing is a planted 2-cycle, the pre-patch shape.
func TestSlowSupersedeMergeCrossRaceV1203(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for round := 0; round < 20; round++ {
		ids := seedCycleRows(t, r, fmt.Sprintf("v1203-sm-%02d", round), 2)
		a, b := ids[0], ids[1]
		res := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, res[0] = r.Supersede(ctx, a, b, at)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[1] = r.Merge(ctx, b, a)
		}()
		wg.Wait()
		_, refA := lineageOf(t, r, a)
		_, refB := lineageOf(t, r, b)
		wins := 0
		if refA != nil {
			wins++
		}
		if refB != nil {
			wins++
		}
		if wins != 1 {
			t.Fatalf("round %d landed writes=%d want exactly 1 (2=planted cycle, 0=lost write)", round, wins)
		}
		if refA != nil && refB != nil && *refA == b && *refB == a {
			t.Fatalf("round %d: 2-cycle 적립 (a→b && b→a)", round)
		}
		lost := 0
		for i, err := range res {
			if err == nil {
				continue
			}
			lost++
			if !errors.Is(err, ErrNotFound) &&
				!strings.Contains(err.Error(), "would create a supersede cycle") {
				t.Fatalf("round %d loser[%d] err=%v want cycle diagnostic or ErrNotFound steward", round, i, err)
			}
		}
		if lost != 1 {
			t.Fatalf("round %d losers=%d want 1 (res=%v)", round, lost, res)
		}
	}
}

// TestSlowMergeMergeCrossRaceV1203 — the same crossed shape inside
// Merge itself: Merge(A,B) ∥ Merge(B,A), 20 rounds, exactly one winner
// (the loser reads the winner's lineage under the lock and is refused).
func TestSlowMergeMergeCrossRaceV1203(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		ids := seedCycleRows(t, r, fmt.Sprintf("v1203-mm-%02d", round), 2)
		a, b := ids[0], ids[1]
		res := make([]error, 2)
		var wg sync.WaitGroup
		for i, pair := range [][2]int{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i, from, to int) {
				defer wg.Done()
				res[i] = r.Merge(ctx, from, to)
			}(i, pair[0], pair[1])
		}
		wg.Wait()
		_, refA := lineageOf(t, r, a)
		_, refB := lineageOf(t, r, b)
		wins := 0
		if refA != nil {
			wins++
		}
		if refB != nil {
			wins++
		}
		if wins != 1 {
			t.Fatalf("round %d landed merges=%d want exactly 1 (res=%v)", round, wins, res)
		}
		if refA != nil && refB != nil && *refA == b && *refB == a {
			t.Fatalf("round %d: 2-cycle 적립 (a→b && b→a)", round)
		}
		// Exactly one nil error — the refused side reports why.
		nils := 0
		for _, err := range res {
			if err == nil {
				nils++
			}
		}
		if nils != 1 {
			t.Fatalf("round %d merge successes=%d want 1 (res=%v)", round, nils, res)
		}
	}
}

// TestSlowSetSupersededRefCrossRaceV1203 — the ref-only writer joins
// the same domain: SetSupersededRef(A,B) ∥ SetSupersededRef(B,A), 20
// rounds, exactly one ref lands; the loser gets the deterministic
// cycle diagnostic (its precheck now runs inside the lock).
func TestSlowSetSupersededRefCrossRaceV1203(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		ids := seedCycleRows(t, r, fmt.Sprintf("v1203-rr-%02d", round), 2)
		a, b := ids[0], ids[1]
		res := make([]error, 2)
		var wg sync.WaitGroup
		for i, pair := range [][2]int{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i, oldID, newID int) {
				defer wg.Done()
				_, res[i] = r.SetSupersededRef(ctx, oldID, newID)
			}(i, pair[0], pair[1])
		}
		wg.Wait()
		_, refA := lineageOf(t, r, a)
		_, refB := lineageOf(t, r, b)
		wins := 0
		if refA != nil {
			wins++
		}
		if refB != nil {
			wins++
		}
		if wins != 1 {
			t.Fatalf("round %d landed refs=%d want exactly 1 (2=planted cycle, 0=lost write; res=%v)", round, wins, res)
		}
		if refA != nil && refB != nil && *refA == b && *refB == a {
			t.Fatalf("round %d: 2-cycle 적립 (a→b && b→a)", round)
		}
		lost := 0
		for i, err := range res {
			if err == nil {
				continue
			}
			lost++
			if !strings.Contains(err.Error(), "would create a supersede cycle") {
				t.Fatalf("round %d loser[%d] err=%v want the deterministic cycle diagnostic", round, i, err)
			}
		}
		if lost != 1 {
			t.Fatalf("round %d losers=%d want 1 (res=%v)", round, lost, res)
		}
	}
}

// TestMergePanicReleasesSupersedeLockV1210 — R90 (v1.21.0): a panic in
// Merge's lock-held body must not leak the supersede advisory lock. The
// deferred release (acquired right after supersedeLockFn) runs on the
// panic unwind too — the immediate unlock call on the success path is
// skipped by the unwinding, so without the defer the lock stays held.
// Verified at the supersedeLockFn swap seam: the first acquisition's
// query panics (the cycle precheck), the unlock counter must still
// advance, and a subsequent supersede-family write (SetSupersededRef)
// acquires the lock again and succeeds.
func TestMergePanicReleasesSupersedeLockV1210(t *testing.T) {
	ctx := context.Background()
	unlockCalls := 0
	acquires := 0
	prevLock := supersedeLockFn
	supersedeLockFn = func(ctx context.Context, r *Repo) (supersedeExecFn, supersedeQueryFn, func(), error) {
		acquires++
		panicking := acquires == 1 // the probe acquisition panics in the precheck
		exec := func(ctx context.Context, query string, args ...any) (int64, error) {
			if panicking {
				panic("v1210 probe: injected panic under the supersede lock")
			}
			return 1, nil
		}
		query := func(ctx context.Context, q string, args ...any) pgx.Row {
			if panicking {
				panic("v1210 probe: injected panic in the cycle precheck")
			}
			return fakeSupersedeRow{} // no cycle — the inert default
		}
		return exec, query, func() { unlockCalls++ }, nil
	}
	t.Cleanup(func() { supersedeLockFn = prevLock })

	// First Merge — the panic propagates AND the deferred release runs
	// (the leak shape: unlockCalls 0 after the recover).
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("주입 panic 미전파 — 테스트 전제 붕괴(주입 경로 확인 필요)")
			}
		}()
		_ = (&Repo{}).Merge(ctx, 1, 2)
	}()
	if unlockCalls == 0 {
		t.Fatal("panic 경로에서 락 미해제 — 어드바이저리 락 누출(R90 회귀)")
	}

	// A subsequent supersede-family write acquires the lock again and
	// succeeds — the released lock is re-acquirable (SetSupersededRef
	// rides the same seam without a pool-based commit, so the zero-Repo
	// probe stays pure).
	n, err := (&Repo{}).SetSupersededRef(ctx, 5, 9)
	if err != nil || n != 1 {
		t.Fatalf("후속 SetSupersededRef = (%d, %v), want (1, nil)(락 재획득 후 성공)", n, err)
	}
	if acquires != 2 {
		t.Fatalf("락 획득 %d회, want 2(panic 1회 + 후속 성공 1회)", acquires)
	}
}
