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

type File struct {
	Path        string
	Title       string
	Tags        string
	Mtime       time.Time
	Size        int64
	ContentHash string
	Summary     string
}

type Existing struct {
	Mtime       time.Time
	Size        int64
	ContentHash string
}

type Store interface {
	Existing(ctx context.Context) (map[string]Existing, error)
	ApplyDiff(ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error)
	Rows(ctx context.Context) (map[string]File, error)
}

type Options struct {
	Limit           int
	ScanMaxFiles    int
	SummaryMaxRunes int
	SkipDirs        []string
}

const titleMaxRunes = 200

type ChangedFile struct {
	RelPath string
	OldHash string
	NewHash string
}

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

	limit := maxFiles
	if opts.Limit > 0 && opts.Limit < maxFiles {
		limit = opts.Limit
	}

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

		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return pathFreeErr(rerr)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		walked = append(walked, walkedFile{
			rel:   filepath.ToSlash(rel),
			full:  p,
			mtime: info.ModTime().Truncate(time.Microsecond),
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
			continue
		}
		f, perr := buildFile(wf, summaryRunes)
		if perr != nil {
			if os.IsNotExist(perr) {
				stats.Vanished++
				continue
			}
			return Stats{}, fmt.Errorf("vault scan: read: %w", pathFreeErr(perr))
		}
		if known && ex.ContentHash == f.ContentHash {

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

type walkedFile struct {
	rel, full string
	mtime     time.Time
	size      int64
}

func pathFreeErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fs.ErrNotExist
	case errors.Is(err, fs.ErrPermission):
		return fs.ErrPermission
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return errVaultPath
}

var errVaultPath = errors.New("path resolution failed")

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

type fenceTracker struct {
	open   bool
	marker byte
	length int
}

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

func neutralizeDanglingFences(lines []string) map[int]bool {
	skip := map[int]bool{}
	for {
		idx := danglingFenceIdx(lines, skip)
		if idx < 0 {
			return skip
		}
		skip[idx] = true
	}
}

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
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, t)
	}
	return strings.Join(para, " ")
}

type PoolStore struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*PoolStore, error) {
	p, err := db.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &PoolStore{pool: p}, nil
}

func (s *PoolStore) Close() { s.pool.Close() }

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

var applyRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second}

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

var applyDiffOnceFn = func(s *PoolStore, ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
	return s.applyDiffOnce(ctx, upserts, touches, keepPaths)
}

func (s *PoolStore) applyDiffOnce(ctx context.Context, upserts, touches []File, keepPaths []string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("vault apply: begin: %w", err)
	}
	defer tx.Rollback(ctx)

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
                                   THEN vault_files.content_changed_at ELSE now() END,
        content_hash = excluded.content_hash,
        summary = excluded.summary,
        last_indexed_at = now()`)
		if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
			return 0, fmt.Errorf("vault apply: upsert batch: %w", err)
		}
	}

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
