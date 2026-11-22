// scan.go — the vault diff scan (v1.21.0 vault bridge I). Walks a vault
// root (.md files only, skip dirs per the vault_skip_dirs threshold),
// diffs against the vault_files pointer snapshot with a two-stage filter —
// (mtime, size) mismatch triggers a sha256 recomputation over the RAW
// bytes (deliberately distinct from the knowledge normalized hash) — and
// applies the whole diff in ONE transaction: new rows INSERT, changed rows
// path-UNIQUE UPSERT, vanished paths DELETE (the NOT IN minimal form —
// Doltgres has no DELETE ... USING).
//
// Write-ownership note: this package owns its diff SQL on a pool from
// db.Connect — the same one-shot-transaction precedent as backup.Restore
// (a surface wire.Store lacks under its write-repo-only contract); the
// writes_through_repo guard scope (lib/{ops,knowledge}) does not cover it.
// The dolt commit is the caller's responsibility (the Invalidate-class
// convention — T3 commits "index: vault files=N changed=M").
package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
)

// File — one vault note's pointer snapshot (the vault_files row shape).
// The original text is never copied: only derived fields (title, summary,
// tags) plus the raw-bytes hash. Path is vault-root-relative with forward
// slashes (stable under vault moves — no absolute machine paths stored).
type File struct {
	Path        string
	Title       string
	Tags        string // comma-joined
	Mtime       time.Time
	Size        int64
	ContentHash string // sha256 hex of the raw bytes
	Summary     string
}

// Existing — the first-filter columns of an indexed row.
type Existing struct {
	Mtime       time.Time
	Size        int64
	ContentHash string
}

// Store — the scan's DB surface. Existing feeds the first filter;
// ApplyDiff applies the whole diff in one transaction and returns the
// removed-row count; Rows exposes the snapshot (the diff's read side and
// the test seam).
type Store interface {
	Existing(ctx context.Context) (map[string]Existing, error)
	ApplyDiff(ctx context.Context, upserts []File, keepPaths []string) (int64, error)
	Rows(ctx context.Context) (map[string]File, error)
}

// Options — scan knobs; zero values fall back to store.DefaultThresholds
// (the consumer-fallback convention — unset keeps 0/""/nil).
type Options struct {
	Limit           int      // 0 = unlimited (the cap alone applies)
	ScanMaxFiles    int      // vault_scan_max_files (default 10000)
	SummaryMaxRunes int      // vault_summary_max_runes (default 200)
	SkipDirs        []string // vault_skip_dirs (default .obsidian, .trash)
}

// Stats — one scan result (the `flint index` JSON payload basis).
type Stats struct {
	Scanned  int
	New      int
	Changed  int
	Removed  int
	Skipped  int
	Duration time.Duration
}

// Scan — walks root and applies the diff to st. Files beyond the effective
// limit are counted Skipped and are NOT deleted from the snapshot (the
// removal set is the walked set, scanned + skipped — a partial scan must
// not purge what it did not look at).
func Scan(ctx context.Context, st Store, root string, opts Options) (Stats, error) {
	start := time.Now()
	def := store.DefaultThresholds()
	maxFiles := opts.ScanMaxFiles
	if maxFiles <= 0 {
		maxFiles = def.VaultScanMaxFiles
	}
	summaryRunes := opts.SummaryMaxRunes
	if summaryRunes <= 0 {
		summaryRunes = def.VaultSummaryMaxRunes
	}
	skip := map[string]bool{}
	if len(opts.SkipDirs) > 0 {
		for _, d := range opts.SkipDirs {
			if d = strings.TrimSpace(d); d != "" {
				skip[d] = true
			}
		}
	} else {
		for _, d := range strings.Split(def.VaultSkipDirs, ",") {
			if d = strings.TrimSpace(d); d != "" {
				skip[d] = true
			}
		}
	}
	// Limit derivation: an explicit --limit wins while under the
	// vault_scan_max_files cap; 0 (unlimited) leaves the cap alone.
	limit := maxFiles
	if opts.Limit > 0 && opts.Limit < maxFiles {
		limit = opts.Limit
	}

	// Walk (lexical order — deterministic limit splits).
	var walked []walkedFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil // vanished mid-walk — no longer an indexable file
		}
		walked = append(walked, walkedFile{
			rel:   filepath.ToSlash(rel),
			full:  path,
			mtime: info.ModTime().Truncate(time.Microsecond), // Postgres timestamptz precision
			size:  info.Size(),
		})
		return nil
	})
	if err != nil {
		return Stats{}, fmt.Errorf("vault scan: walk %s: %w", root, err)
	}

	scanned := walked
	skipped := 0
	if len(walked) > limit {
		scanned = walked[:limit]
		skipped = len(walked) - limit
	}

	existing, err := st.Existing(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("vault scan: existing rows: %w", err)
	}

	var upserts []File
	stats := Stats{Scanned: len(scanned), Skipped: skipped}
	for _, wf := range scanned {
		ex, known := existing[wf.rel]
		if known && ex.Mtime.Equal(wf.mtime) && ex.Size == wf.size {
			continue // first filter clean — no read at all
		}
		f, perr := buildFile(wf, summaryRunes)
		if perr != nil {
			if os.IsNotExist(perr) {
				stats.Skipped++ // vanished between walk and read
				continue
			}
			return Stats{}, fmt.Errorf("vault scan: read %s: %w", wf.rel, perr)
		}
		if known && ex.ContentHash == f.ContentHash {
			// First filter mismatch but content identical (a touch-only
			// change) — unchanged; the stored mtime stays as-is.
			continue
		}
		if known {
			stats.Changed++
		} else {
			stats.New++
		}
		upserts = append(upserts, f)
	}

	// Removal: snapshot rows absent from the whole walked set.
	keep := make([]string, 0, len(walked))
	keepSet := make(map[string]bool, len(walked))
	for _, wf := range walked {
		keep = append(keep, wf.rel)
		keepSet[wf.rel] = true
	}
	for path := range existing {
		if !keepSet[path] {
			stats.Removed++
		}
	}
	if len(upserts) > 0 || stats.Removed > 0 {
		removed, aerr := st.ApplyDiff(ctx, upserts, keep)
		if aerr != nil {
			return Stats{}, fmt.Errorf("vault scan: apply diff: %w", aerr)
		}
		stats.Removed = int(removed)
	}
	stats.Duration = time.Since(start)
	return stats, nil
}

// walkedFile — one walked .md file's identity and stat.
type walkedFile struct {
	rel, full string
	mtime     time.Time
	size      int64
}

// buildFile — reads the raw bytes and derives the pointer snapshot:
// hash (raw sha256), title (first `# ` heading outside code fences, else
// the file-name stem), tags (frontmatter), summary (first non-empty body
// paragraph outside fences, TruncateRunes-capped).
func buildFile(wf walkedFile, summaryRunes int) (File, error) {
	raw, err := os.ReadFile(wf.full)
	if err != nil {
		return File{}, err
	}
	sum := sha256.Sum256(raw)
	fm, body := ParseFrontmatter(string(raw))
	title := firstHeading(body)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(wf.rel), filepath.Ext(wf.rel))
	}
	return File{
		Path:        wf.rel,
		Title:       title,
		Tags:        strings.Join(fm.Tags, ","),
		Mtime:       wf.mtime,
		Size:        wf.size,
		ContentHash: hex.EncodeToString(sum[:]),
		Summary:     render.TruncateRunes(firstParagraph(body), summaryRunes),
	}, nil
}

// fenceTracker — the code-fence state machine inherited from the knowledge
// import path (lib/knowledge/import.go — both ``` and ~~~, CommonMark
// closing rules: same character, at least the opening length). A heading
// or paragraph inside a fence is not document structure.
type fenceTracker struct {
	open   bool
	marker byte
	length int
}

// fenceDelim — a 3+ run of ` or ~ at line start (after trimming).
func fenceDelim(line string) (byte, int, bool) {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return 0, 0, false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n >= 3 {
		return c, n, true
	}
	return 0, 0, false
}

// update — advances the fence state; true = delimiter line, not content.
func (f *fenceTracker) update(line string) bool {
	c, n, isDelim := fenceDelim(line)
	if !isDelim {
		return false
	}
	if !f.open {
		f.open = true
		f.marker = c
		f.length = n
		return true
	}
	if c == f.marker && n >= f.length {
		f.open = false
		return true
	}
	return false
}

// danglingFenceIdx — the starting line of a fence still open at EOF (-1 if
// balanced). An unterminated fence (decorative ~~~, a missing close) would
// swallow all remaining structure — the import.go inheritance.
func danglingFenceIdx(lines []string, skip map[int]bool) int {
	var f fenceTracker
	openIdx := -1
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if _, _, isDelim := fenceDelim(line); isDelim && !f.open {
			openIdx = i
		}
		f.update(line)
	}
	if f.open {
		return openIdx
	}
	return -1
}

// neutralizeDanglingFences — starting lines of unterminated fences,
// recomputed until the document is balanced.
func neutralizeDanglingFences(lines []string) map[int]bool {
	skip := map[int]bool{}
	for {
		idx := danglingFenceIdx(lines, skip)
		if idx < 0 {
			return skip
		}
		skip[idx] = true // that line is body content, not a fence start
	}
}

// firstHeading — the first `# ` heading outside code fences ("" if none).
func firstHeading(body string) string {
	lines := strings.Split(body, "\n")
	skip := neutralizeDanglingFences(lines)
	var f fenceTracker
	for i, line := range lines {
		if !skip[i] && f.update(line) {
			continue
		}
		if f.open {
			continue
		}
		if t := trimCR(line); strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}

// firstParagraph — the first non-empty paragraph of the body: heading
// lines, fence delimiters, and fence interiors are excluded; consecutive
// content lines join with a space (markdown soft-wrap) until a blank line.
func firstParagraph(body string) string {
	lines := strings.Split(body, "\n")
	skip := neutralizeDanglingFences(lines)
	var f fenceTracker
	var para []string
	for i, line := range lines {
		if !skip[i] && f.update(line) {
			continue
		}
		if f.open {
			continue
		}
		t := strings.TrimSpace(trimCR(line))
		if t == "" {
			if len(para) > 0 {
				break // paragraph complete
			}
			continue
		}
		if strings.HasPrefix(t, "#") { // heading lines are not body prose
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, t)
	}
	return strings.Join(para, " ")
}

// PoolStore — the real Store on a Doltgres pool (db.Connect — the shared
// pool config). The whole diff applies inside one transaction.
type PoolStore struct {
	pool *pgxpool.Pool
}

// Open — connects and returns a PoolStore. The caller owns Close.
func Open(ctx context.Context, dsn string) (*PoolStore, error) {
	p, err := db.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &PoolStore{pool: p}, nil
}

// Close tears the pool down.
func (s *PoolStore) Close() { s.pool.Close() }

// Existing — the full snapshot's first-filter columns.
func (s *PoolStore) Existing(ctx context.Context) (map[string]Existing, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT path, mtime, size, content_hash FROM vault_files`)
	if err != nil {
		return nil, fmt.Errorf("vault existing: %w", err)
	}
	defer rows.Close()
	out := map[string]Existing{}
	for rows.Next() {
		var path string
		var ex Existing
		if err := rows.Scan(&path, &ex.Mtime, &ex.Size, &ex.ContentHash); err != nil {
			return nil, fmt.Errorf("vault existing scan: %w", err)
		}
		out[path] = ex
	}
	return out, rows.Err()
}

// Rows — the full snapshot (the diff read side / test seam).
func (s *PoolStore) Rows(ctx context.Context) (map[string]File, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT path, title, tags, mtime, size, content_hash, summary FROM vault_files`)
	if err != nil {
		return nil, fmt.Errorf("vault rows: %w", err)
	}
	defer rows.Close()
	out := map[string]File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.Title, &f.Tags, &f.Mtime, &f.Size, &f.ContentHash, &f.Summary); err != nil {
			return nil, fmt.Errorf("vault rows scan: %w", err)
		}
		out[f.Path] = f
	}
	return out, rows.Err()
}

// notInLimit — the placeholder ceiling of one NOT IN statement (the
// Postgres protocol caps at 65535 bind parameters — 60000 keeps a margin
// far above the 10000 vault_scan_max_files default).
const notInLimit = 60000

// ApplyDiff — the whole diff in ONE transaction: batched multi-row
// upserts, then the NOT IN minimal delete (Doltgres has no DELETE ...
// USING). keepPaths is the complete walked set — a row is removed only if
// its path is in neither the scanned nor the skipped set.
func (s *PoolStore) ApplyDiff(ctx context.Context, upserts []File, keepPaths []string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("vault apply: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	// Multi-row upserts, 50 rows per statement (350 parameters — small
	// enough for every protocol, large enough to avoid 1 round trip/row).
	const batch = 50
	for start := 0; start < len(upserts); start += batch {
		end := start + batch
		if end > len(upserts) {
			end = len(upserts)
		}
		chunk := upserts[start:end]
		var b strings.Builder
		b.WriteString(`INSERT INTO vault_files
    (path, title, tags, mtime, size, content_hash, summary, last_indexed_at)
    VALUES `)
		args := make([]any, 0, len(chunk)*7)
		for i, f := range chunk {
			if i > 0 {
				b.WriteString(", ")
			}
			base := i * 7
			fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, now())",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			args = append(args, f.Path, f.Title, f.Tags, f.Mtime, f.Size, f.ContentHash, f.Summary)
		}
		b.WriteString(`
    ON CONFLICT (path) DO UPDATE SET
        title = excluded.title,
        tags = excluded.tags,
        mtime = excluded.mtime,
        size = excluded.size,
        content_hash = excluded.content_hash,
        summary = excluded.summary,
        last_indexed_at = now()`)
		if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
			return 0, fmt.Errorf("vault apply: upsert batch: %w", err)
		}
	}
	var removed int64
	if len(keepPaths) == 0 {
		ct, err := tx.Exec(ctx, `DELETE FROM vault_files`)
		if err != nil {
			return 0, fmt.Errorf("vault apply: delete all: %w", err)
		}
		removed = ct.RowsAffected()
	} else if len(keepPaths) > notInLimit {
		return 0, fmt.Errorf("vault apply: walked set %d exceeds the NOT IN parameter ceiling %d", len(keepPaths), notInLimit)
	} else {
		var b strings.Builder
		b.WriteString(`DELETE FROM vault_files WHERE path NOT IN (`)
		args := make([]any, 0, len(keepPaths))
		for i, p := range keepPaths {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "$%d", i+1)
			args = append(args, p)
		}
		b.WriteByte(')')
		ct, err := tx.Exec(ctx, b.String(), args...)
		if err != nil {
			return 0, fmt.Errorf("vault apply: delete not-in: %w", err)
		}
		removed = ct.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("vault apply: commit: %w", err)
	}
	return removed, nil
}
