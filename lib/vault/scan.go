// scan.go — the vault diff scan (v1.21.0 vault bridge I). Walks a vault
// root (.md files only, skip dirs per the vault_skip_dirs threshold,
// symlinks never followed), diffs against the vault_files pointer snapshot
// with a two-stage filter — (mtime, size) mismatch triggers a sha256
// recomputation over the RAW bytes (deliberately distinct from the
// knowledge normalized hash) — and applies the whole diff in ONE
// transaction: new rows INSERT, changed rows path-UNIQUE UPSERT (stamping
// content_changed_at — the v1.22.0 re-review queue), touch-only
// rows mtime/size UPDATE, vanished paths DELETE (`NOT IN (SELECT
// unnest($1))` — no placeholder ceiling; Doltgres has no DELETE ... USING).
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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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
// ApplyDiff applies the whole diff in one transaction (content upserts,
// touch-only mtime/size refreshes, then the vanish delete) and returns the
// removed-row count; Rows exposes the snapshot (the diff's read side and
// the test seam).
type Store interface {
	Existing(ctx context.Context) (map[string]Existing, error)
	ApplyDiff(ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error)
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

// titleMaxRunes — the title truncation cap at index time (the
// vault_summary_max_runes analog for the title column; a fixed cap, not a
// threshold key — an unbounded first heading would blow the read-side
// output budget the summary cap already guards).
const titleMaxRunes = 200

// ChangedFile — one changed row of a scan's diff: the vault-relative path
// with the content hash before and after (v1.22.0 — the Stats exposure the
// staleness circuit records; paths here are the same root-relative form the
// snapshot stores, and the index CLI output stays count-only — no path
// ever prints).
type ChangedFile struct {
	RelPath string
	OldHash string
	NewHash string
}

// Stats — one scan result (the `flint index` JSON payload basis). Skipped
// counts ONLY the limit overflow; files vanished between the walk and the
// read are Vanished (v1.21.1 — the two were one counter, so a vanished file
// misfired the "rerun with a higher --limit" note). Touch counts the
// touch-only mtime/size refreshes (excluded from Changed — content is
// identical, but the pointer update deserves its own meter). ChangedFiles
// lists the changed rows for DB-side recording (the re-review queue input);
// new and touch-only rows never appear in it.
type Stats struct {
	Scanned      int
	New          int
	Changed      int
	Removed      int
	Skipped      int
	Vanished     int
	Touch        int
	ChangedFiles []ChangedFile
	Duration     time.Duration
}

// Scan — walks root and applies the diff to st. Files beyond the effective
// limit are counted Skipped (the limit overflow alone) and are NOT deleted
// from the snapshot (the removal set is the walked set, scanned + skipped —
// a partial scan must not purge what it did not look at). Files that
// vanish between the walk and the read are Vanished — a different counter
// than the limit overflow, so the "raise --limit" note never misfires.
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
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return pathFreeErr(werr)
		}
		if d.IsDir() {
			if p != root && skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks are never indexed (v1.21.1): a .md symlink could point
		// outside the vault root (an escape the dir-skip rule never sees) or
		// dangle (a read failure) — skipped symmetrically with directories,
		// which WalkDir never follows either.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// filepath.Ext on the OS-native walk path (the separator-aware
		// form); the rel path below uses path.* — it is always
		// forward-slashed (v1.21.1 r91).
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return pathFreeErr(rerr) // the message names both paths — never surfaced
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil // vanished mid-walk — no longer an indexable file
		}
		walked = append(walked, walkedFile{
			rel:   filepath.ToSlash(rel),
			full:  p,
			mtime: info.ModTime().Truncate(time.Microsecond), // Postgres timestamptz precision
			size:  info.Size(),
		})
		return nil
	})
	if err != nil {
		return Stats{}, fmt.Errorf("vault scan: walk: %w", err)
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

	var upserts, touches []File
	stats := Stats{Scanned: len(scanned), Skipped: skipped}
	for _, wf := range scanned {
		ex, known := existing[wf.rel]
		if known && ex.Mtime.Equal(wf.mtime) && ex.Size == wf.size {
			continue // first filter clean — no read at all
		}
		f, perr := buildFile(wf, summaryRunes)
		if perr != nil {
			if os.IsNotExist(perr) {
				stats.Vanished++ // vanished between walk and read (not a limit skip)
				continue
			}
			return Stats{}, fmt.Errorf("vault scan: read: %w", pathFreeErr(perr))
		}
		if known && ex.ContentHash == f.ContentHash {
			// First filter mismatch but content identical (a touch-only
			// change) — not Changed, but the stored pointer must refresh or
			// EVERY later rescan re-reads and re-hashes the file (the
			// first filter never turns clean again).
			touches = append(touches, f)
			stats.Touch++
			continue
		}
		if known {
			stats.Changed++
			stats.ChangedFiles = append(stats.ChangedFiles, ChangedFile{
				RelPath: wf.rel,
				OldHash: ex.ContentHash,
				NewHash: f.ContentHash,
			})
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
	if len(upserts) > 0 || stats.Removed > 0 || len(touches) > 0 {
		removed, aerr := st.ApplyDiff(ctx, upserts, touches, keep)
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

// pathFreeErr — a classification-preserving, path-free stand-in for an OS
// error whose message names machine paths (fs.PathError prints
// "lstat /abs/vault/root: ..."; filepath.Rel names both sides). The §3
// privacy contract reaches error lines too — index prints the chain
// verbatim via ErrLine — so the errno class is kept as the sentinel
// (fs.ErrNotExist / fs.ErrPermission carry no path) and everything
// unmappable collapses to a generic verdict (v1.21.1 r91/r92/r93).
func pathFreeErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fs.ErrNotExist
	case errors.Is(err, fs.ErrPermission):
		return fs.ErrPermission
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno // e.g. "no space left on device" — a verdict, no path
	}
	return errVaultPath
}

// errVaultPath — the path-free fallback sentinel (the message never names
// the vault root or a note path).
var errVaultPath = errors.New("path resolution failed")

// buildFile — reads the raw bytes and derives the pointer snapshot:
// hash (raw sha256), title (first `# ` heading outside code fences, else
// the file-name stem; titleMaxRunes-capped), tags (frontmatter), summary
// (first non-empty body paragraph outside fences, TruncateRunes-capped).
// The stem derivation uses path (not filepath) — rel is always
// forward-slashed (ToSlash), and filepath's Windows separator would miscut.
func buildFile(wf walkedFile, summaryRunes int) (File, error) {
	raw, err := os.ReadFile(wf.full)
	if err != nil {
		return File{}, err
	}
	sum := sha256.Sum256(raw)
	fm, body := ParseFrontmatter(string(raw))
	title := firstHeading(body)
	if title == "" {
		title = strings.TrimSuffix(path.Base(wf.rel), path.Ext(wf.rel))
	}
	return File{
		Path:        wf.rel,
		Title:       render.TruncateRunes(title, titleMaxRunes),
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

// applyRetryBackoff — the retry spacing for ApplyDiff under parallel index
// runs (the db gateRetryBackoff convention; a shorter ladder — the diff is
// one transaction, and one rematch usually settles it).
var applyRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// retryableApplyErr — the verdict for ApplyDiff retries (v1.21.1 r94):
// 40001 (serialization failure) and the Doltgres commit-time constraint
// rollback (XX000, classified by message — the mapInsertErr convention).
// Under parallel `flint index` runs the losing transaction aborts with one
// of these; a single retry wins because the winner's rows are already
// committed and ON CONFLICT (path) absorbs the rematch.
func retryableApplyErr(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code == "40001" {
		return true
	}
	return pgErr.Code == "XX000" &&
		strings.Contains(pgErr.Message, "constraint violations, transaction rolled back")
}

// ApplyDiff — the whole diff in ONE transaction: batched multi-row
// upserts, touch-only mtime/size refreshes, then the vanish delete
// (`NOT IN (SELECT unnest($1::text[]))` — one array bind parameter, so no
// walked-set ceiling; the placeholder form capped at 60,000 paths,
// v1.21.1 r93). keepPaths is the complete walked set — a row is removed
// only if its path is in neither the scanned nor the skipped set.
// Serializable-conflict losers retry per applyRetryBackoff.
//
// v1.22.0 stamp rule (the re-review queue): an upsert whose content hash
// differs from the stored row stamps content_changed_at = now(); an
// upsert restoring the stored hash (a hash-identical re-apply) returns it
// to NULL — the ONLY erase path. New-row INSERTs stay NULL (a new file is
// not a content change), touch-only refreshes never touch the column, a
// no-change rescan writes nothing (stamp preserved), and a removed row
// takes its stamp with it. The CASE reads the row's PRE-update hash, so it
// must precede the content_hash assignment: Doltgres (unlike Postgres)
// evaluates SET arms sequentially against the partially updated row —
// with the assignment first, the comparison always sees the new hash and
// the stamp never lands.
func (s *PoolStore) ApplyDiff(ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
	var last error
	for i, wait := range applyRetryBackoff {
		if i > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(wait):
			}
		}
		removed, err := applyDiffOnceFn(s, ctx, upserts, touches, keepPaths)
		if err == nil {
			return removed, nil
		}
		last = err
		if !retryableApplyErr(err) {
			return 0, err
		}
	}
	return 0, fmt.Errorf("vault apply: retries exhausted: %w", last)
}

// applyDiffOnceFn — the ApplyDiff transaction seam (the supersedeLockFn
// convention): the retry wrapper composes over this; tests inject
// sequencing failures.
var applyDiffOnceFn = func(s *PoolStore, ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
	return s.applyDiffOnce(ctx, upserts, touches, keepPaths)
}

// applyDiffOnce — one full diff transaction (the retried unit).
func (s *PoolStore) applyDiffOnce(ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
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
        content_changed_at = CASE WHEN vault_files.content_hash = excluded.content_hash
                                   THEN NULL ELSE now() END,
        content_hash = excluded.content_hash,
        summary = excluded.summary,
        last_indexed_at = now()`)
		if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
			return 0, fmt.Errorf("vault apply: upsert batch: %w", err)
		}
	}
	// Touch-only refreshes (v1.21.1 r91/r94): mtime/size alone — content is
	// hash-identical, so derived columns and last_indexed_at stay put.
	// Batched the same 50-row way: mtime/size as CASE arms keyed by path
	// (150 distinct parameters; the path binds are reused three times).
	for start := 0; start < len(touches); start += batch {
		end := start + batch
		if end > len(touches) {
			end = len(touches)
		}
		chunk := touches[start:end]
		var mtimeArms, sizeArms strings.Builder
		args := make([]any, 0, len(chunk)*3)
		for i, f := range chunk {
			if i > 0 {
				mtimeArms.WriteByte(' ')
				sizeArms.WriteByte(' ')
			}
			fmt.Fprintf(&mtimeArms, "WHEN $%d THEN $%d::timestamptz", i*3+1, i*3+2)
			fmt.Fprintf(&sizeArms, "WHEN $%d THEN $%d::bigint", i*3+1, i*3+3)
			args = append(args, f.Path, f.Mtime, f.Size)
		}
		var b strings.Builder
		b.WriteString(`UPDATE vault_files SET mtime = CASE path `)
		b.WriteString(mtimeArms.String())
		b.WriteString(` END, size = CASE path `)
		b.WriteString(sizeArms.String())
		b.WriteString(` END WHERE path IN (`)
		for i := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "$%d", i*3+1)
		}
		b.WriteByte(')')
		if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
			return 0, fmt.Errorf("vault apply: touch batch: %w", err)
		}
	}
	var removed int64
	if len(keepPaths) == 0 {
		ct, err := tx.Exec(ctx, `DELETE FROM vault_files`)
		if err != nil {
			return 0, fmt.Errorf("vault apply: delete all: %w", err)
		}
		removed = ct.RowsAffected()
	} else {
		ct, err := tx.Exec(ctx,
			`DELETE FROM vault_files WHERE path NOT IN (SELECT unnest($1::text[]))`,
			keepPaths)
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
