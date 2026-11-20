// done_test.go — the done dispatch and realStore delegation paths (registration activation and seams).
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// doneFake — a fakeStore with a StaleSessionRuns stub layered on.
// Without a stub it behaves as zero; with SUPERLITE_REAL=1 it delegates to the real implementation.
type doneFake struct {
	*fakeStore

	stubbed bool // whether runs or runsErr was specified
	runs    int
	runsErr error

	// recording
	gotAgent string
}

func (d *doneFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	d.gotAgent = agent
	if d.runsErr != nil {
		return 0, d.runsErr
	}
	return d.runs, nil
}

// UpdateSessionSummary — closing-summary update delegation (write guard).
// The fake is a no-op success (unit path — the update contract is asserted by the knowledge done_test).
func (d *doneFake) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	return 1, nil
}

// withDoneFake — the Run-path seam. Default fake; with FLINT_TEST_DSN set +
// SUPERLITE_REAL=1, unstubbed operations delegate to the real implementation (skips
// without a real DB). Without FLINT_TEST_DSN, no real-implementation fallback (seamRealDSN).
func withDoneFake(t *testing.T, d *doneFake) {
	t.Helper()
	prev := newStore
	if dsn, real := seamRealDSN(); real {
		realSt, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(realSt.Close)
		rs := realSt.(*realStore)
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return &delegatingDone{done: d, real: rs}, nil
		}
	} else {
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return d, nil
		}
	}
	t.Cleanup(func() { newStore = prev })
}

// delegatingDone — stubbed operations use the fake; unstubbed ones delegate to the real implementation.
type delegatingDone struct {
	done *doneFake
	real *realStore
}

func (d *delegatingDone) Migrate(ctx context.Context) error { return d.real.Migrate(ctx) }

// the 5 history methods — inheriting the delegation pattern (passing through the real-implementation seam as-is).
func (d *delegatingDone) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	return d.real.LogEntries(ctx, q)
}
func (d *delegatingDone) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return d.real.QueryAsOf(ctx, q)
}
func (d *delegatingDone) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return d.real.DiffRows(ctx, fromHash, toHash)
}
func (d *delegatingDone) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return d.real.DiffStat(ctx, hashes)
}
func (d *delegatingDone) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return d.real.DiffCommits(ctx, fromHash, toHash)
}
func (d *delegatingDone) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return d.real.InsertBatch(ctx, recs)
}
func (d *delegatingDone) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return d.real.ExistsHash(ctx, hash)
}
func (d *delegatingDone) ExistsProject(ctx context.Context, name string) (bool, error) {
	return d.real.ExistsProject(ctx, name)
}
func (d *delegatingDone) Commit(ctx context.Context, msg string) (string, error) {
	return d.real.Commit(ctx, msg)
}
func (d *delegatingDone) Ping(ctx context.Context) error { return d.real.Ping(ctx) }
func (d *delegatingDone) Close()                         { d.real.Close() }
func (d *delegatingDone) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if d.done.queryFn != nil || d.done.queryErr != nil {
		return d.done.Query(ctx, sql, args...)
	}
	return d.real.Query(ctx, sql, args...)
}
func (d *delegatingDone) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	if d.done.stubbed {
		return d.done.StaleSessionRuns(ctx, agent)
	}
	return d.real.StaleSessionRuns(ctx, agent)
}

// UpdateSessionSummary — the write-guard transition — real-implementation delegation.
func (d *delegatingDone) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	return d.real.UpdateSessionSummary(ctx, id, summary, tasks)
}

// mustDoneRun — the full Run path (including registration and seams).
func mustDoneRun(t *testing.T, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	return mustRun(t, args, "")
}

// stubAutoBackup — swaps the knowledge.AutoBackupFn seam (guaranteeing side-effect-free unit tests).
// The seam var lives in the owning package (knowledge), so it is
// stubbed directly.
func stubAutoBackup(t *testing.T) {
	t.Helper()
	prev := knowledge.AutoBackupFn
	knowledge.AutoBackupFn = func(ctx context.Context, st Store) {}
	t.Cleanup(func() { knowledge.AutoBackupFn = prev })
}

// registration + full Run path — Lookup success + exit 0.
func TestDoneRegisteredAndRun(t *testing.T) {
	if _, ok := Lookup("done"); !ok {
		t.Fatal("done 미등록")
	}
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustDoneRun(t, []string{"done", "--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("status=%v, want ok", m["status"])
	}
}

// seam check — unstubbed operations delegate to the real implementation (SUPERLITE_REAL=1 real DB, else fake).
func TestDoneDelegatingRun(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f}
	withDoneFake(t, d)
	out, code := mustDoneRun(t, []string{"done", "--session", "done-seam-probe"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out)
}

// that stubAutoBackup actually disables the knowledge-owned seam (preventing a missing delegation).
func TestStubAutoBackupTargetsKnowledgeSeam(t *testing.T) {
	stubAutoBackup(t)
	var out strings.Builder
	// call the handler directly — via knowledge.RunDone.
	code := runDoneForSeamCheck(&out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
}

// runDoneForSeamCheck — runs via the registered done handler (minimal session).
func runDoneForSeamCheck(out *strings.Builder) int {
	h, ok := Lookup("done")
	if !ok {
		return -1
	}
	return h(context.Background(), &fakeStore{}, []string{"--session", "claude"}, strings.NewReader(""), out)
}
