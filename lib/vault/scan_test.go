// scan_test.go — the vault diff scan. Fast tests run on the in-memory
// fakeStore by default; with SUPERLITE_REAL=1 + FLINT_TEST_DSN the same
// tests run against the real PoolStore on an isolated scratch database
// (created and dropped per test — shared/personal DBs are never touched).
// TestScanVaultDiffRealSlow is the always-slow real-DB end-to-end.
package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// fakeStore — the in-memory Store: keeps rows in a map, counts ApplyDiff
// calls (the idempotence assertion — a no-change rescan must not write).
type fakeStore struct {
	rows    map[string]File
	applies int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]File{}} }

func (f *fakeStore) Existing(ctx context.Context) (map[string]Existing, error) {
	out := make(map[string]Existing, len(f.rows))
	for p, r := range f.rows {
		out[p] = Existing{Mtime: r.Mtime, Size: r.Size, ContentHash: r.ContentHash}
	}
	return out, nil
}

func (f *fakeStore) Rows(ctx context.Context) (map[string]File, error) {
	out := make(map[string]File, len(f.rows))
	for p, r := range f.rows {
		out[p] = r
	}
	return out, nil
}

func (f *fakeStore) ApplyDiff(ctx context.Context, upserts []File, keep []string) (int64, error) {
	f.applies++
	keepSet := make(map[string]bool, len(keep))
	for _, p := range keep {
		keepSet[p] = true
	}
	var removed int64
	for p := range f.rows {
		if !keepSet[p] {
			delete(f.rows, p)
			removed++
		}
	}
	for _, u := range upserts {
		f.rows[u.Path] = u
	}
	return removed, nil
}

// vaultFilesDDL — the schema v16 vault_files DDL (mirrors lib/sqlfs
// schema.sql; synthetic scratch databases only).
const vaultFilesDDL = `CREATE TABLE IF NOT EXISTS vault_files (
    id              bigserial PRIMARY KEY,
    path            text NOT NULL UNIQUE,
    title           text NOT NULL,
    tags            text NOT NULL DEFAULT '',
    mtime           timestamptz NOT NULL,
    size            bigint NOT NULL,
    content_hash    text NOT NULL,
    summary         text NOT NULL DEFAULT '',
    last_indexed_at timestamptz NOT NULL DEFAULT now()
)`

const vaultHashIdxDDL = `CREATE INDEX IF NOT EXISTS idx_vault_files_content_hash ON vault_files (content_hash)`

// realStore — a PoolStore on an isolated scratch database under
// FLINT_TEST_DSN (created here, dropped on cleanup). SUPERLITE_REAL=1
// required — never a production fallback.
func realStore(t *testing.T) *PoolStore {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Fatal("realStore requires SUPERLITE_REAL=1")
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Fatal("realStore requires FLINT_TEST_DSN")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	// The base pool must outlive the cleanup (a defer would close it
	// before the DROP runs — that leak was measured once).
	t.Cleanup(base.Close)
	name := fmt.Sprintf("flint_t2vault_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
	}
	u.Path = "/" + name
	st, err := Open(ctx, u.String())
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(st.Close)
	// One statement per Exec (the Doltgres multi-statement constraint).
	if _, err := st.pool.Exec(ctx, vaultFilesDDL); err != nil {
		t.Fatalf("vault_files DDL: %v", err)
	}
	if _, err := st.pool.Exec(ctx, vaultHashIdxDDL); err != nil {
		t.Fatalf("vault_files index DDL: %v", err)
	}
	return st
}

// testStore — the fake by default; the real PoolStore when SUPERLITE_REAL=1
// (the seam check reruns the same tests against real code).
func testStore(t *testing.T) Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" && os.Getenv("FLINT_TEST_DSN") != "" {
		return realStore(t)
	}
	return newFakeStore()
}

var demoMtime = time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)

// writeNote — writes a synthetic demo note under dir with an explicit mtime
// (deterministic first-filter comparisons). Returns the full path.
func writeNote(t *testing.T, dir, rel, content string, mtime time.Time) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(full, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return full
}

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func mustScan(t *testing.T, st Store, root string, opts Options) Stats {
	t.Helper()
	stats, err := Scan(context.Background(), st, root, opts)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return stats
}

func mustRows(t *testing.T, st Store) map[string]File {
	t.Helper()
	rows, err := st.Rows(context.Background())
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	return rows
}

func TestScanNewInsert(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	content := "# Demo Title\n\nFirst paragraph text here.\n"
	writeNote(t, dir, "a.md", content, demoMtime)
	stats := mustScan(t, st, dir, Options{})
	if stats.Scanned != 1 || stats.New != 1 || stats.Changed != 0 || stats.Removed != 0 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v, want scanned=1 new=1 others 0", stats)
	}
	rows := mustRows(t, st)
	got, ok := rows["a.md"]
	if !ok {
		t.Fatalf("row a.md missing: %v", rows)
	}
	if got.Title != "Demo Title" {
		t.Fatalf("Title = %q, want %q", got.Title, "Demo Title")
	}
	if got.Summary != "First paragraph text here." {
		t.Fatalf("Summary = %q, want %q", got.Summary, "First paragraph text here.")
	}
	if got.ContentHash != hashOf(content) {
		t.Fatalf("ContentHash = %q, want sha256 of raw bytes", got.ContentHash)
	}
	if got.Tags != "" {
		t.Fatalf("Tags = %q, want empty", got.Tags)
	}
	if !got.Mtime.Equal(demoMtime.Truncate(time.Microsecond)) {
		t.Fatalf("Mtime = %v, want %v (microsecond-truncated)", got.Mtime, demoMtime)
	}
}

func TestScanChangedOnContentEdit(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# Old Title\n\nOld paragraph.\n", demoMtime)
	mustScan(t, st, dir, Options{})
	v2 := "# New Title\n\nNew paragraph.\n"
	writeNote(t, dir, "a.md", v2, demoMtime.Add(time.Hour))
	stats := mustScan(t, st, dir, Options{})
	if stats.Changed != 1 || stats.New != 0 || stats.Removed != 0 {
		t.Fatalf("stats = %+v, want changed=1", stats)
	}
	rows := mustRows(t, st)
	if rows["a.md"].Title != "New Title" || rows["a.md"].Summary != "New paragraph." {
		t.Fatalf("changed row not refreshed: %+v", rows["a.md"])
	}
	if rows["a.md"].ContentHash != hashOf(v2) {
		t.Fatalf("ContentHash not updated")
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly 1 (UPSERT on path, not a second row)", rows)
	}
}

func TestScanUnchangedOnTouchOnly(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	content := "# Demo\n\nParagraph.\n"
	writeNote(t, dir, "a.md", content, demoMtime)
	mustScan(t, st, dir, Options{})
	before := mustRows(t, st)["a.md"]
	// mtime-only change: the first filter mismatches, the recomputed hash
	// matches, so the row must NOT be rewritten.
	writeNote(t, dir, "a.md", content, demoMtime.Add(2*time.Hour))
	stats := mustScan(t, st, dir, Options{})
	if stats.New != 0 || stats.Changed != 0 || stats.Removed != 0 {
		t.Fatalf("stats = %+v, want all deltas 0 on a touch-only change", stats)
	}
	after := mustRows(t, st)["a.md"]
	if after.Mtime.UnixNano() != before.Mtime.UnixNano() {
		t.Fatalf("touch-only change rewrote the row: %v -> %v", before.Mtime, after.Mtime)
	}
}

func TestScanIdempotentRescanZeroWrites(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# Demo\n\nParagraph.\n", demoMtime)
	writeNote(t, dir, "b.md", "# Demo 2\n\nParagraph 2.\n", demoMtime)
	mustScan(t, st, dir, Options{})
	var appliesAfterFirst int
	if f, ok := st.(*fakeStore); ok {
		appliesAfterFirst = f.applies
	}
	stats := mustScan(t, st, dir, Options{})
	if stats.Scanned != 2 || stats.New != 0 || stats.Changed != 0 || stats.Removed != 0 || stats.Skipped != 0 {
		t.Fatalf("rescan stats = %+v, want scanned=2 all deltas 0", stats)
	}
	if f, ok := st.(*fakeStore); ok && f.applies != appliesAfterFirst {
		t.Fatalf("no-change rescan wrote (applies %d -> %d)", appliesAfterFirst, f.applies)
	}
	if len(mustRows(t, st)) != 2 {
		t.Fatalf("rows = %v, want 2", mustRows(t, st))
	}
}

func TestScanRemovalAndRename(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# A\n\nPara a.\n", demoMtime)
	writeNote(t, dir, "b.md", "# B\n\nPara b.\n", demoMtime)
	mustScan(t, st, dir, Options{})
	// Delete a.md; rename b.md -> c.md (same content, same mtime).
	if err := os.Remove(filepath.Join(dir, "a.md")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "b.md"), filepath.Join(dir, "c.md")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	stats := mustScan(t, st, dir, Options{})
	if stats.New != 1 || stats.Removed != 2 || stats.Changed != 0 {
		t.Fatalf("stats = %+v, want new=1 removed=2 (a.md deleted + b.md renamed away)", stats)
	}
	rows := mustRows(t, st)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want only c.md", rows)
	}
	if _, ok := rows["c.md"]; !ok {
		t.Fatalf("renamed row c.md missing: %v", rows)
	}
}

func TestScanDuplicateNoteNonUniqueHash(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	content := "# Same\n\nSame paragraph.\n"
	writeNote(t, dir, "a.md", content, demoMtime)
	writeNote(t, dir, "copy.md", content, demoMtime)
	stats := mustScan(t, st, dir, Options{})
	if stats.New != 2 {
		t.Fatalf("stats = %+v, want new=2", stats)
	}
	rows := mustRows(t, st)
	if rows["a.md"].ContentHash != rows["copy.md"].ContentHash {
		t.Fatalf("identical content must share the hash (duplicate-note axis)")
	}
	if len(rows) != 2 {
		t.Fatalf("duplicate-content note dropped: %v", rows)
	}
}

func TestScanSkipDirs(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, ".obsidian/config.md", "# Hidden\n\nx\n", demoMtime)
	writeNote(t, dir, ".trash/deleted.md", "# Gone\n\nx\n", demoMtime)
	writeNote(t, dir, "notes/demo.md", "# Demo\n\nx\n", demoMtime)
	stats := mustScan(t, st, dir, Options{})
	if stats.Scanned != 1 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v, want scanned=1 (skip dirs are not walked)", stats)
	}
	rows := mustRows(t, st)
	if len(rows) != 1 || rows["notes/demo.md"].Title != "Demo" {
		t.Fatalf("rows = %+v, want only notes/demo.md", rows)
	}
	// A custom override replaces the default list entirely.
	st2 := testStore(t)
	writeNote(t, dir, "private/secret.md", "# P\n\nx\n", demoMtime)
	stats = mustScan(t, st2, dir, Options{SkipDirs: []string{"private", ".trash"}})
	rows = mustRows(t, st2)
	if _, ok := rows["private/secret.md"]; ok {
		t.Fatalf("custom skip dir still indexed: %v", rows)
	}
	if _, ok := rows[".obsidian/config.md"]; !ok {
		t.Fatalf("default .obsidian must be indexed when overridden out: %v", rows)
	}
	if _, ok := rows[".trash/deleted.md"]; ok {
		t.Fatalf(".trash must be skipped by the override: %v", rows)
	}
}

func TestScanLimitSkipped(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# A\n\nx\n", demoMtime)
	writeNote(t, dir, "b.md", "# B\n\nx\n", demoMtime)
	writeNote(t, dir, "c.md", "# C\n\nx\n", demoMtime)
	stats := mustScan(t, st, dir, Options{Limit: 1})
	if stats.Scanned != 1 || stats.Skipped != 2 {
		t.Fatalf("stats = %+v, want scanned=1 skipped=2", stats)
	}
	rows := mustRows(t, st)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 (limit)", rows)
	}
	// Skipped files must not be treated as removed on the next full scan.
	stats = mustScan(t, st, dir, Options{})
	if stats.New != 2 || stats.Removed != 0 {
		t.Fatalf("full rescan stats = %+v, want new=2 removed=0 (skipped files survive)", stats)
	}
	if len(mustRows(t, st)) != 3 {
		t.Fatalf("rows = %v, want 3", mustRows(t, st))
	}
}

func TestScanLimitDerivedFromCap(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "a.md", "# A\n\nx\n", demoMtime)
	writeNote(t, dir, "b.md", "# B\n\nx\n", demoMtime)
	writeNote(t, dir, "c.md", "# C\n\nx\n", demoMtime)
	stats := mustScan(t, st, dir, Options{Limit: 100, ScanMaxFiles: 2})
	if stats.Scanned != 2 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, want scanned=2 skipped=1 (cap wins over limit)", stats)
	}
	// limit 0 = unlimited: the cap applies alone.
	st2 := testStore(t)
	stats = mustScan(t, st2, dir, Options{ScanMaxFiles: 2})
	if stats.Scanned != 2 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, want scanned=2 skipped=1 (cap with limit 0)", stats)
	}
}

func TestScanTitleFallbackAndFields(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "plain-note.md", "Just a body, no heading.\n", demoMtime)
	mustScan(t, st, dir, Options{})
	rows := mustRows(t, st)
	if rows["plain-note.md"].Title != "plain-note" {
		t.Fatalf("Title = %q, want file-name stem %q", rows["plain-note.md"].Title, "plain-note")
	}
	if rows["plain-note.md"].Summary != "Just a body, no heading." {
		t.Fatalf("Summary = %q", rows["plain-note.md"].Summary)
	}
}

func TestScanCodeFenceHeadingAndParagraphExcluded(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	content := "```\n# Fenced Fake Title\nfenced code paragraph\n```\n# Real Title\n\nReal paragraph.\n"
	writeNote(t, dir, "a.md", content, demoMtime)
	mustScan(t, st, dir, Options{})
	rows := mustRows(t, st)
	if rows["a.md"].Title != "Real Title" {
		t.Fatalf("Title = %q, want %q (fence interiors excluded — parseMarkdownSections inheritance)", rows["a.md"].Title, "Real Title")
	}
	if rows["a.md"].Summary != "Real paragraph." {
		t.Fatalf("Summary = %q, want %q (fenced content excluded)", rows["a.md"].Summary, "Real paragraph.")
	}
}

func TestScanFrontmatterVariantsStored(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	cases := []struct{ rel, content string }{
		{"flow.md", "---\ntags: [alpha, beta]\n---\n# Flow\n\nFlow body.\n"},
		{"block.md", "---\ntags:\n  - alpha\n  - beta\n---\n# Block\n\nBlock body.\n"},
		{"quoted.md", "---\ntags: \"alpha\"\nother: demo\n---\n# Quoted\n\nQuoted body.\n"},
	}
	for _, c := range cases {
		writeNote(t, dir, c.rel, c.content, demoMtime)
	}
	stats := mustScan(t, st, dir, Options{})
	if stats.New != 3 {
		t.Fatalf("stats = %+v, want new=3", stats)
	}
	rows := mustRows(t, st)
	wantTags := map[string]string{"flow.md": "alpha,beta", "block.md": "alpha,beta", "quoted.md": "alpha"}
	for rel, want := range wantTags {
		if rows[rel].Tags != want {
			t.Fatalf("%s Tags = %q, want %q", rel, rows[rel].Tags, want)
		}
		if rows[rel].Summary == "" || strings.Contains(rows[rel].Summary, "tags") {
			t.Fatalf("%s Summary = %q, want the body paragraph (frontmatter excluded)", rel, rows[rel].Summary)
		}
	}
}

func TestScanFrontmatterEmptyAndCRLF(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "empty-fm.md", "---\n---\r\n# Empty FM\n\r\nBody after empty frontmatter.\r\n", demoMtime)
	writeNote(t, dir, "crlf.md", "---\r\ntags: [a]\r\n---\r\n# CRLF Title\r\n\r\nCRLF body paragraph.\r\n", demoMtime)
	mustScan(t, st, dir, Options{})
	rows := mustRows(t, st)
	if rows["empty-fm.md"].Title != "Empty FM" {
		t.Fatalf("empty frontmatter Title = %q, want %q", rows["empty-fm.md"].Title, "Empty FM")
	}
	if rows["empty-fm.md"].Summary != "Body after empty frontmatter." {
		t.Fatalf("empty frontmatter Summary = %q", rows["empty-fm.md"].Summary)
	}
	if rows["crlf.md"].Title != "CRLF Title" {
		t.Fatalf("CRLF Title = %q, want %q", rows["crlf.md"].Title, "CRLF Title")
	}
	if rows["crlf.md"].Summary != "CRLF body paragraph." {
		t.Fatalf("CRLF Summary = %q", rows["crlf.md"].Summary)
	}
	if rows["crlf.md"].Tags != "a" {
		t.Fatalf("CRLF Tags = %q, want %q", rows["crlf.md"].Tags, "a")
	}
}

func TestScanSummaryTruncateRunes(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	long := strings.Repeat("가", 300) // 300 runes > the 200-rune default cap
	writeNote(t, dir, "long.md", "# Long\n\n"+long+"\n", demoMtime)
	mustScan(t, st, dir, Options{})
	rows := mustRows(t, st)
	if got := len([]rune(rows["long.md"].Summary)); got != 200 {
		t.Fatalf("summary runes = %d, want 200 (TruncateRunes)", got)
	}
}

// TestScanVaultDiffRealSlow — the real-DB end-to-end (FLINT_TEST_DSN):
// new -> changed -> removed on an isolated scratch database, verifying the
// persisted rows directly (the ON CONFLICT path upsert is exercised by the
// second scan on the same path).
func TestScanVaultDiffRealSlow(t *testing.T) {
	if os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	t.Setenv("SUPERLITE_REAL", "1")
	st := realStore(t)
	dir := t.TempDir()
	writeNote(t, dir, "demo.md", "# Demo\n\nDemo paragraph.\n", demoMtime)
	stats, err := Scan(ctx, st, dir, Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if stats.New != 1 {
		t.Fatalf("stats = %+v, want new=1", stats)
	}
	// Rewrite with a later mtime -> changed (path UNIQUE UPSERT, not a new row).
	writeNote(t, dir, "demo.md", "# Demo v2\n\nDemo paragraph v2.\n", demoMtime.Add(time.Hour))
	stats, err = Scan(ctx, st, dir, Options{})
	if err != nil {
		t.Fatalf("Scan 2: %v", err)
	}
	if stats.Changed != 1 || stats.New != 0 {
		t.Fatalf("stats = %+v, want changed=1 new=0", stats)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM vault_files`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("vault_files rows = %d, want 1 (UPSERT axis)", n)
	}
	// Delete -> removed.
	if err := os.Remove(filepath.Join(dir, "demo.md")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	stats, err = Scan(ctx, st, dir, Options{})
	if err != nil {
		t.Fatalf("Scan 3: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("stats = %+v, want removed=1", stats)
	}
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM vault_files`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("vault_files rows = %d, want 0 after removal", n)
	}
}
