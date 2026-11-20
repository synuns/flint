package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// ADR-0006 backup integrity — unit tests for Checksum, WriteSidecar, and
// VerifyFile (fast). The original dump is replaced by a fake buffer; only
// SUPERLITE_REAL=1 uses the real Dump.

// fakeDumpBuffer — a dump buffer for tests (fake). With SUPERLITE_REAL=1, a real-DB Dump.
func fakeDumpBuffer(t *testing.T) (*bytes.Buffer, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if os.Getenv("SUPERLITE_REAL") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		pool, err := db.Connect(ctx, testDSN())
		if err != nil {
			t.Skipf("real dump unavailable: %v", err)
		}
		pool.Close()
		if err := Dump(ctx, testDSN(), &buf); err != nil {
			t.Fatalf("real dump: %v", err)
		}
	} else {
		buf.WriteString("-- flint backup (ADR-0002) probe\nBEGIN;\nINSERT INTO t VALUES ('x');\nCOMMIT;\n")
	}
	return &buf, append([]byte(nil), buf.Bytes()...)
}

// Test code (as planned): Dump -> tamper -> Verify failure.
// Original intact — the tamper check does not touch the original file.
func TestChecksumRoundtrip(t *testing.T) {
	buf, data := fakeDumpBuffer(t)

	written, sum, err := Checksum(buf)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if written != int64(len(data)) {
		t.Fatalf("written=%d, want %d", written, len(data))
	}
	want := sha256.Sum256(data)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum=%s, want %s", sum, hex.EncodeToString(want[:]))
	}

	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSidecar(path, sum); err != nil {
		t.Fatalf("sidecar: %v", err)
	}

	got, err := VerifyFile(path)
	if err != nil || got != sum {
		t.Fatalf("verify ok path: sum=%s err=%v, want %s nil", got, err, sum)
	}

	// Tamper — append a statement to the original, re-verify -> mismatch.
	tampered := append(append([]byte(nil), data...), []byte("DELETE FROM knowledge;\n")...)
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("tampered verify err=%v, want ErrChecksumMismatch", err)
	}
	// Original intact: restoring it passes again (VerifyFile did not modify the file).
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path); err != nil {
		t.Fatalf("restored original fails verify: %v", err)
	}
}

// Missing sidecar path — the warn sentinel + the computed hash returned.
func TestVerifyFileMissingSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.sql")
	data := []byte("BEGIN;\nCOMMIT;\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := VerifyFile(path)
	if !errors.Is(err, ErrNoSidecar) {
		t.Fatalf("err=%v, want ErrNoSidecar", err)
	}
	want := sha256.Sum256(data)
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("sum=%s, want %s", got, hex.EncodeToString(want[:]))
	}
	// Original intact — no sidecar is created.
	if _, statErr := os.Stat(path + ".sha256"); !os.IsNotExist(statErr) {
		t.Fatalf("sidecar should not be created by VerifyFile: %v", statErr)
	}
}

// The sidecar must be in sha256sum -c compatible form ("<hex>  <basename>\n"),
// and VerifyFile compares only the first token (filename ignored — moves
// allowed).
func TestSidecarSha256sumCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dump.sql")
	data := []byte("-- dump\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if err := WriteSidecar(path, hex.EncodeToString(want[:])); err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if string(side) != hex.EncodeToString(want[:])+"  dump.sql\n" {
		t.Fatalf("sidecar=%q, want sha256sum format", side)
	}
	// Even with a different filename (moved/copied), it passes on the hash token alone.
	moved := filepath.Join(dir, "moved.sql")
	if err := os.WriteFile(moved, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved+".sha256", []byte(hex.EncodeToString(want[:])+"  other-name.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(moved); err != nil {
		t.Fatalf("moved sidecar should verify: %v", err)
	}
}
