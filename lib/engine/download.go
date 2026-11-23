package engine

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var maxDownloadBytes int64 = 512 << 20

var runtimePlatform = func() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}

var execVersion = func(bin string) (string, error) {
	out, err := exec.Command(bin, "-version").Output()
	if err != nil {
		return "", fmt.Errorf("engine: run %s -version: %w", bin, err)
	}
	return string(out), nil
}

func engineHomeDir() string {
	if d := os.Getenv("FLINT_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".flint")
}

func downloadURL(ver, goos, goarch string) string {
	return fmt.Sprintf("https://github.com/dolthub/doltgresql/releases/download/v%s/doltgresql-%s-%s.tar.gz", ver, goos, goarch)
}

func Download(ctx context.Context, ver string) (string, error) {
	if ver == "" {
		ver = NativePinVersion
	}
	goos, goarch := runtimePlatform()
	wantHash, err := PinnedHash(goos, goarch)
	if err != nil {
		return "", err
	}
	u := os.Getenv("FLINT_ENGINE_URL")
	if u == "" {
		u = downloadURL(ver, goos, goarch)
	}

	home := engineHomeDir()
	if home == "" {
		return "", errors.New("engine: home directory unavailable — set FLINT_HOME")
	}
	engDir := filepath.Join(home, "engine")
	if err := os.MkdirAll(engDir, 0o700); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(engDir, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	tarPath := filepath.Join(work, "engine.tar.gz")
	sum, err := fetchTarball(ctx, u, tarPath)
	if err != nil {
		return "", err
	}
	if sum != wantHash {
		return "", fmt.Errorf("engine: sha256 mismatch for %s — want %s, got %s", u, wantHash, sum)
	}

	extDir := filepath.Join(work, "x")
	if err := os.MkdirAll(extDir, 0o700); err != nil {
		return "", err
	}
	binRel, err := extractBinary(tarPath, extDir)
	if err != nil {
		return "", err
	}

	out, err := execVersion(filepath.Join(extDir, binRel))
	if err != nil {
		return "", err
	}
	if v := versionFromOutput(out); v != ver {
		return "", fmt.Errorf("engine: binary version mismatch — want %s, got %q", ver, v)
	}

	staging, err := os.MkdirTemp(engDir, ".staging-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := copyBinary(filepath.Join(extDir, binRel), filepath.Join(staging, "doltgres")); err != nil {
		return "", err
	}

	final := filepath.Join(engDir, ver)
	if err := os.RemoveAll(final); err != nil {
		return "", err
	}
	if err := os.Rename(staging, final); err != nil {
		return "", err
	}

	tmpLink := filepath.Join(engDir, ".current-tmp")
	if err := os.Remove(tmpLink); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Symlink(ver, tmpLink); err != nil {
		return "", err
	}
	if err := os.Rename(tmpLink, filepath.Join(engDir, "current")); err != nil {
		return "", err
	}
	return final, nil
}

func fetchTarball(ctx context.Context, u, dst string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("engine: download %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("engine: download %s: status %s", u, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("engine: download %s: %w", u, err)
	}
	if n > maxDownloadBytes {
		return "", fmt.Errorf("engine: download %s exceeds size limit of %d bytes", u, maxDownloadBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractBinary(tarPath, dir string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("engine: unpack %s: %w", tarPath, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	fallback := ""
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("engine: unpack %s: %w", tarPath, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
			return "", fmt.Errorf("engine: unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		mode := os.FileMode(hdr.Mode & 0o777)
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", fmt.Errorf("engine: unpack %s: %w", tarPath, err)
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if name == "bin/doltgres" {
			return name, nil
		}
		if name == "doltgres" && fallback == "" {
			fallback = name
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("engine: doltgres binary not found in archive")
}

func versionFromOutput(out string) string {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}
