package regression

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
)

func regViewDSN() string {
	return regTestDSN()
}

func regViewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, regViewDSN())
	if err != nil {
		t.Skipf("doltgres unavailable: %v", err)
	}
	return pool
}

func regSharedDBLock(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}
	t.Cleanup(func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

func TestActiveKnowledgeExposesReadColumns(t *testing.T) {
	regSharedDBLock(t)
	pool := regViewDB(t)
	defer pool.Close()
	ctx := context.Background()

	cleanup := func() { pool.Exec(ctx, `DELETE FROM knowledge WHERE topic = 'reg-view-read'`) }
	cleanup()
	defer cleanup()
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge
		(topic, summary, tags, confidence, content_hash)
		VALUES ('reg-view-read', 'view regression', ':r:', 0.9, 'reg-view-read-v4')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Setenv("FLINT_DSN", regViewDSN())
	var out bytes.Buffer
	if code := cli.Run([]string{"read", "reg-view-read"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read exit=%d, want 0 (Migrate 후); out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "reg-view-read") {
		t.Fatalf("read 출력에 seed 행 없음: %q", out.String())
	}

	rows, err := pool.Query(ctx, `SELECT * FROM active_knowledge LIMIT 0`)
	if err != nil {
		t.Fatalf("view query: %v", err)
	}
	cols := make(map[string]bool)
	for _, fd := range rows.FieldDescriptions() {
		cols[string(fd.Name)] = true
	}
	rows.Close()
	for _, c := range []string{"read_count", "last_read_at"} {
		if !cols[c] {
			t.Fatalf("active_knowledge에 %s 컬럼 미노출: %v", c, cols)
		}
	}
}
