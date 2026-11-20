package backup

// Rotate targets only flint-generated dumps (flint-YYYYMMDD-HHMMSS-PID.sql
// or .age) — user-placed .sql/.age in the same directory are excluded from
// both deletion and the keep count (data-loss prevention).

import (
	"path/filepath"
	"testing"
	"time"
)

// Plan-specified: 2 flint dumps (old, new) + a user file (the oldest
// mtime) -> keep 1 -> only the old flint deleted; the user file remains.
func TestRotateSkipsForeignDumps(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	writeDump(t, dir, "mine.sql", base.Add(-48*time.Hour), false) // the oldest mtime
	writeDump(t, dir, "flint-20260101-000000-1.sql", base.Add(-24*time.Hour), false)
	writeDump(t, dir, "flint-20260102-000000-2.sql", base, false) // newest

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	want := "flint-20260101-000000-1.sql"
	if len(removed) != 1 || removed[0] != want {
		t.Fatalf("removed=%v, want [%s] (사이드카 없음)", removed, want)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
	mustExist(t, filepath.Join(dir, "flint-20260102-000000-2.sql"))
	mustGone(t, filepath.Join(dir, want))
}

// Plan-specified: user files only -> no-op — (nil, nil).
func TestRotateForeignOnlyNoop(t *testing.T) {
	dir := t.TempDir()
	writeDump(t, dir, "mine.sql", time.Now(), true)
	writeDump(t, dir, "manual.age", time.Now().Add(-time.Hour), false)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if removed != nil {
		t.Fatalf("removed=%v, want nil", removed)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
	mustExist(t, filepath.Join(dir, "mine.sql.sha256"))
	mustExist(t, filepath.Join(dir, "manual.age"))
}

// Pattern boundary: a single character off the flint-generated form makes it a user file.
func TestRotatePatternBoundaries(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	foreign := []string{
		"flint-202601-000000-1.sql",            // 7-digit date
		"flint-20260101-00000-1.sql",           // 5-digit time
		"flint-20260101-000000-x.sql",          // non-numeric PID
		"flint-20260101-000000-.sql",           // empty PID
		"flint-20260101-000000-1.txt",          // extension mismatch
		"flint-20260101-000000-1.sqlx",         // extension suffix
		"xflint-20260101-000000-1.sql",         // prefix
		"flint--20260101-000000-1.sql",         // doubled dash
		"mybackup-flint-20260101-000000-1.sql", // inserted in the middle
	}
	for i, name := range foreign {
		writeDump(t, dir, name, base.Add(time.Duration(i)*time.Hour), false)
	}
	writeDump(t, dir, "flint-20260101-000000-42.age", base.Add(-100*time.Hour), false) // a target (the oldest)
	writeDump(t, dir, "flint-20260113-235959-999.sql", base, false)                    // a target (newest — occupies keep 1)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "flint-20260101-000000-42.age" {
		t.Fatalf("removed=%v, want [flint-20260101-000000-42.age]", removed)
	}
	for _, name := range foreign {
		mustExist(t, filepath.Join(dir, name))
	}
}

// Contract-source consistency: the real producer (lazyDumpFile) names
// encrypted dumps flint-<date8>-<time6>-<pid>.sql.age (done.go ext =
// ".sql.age"). If the pattern missed this form, encrypted backups would be
// permanently excluded from rotation (unbounded accumulation).
func TestRotateMatchesEncryptedDumpName(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	enc := "flint-20261112-080000-77.sql.age"             // exactly the producer's assembled form
	writeDump(t, dir, enc, base.Add(-24*time.Hour), true) // .sql.age.sha256 sidecar
	writeDump(t, dir, "flint-20261113-090000-78.sql", base, false)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != enc || removed[1] != enc+".sha256" {
		t.Fatalf("removed=%v, want [%s %s.sha256]", removed, enc, enc)
	}
	mustGone(t, filepath.Join(dir, enc))
	mustGone(t, filepath.Join(dir, enc+".sha256"))
	mustExist(t, filepath.Join(dir, "flint-20261113-090000-78.sql"))
}

// A user file does not occupy a keep slot — 3 flint dumps + 1 user file
// (newest) -> even with keep 2, one flint is deleted.
func TestRotateForeignDoesNotOccupyKeepSlot(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	writeDump(t, dir, "flint-20260101-000000-1.sql", base.Add(-3*time.Hour), false)
	writeDump(t, dir, "flint-20260102-000000-2.sql", base.Add(-2*time.Hour), false)
	writeDump(t, dir, "flint-20260103-000000-3.sql", base.Add(-1*time.Hour), false)
	writeDump(t, dir, "mine.sql", base, false) // newest but excluded from the count

	removed, err := Rotate(dir, 2)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "flint-20260101-000000-1.sql" {
		t.Fatalf("removed=%v, want [flint-20260101-000000-1.sql]", removed)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
}
