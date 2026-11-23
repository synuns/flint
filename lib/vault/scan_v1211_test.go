package vault

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestScanErrorsPathFree(t *testing.T) {

	missing := filepath.Join(t.TempDir(), "no-such-vault-root")
	_, err := Scan(context.Background(), newFakeStore(), missing, Options{})
	if err == nil {
		t.Fatal("missing root: no error")
	}
	if !strings.Contains(err.Error(), "vault scan: walk") {
		t.Fatalf("walk 오류 형태=%q", err.Error())
	}
	if strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), "no-such-vault-root") {
		t.Fatalf("walk 오류에 경로 유출: %q", err.Error())
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("not-exist 분류 상실: %v", err)
	}

	dir := t.TempDir()
	locked := writeNote(t, dir, "locked-note.md", "# 데모\n\n본문.\n", demoMtime)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	_, err = Scan(context.Background(), newFakeStore(), dir, Options{})
	if err == nil {
		t.Fatal("unreadable note: no error")
	}
	if !strings.Contains(err.Error(), "vault scan: read") {
		t.Fatalf("read 오류 형태=%q", err.Error())
	}
	if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "locked-note.md") {
		t.Fatalf("read 오류에 경로 유출: %q", err.Error())
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("permission 분류 상실: %v", err)
	}
}

type hookExistingStore struct {
	*fakeStore
	hook func()
}

func (h *hookExistingStore) Existing(ctx context.Context) (map[string]Existing, error) {
	h.hook()
	return h.fakeStore.Existing(ctx)
}

func TestScanVanishedSeparateFromSkipped(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# A\n\nx\n", demoMtime)
	writeNote(t, dir, "b.md", "# B\n\nx\n", demoMtime)
	st := &hookExistingStore{fakeStore: newFakeStore()}
	st.hook = func() {
		if err := os.Remove(filepath.Join(dir, "b.md")); err == nil {
			st.hook = func() {}
		}
	}
	stats, err := Scan(context.Background(), st, dir, Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if stats.Skipped != 0 {
		t.Fatalf("Skipped=%d, want 0 (a vanish is not a limit skip)", stats.Skipped)
	}
	if stats.Vanished != 1 || stats.New != 1 {
		t.Fatalf("stats=%+v, want vanished=1 new=1", stats)
	}
	if _, ok := mustRows(t, st)["a.md"]; !ok {
		t.Fatal("a.md not indexed")
	}
}

func TestScanSymlinksSkipped(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "outside-target.md")
	if err := os.WriteFile(target, []byte("# 바깥 데모\n\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "escape.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "gone.md"), filepath.Join(dir, "dangling.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	writeNote(t, dir, "real.md", "# 진짜\n\nx\n", demoMtime)
	st := newFakeStore()
	stats, err := Scan(context.Background(), st, dir, Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if stats.Scanned != 1 || stats.New != 1 {
		t.Fatalf("stats=%+v, want scanned=1 new=1 (symlinks skipped)", stats)
	}
	for _, p := range []string{"escape.md", "dangling.md"} {
		if _, ok := mustRows(t, st)[p]; ok {
			t.Fatalf("symlink %s indexed — escape/dangling must be skipped", p)
		}
	}
	if _, ok := mustRows(t, st)["real.md"]; !ok {
		t.Fatal("real.md missing")
	}
}

func TestScanTouchOnlyRefreshesPointerThenClean(t *testing.T) {
	st := newFakeStore()
	dir := t.TempDir()
	content := "# 데모\n\n본문.\n"
	writeNote(t, dir, "a.md", content, demoMtime)
	mustScan(t, st, dir, Options{})
	touched := demoMtime.Add(3 * time.Hour)
	writeNote(t, dir, "a.md", content, touched)
	stats := mustScan(t, st, dir, Options{})
	if stats.New != 0 || stats.Changed != 0 || stats.Removed != 0 {
		t.Fatalf("touch-only stats=%+v, want deltas 0", stats)
	}
	if stats.Touch != 1 || len(st.touched) != 1 {
		t.Fatalf("Touch=%d touched=%v, want 1", stats.Touch, st.touched)
	}
	row := mustRows(t, st)["a.md"]
	if !row.Mtime.Equal(touched.Truncate(time.Microsecond)) {
		t.Fatalf("mtime 미갱신: %v, want %v", row.Mtime, touched)
	}
	if row.ContentHash != hashOf(content) {
		t.Fatalf("touch가 해시 변경: %q", row.ContentHash)
	}

	stats = mustScan(t, st, dir, Options{})
	if stats.Touch != 0 || stats.Changed != 0 || stats.New != 0 {
		t.Fatalf("갱신 후 재스캔 stats=%+v, want all 0 (반복 sha256 해소)", stats)
	}
	if len(st.touched) != 1 {
		t.Fatalf("재스캔이 다시 touch 기록: %d", len(st.touched))
	}
}

func TestScanTitleTruncateRunes(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	long := strings.Repeat("제", 300)
	writeNote(t, dir, "long-title.md", "# "+long+"\n\n본문.\n", demoMtime)
	mustScan(t, st, dir, Options{})
	got := len([]rune(mustRows(t, st)["long-title.md"].Title))
	if got != 200 {
		t.Fatalf("title runes=%d, want 200", got)
	}
}

func TestApplyDiffRetriesSerializationFailure(t *testing.T) {
	prevFn, prevDelays := applyDiffOnceFn, applyRetryBackoff
	applyRetryBackoff = []time.Duration{0, 0}
	t.Cleanup(func() { applyDiffOnceFn, applyRetryBackoff = prevFn, prevDelays })

	attempts := 0
	applyDiffOnceFn = func(s *PoolStore, ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
		attempts++
		if attempts == 1 {
			return 0, fmt.Errorf("vault apply: upsert batch: %w",
				&pgconn.PgError{Code: "40001", Message: "could not serialize"})
		}
		return 2, nil
	}
	removed, err := (&PoolStore{}).ApplyDiff(context.Background(), nil, nil, []string{"a.md"})
	if err != nil || removed != 2 || attempts != 2 {
		t.Fatalf("40001 재시도: removed=%d attempts=%d err=%v, want 2/2/nil", removed, attempts, err)
	}

	attempts = 0
	applyDiffOnceFn = func(s *PoolStore, ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
		attempts++
		if attempts == 1 {
			return 0, fmt.Errorf("vault apply: commit: %w", &pgconn.PgError{
				Code: "XX000", Message: "constraint violations, transaction rolled back"})
		}
		return 1, nil
	}
	if _, err := (&PoolStore{}).ApplyDiff(context.Background(), nil, nil, []string{"a.md"}); err != nil || attempts != 2 {
		t.Fatalf("XX000 재시도: attempts=%d err=%v, want 2/nil", attempts, err)
	}

	attempts = 0
	applyDiffOnceFn = func(s *PoolStore, ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
		attempts++
		return 0, fmt.Errorf("vault apply: delete: %w", &pgconn.PgError{Code: "42601", Message: "syntax"})
	}
	if _, err := (&PoolStore{}).ApplyDiff(context.Background(), nil, nil, []string{"a.md"}); err == nil || attempts != 1 {
		t.Fatalf("비재시도 오류: attempts=%d err=%v, want 1/non-nil", attempts, err)
	}
}

func TestApplyDiffLargeKeepSetRealSlow(t *testing.T) {
	if os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("FLINT_TEST_DSN required")
	}
	t.Setenv("SUPERLITE_REAL", "1")
	st := realStore(t)
	ctx := context.Background()
	seed := []File{
		{Path: "a.md", Title: "A", Mtime: demoMtime, Size: 1, ContentHash: hashOf("a")},
		{Path: "b.md", Title: "B", Mtime: demoMtime, Size: 1, ContentHash: hashOf("b")},
		{Path: "c.md", Title: "C", Mtime: demoMtime, Size: 1, ContentHash: hashOf("c")},
	}
	if _, err := st.ApplyDiff(ctx, seed, nil, []string{"a.md", "b.md", "c.md"}); err != nil {
		t.Fatalf("seed ApplyDiff: %v", err)
	}
	keep := make([]string, 60001)
	keep[0] = "a.md"
	for i := 1; i < len(keep); i++ {
		keep[i] = fmt.Sprintf("gen/%06d.md", i)
	}
	removed, err := st.ApplyDiff(ctx, nil, nil, keep)
	if err != nil {
		t.Fatalf("60001 keep set: %v (the placeholder ceiling must be gone)", err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d, want 2 (b.md, c.md)", removed)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM vault_files`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows=%d, want 1 (a.md)", n)
	}
}
