package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setupDownload(t *testing.T, tarball []byte, versionOutput string) string {
	t.Helper()
	sum := sha256.Sum256(tarball)
	savedHash := pinHashes["darwin/arm64"]
	pinHashes["darwin/arm64"] = hex.EncodeToString(sum[:])
	savedPlatform := runtimePlatform
	runtimePlatform = func() (string, string) { return "darwin", "arm64" }
	savedExec := execVersion
	execVersion = func(string) (string, error) { return versionOutput, nil }
	savedMax := maxDownloadBytes
	maxDownloadBytes = 512 << 20
	t.Cleanup(func() {
		pinHashes["darwin/arm64"] = savedHash
		runtimePlatform = savedPlatform
		execVersion = savedExec
		maxDownloadBytes = savedMax
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("FLINT_ENGINE_URL", ts.URL)
	home := t.TempDir()
	t.Setenv("FLINT_HOME", home)
	return home
}

func TestDownloadInstallsPinnedBinary(t *testing.T) {
	tb := buildTarball(t, map[string]string{
		"bin/doltgres":     "fake-binary",
		"licenses/LICENSE": "demo license",
	})
	home := setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")

	dir, err := Download(context.Background(), "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := filepath.Join(home, "engine", NativePinVersion)
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	fi, err := os.Stat(filepath.Join(dir, "doltgres"))
	if err != nil {
		t.Fatalf("installed binary: %v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %o, want 755", fi.Mode().Perm())
	}
	link, err := os.Readlink(filepath.Join(home, "engine", "current"))
	if err != nil {
		t.Fatalf("current symlink: %v", err)
	}
	if link != NativePinVersion {
		t.Fatalf("current -> %q, want %q", link, NativePinVersion)
	}
	entries, err := os.ReadDir(filepath.Join(home, "engine"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("residue left behind: %s", e.Name())
		}
	}
}

func TestDownloadSelectsTopLevelBinary(t *testing.T) {
	tb := buildTarball(t, map[string]string{"doltgres": "fake-binary"})
	setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")

	dir, err := Download(context.Background(), NativePinVersion)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "doltgres")); err != nil {
		t.Fatalf("top-level doltgres not selected: %v", err)
	}
}

func TestDownloadRejectsHashMismatch(t *testing.T) {
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	home := setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")
	pinHashes["darwin/arm64"] = "0000000000000000000000000000000000000000000000000000000000000000"

	_, err := Download(context.Background(), "")
	if err == nil {
		t.Fatal("Download succeeded, want hash mismatch error")
	}
	if _, serr := os.Stat(filepath.Join(home, "engine", NativePinVersion)); !os.IsNotExist(serr) {
		t.Fatalf("version dir must not exist after hash mismatch: %v", serr)
	}
	if _, lerr := os.Lstat(filepath.Join(home, "engine", "current")); !os.IsNotExist(lerr) {
		t.Fatalf("current symlink must not exist after hash mismatch: %v", lerr)
	}
}

func TestDownloadRejectsOversize(t *testing.T) {
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	home := setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")
	maxDownloadBytes = int64(len(tb)) - 1

	_, err := Download(context.Background(), "")
	if err == nil {
		t.Fatal("Download succeeded, want size limit error")
	}
	if _, serr := os.Stat(filepath.Join(home, "engine", NativePinVersion)); !os.IsNotExist(serr) {
		t.Fatalf("version dir must not exist after oversize: %v", serr)
	}
}

func TestDownloadRejectsVersionMismatch(t *testing.T) {
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	home := setupDownload(t, tb, "Doltgres version 9.9.9\n")

	_, err := Download(context.Background(), "")
	if err == nil {
		t.Fatal("Download succeeded, want version mismatch error")
	}
	if _, serr := os.Stat(filepath.Join(home, "engine", NativePinVersion)); !os.IsNotExist(serr) {
		t.Fatalf("version dir must not exist after version mismatch: %v", serr)
	}
	if _, lerr := os.Lstat(filepath.Join(home, "engine", "current")); !os.IsNotExist(lerr) {
		t.Fatalf("current symlink must not exist after version mismatch: %v", lerr)
	}
}

func TestDownloadReplacesSymlinkAndPreservesOldVersion(t *testing.T) {
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	home := setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")
	engDir := filepath.Join(home, "engine")
	oldDir := filepath.Join(engDir, "0.9.0")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "doltgres"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("0.9.0", filepath.Join(engDir, "current")); err != nil {
		t.Fatal(err)
	}

	if _, err := Download(context.Background(), ""); err != nil {
		t.Fatalf("Download: %v", err)
	}
	link, err := os.Readlink(filepath.Join(engDir, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if link != NativePinVersion {
		t.Fatalf("current -> %q, want %q", link, NativePinVersion)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "doltgres")); err != nil {
		t.Fatalf("old version not preserved: %v", err)
	}
}

func TestDownloadUnsupportedPlatform(t *testing.T) {
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	setupDownload(t, tb, "Doltgres version "+NativePinVersion+"\n")
	runtimePlatform = func() (string, string) { return "windows", "amd64" }

	_, err := Download(context.Background(), "")
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestPinnedHash(t *testing.T) {
	h, err := PinnedHash("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if h != "39ce23c683dc94f4e26599398987d18000de78202904a3466645ccb1f7b039ac" {
		t.Fatalf("darwin/arm64 hash = %q", h)
	}
	for _, p := range [][2]string{{"darwin", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}} {
		if _, err := PinnedHash(p[0], p[1]); !errors.Is(err, ErrUnsupportedPlatform) {
			t.Fatalf("PinnedHash(%s,%s) err = %v, want ErrUnsupportedPlatform", p[0], p[1], err)
		}
	}
}

func TestDownloadURLAssembly(t *testing.T) {
	t.Setenv("FLINT_ENGINE_URL", "")
	got := downloadURL("1.4.0", "darwin", "arm64")
	want := "https://github.com/dolthub/doltgresql/releases/download/v1.4.0/doltgresql-darwin-arm64.tar.gz"
	if got != want {
		t.Fatalf("downloadURL = %q, want %q", got, want)
	}
}
