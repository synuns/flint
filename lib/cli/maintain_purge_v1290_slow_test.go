package cli

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/ops"
)

func TestMaintainPurgeDroppedRoundtripRealSlowV1290(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer base.Close()

	freshDSN := testengine.Scratch(t)
	u, err := url.Parse(freshDSN)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	scratch, err := db.Connect(ctx, freshDSN)
	if err != nil {
		t.Fatalf("scratch connect: %v", err)
	}
	if _, err := scratch.Exec(ctx, "CREATE TABLE note (v text)"); err != nil {
		scratch.Close()
		t.Fatalf("scratch table: %v", err)
	}
	if _, err := scratch.Exec(ctx, "INSERT INTO note VALUES ('v1290 synthetic demo')"); err != nil {
		scratch.Close()
		t.Fatalf("scratch seed: %v", err)
	}
	scratch.Close()
	if _, err := base.Exec(ctx, "DROP DATABASE "+name); err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}

	st, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Fatalf("newRealStore: %v", err)
	}
	defer st.Close()

	oldPurgeDSN := ops.PurgeDSNFn
	ops.PurgeDSNFn = func() string { return dsn }
	t.Cleanup(func() { ops.PurgeDSNFn = oldPurgeDSN })

	var dryOut strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--purge-dropped"}, nil, &dryOut); code != 0 {
		t.Fatalf("dry-run exit=%d out=%s, want 0", code, dryOut.String())
	}
	if !strings.Contains(dryOut.String(), `"status":"dry_run"`) || !strings.Contains(dryOut.String(), name) {
		t.Fatalf("dry-run 목록에 스크래치 부재: %s", dryOut.String())
	}

	var yesOut strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--purge-dropped", "--yes"}, nil, &yesOut); code != 0 {
		t.Fatalf("purge exit=%d out=%s, want 0", code, yesOut.String())
	}
	if !strings.Contains(yesOut.String(), `"status":"purged"`) {
		t.Fatalf("purged 응답 아님: %s", yesOut.String())
	}

	var afterOut strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--purge-dropped"}, nil, &afterOut); code != 0 {
		t.Fatalf("purge 후 dry-run exit=%d out=%s, want 0", code, afterOut.String())
	}
	if strings.Contains(afterOut.String(), name) {
		t.Fatalf("purge 후에도 스크래치 잔존: %s", afterOut.String())
	}
}
