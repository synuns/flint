// ADR-0006 backup integrity — the dump original plus a .sha256 sidecar,
// verified before restore. Backup files execute arbitrary SQL, so the
// checksum is checked for tampering before replay.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNoSidecar — no .sha256 sidecar. The original cannot be confirmed intact: warn and proceed.
	ErrNoSidecar = errors.New("checksum sidecar missing")
	// ErrChecksumMismatch — sidecar mismatch with the original's hash. Grounds for refusing restore.
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

// Checksum returns the byte count and sha256 hex digest from the contents of
// w, where the dump was written (a writer that also implements io.Reader,
// e.g. *bytes.Buffer). It consumes the buffer, so preserve the original
// bytes before calling.
func Checksum(w io.Writer) (written int64, sum string, err error) {
	r, ok := w.(io.Reader)
	if !ok {
		return 0, "", fmt.Errorf("checksum: w must be readable (e.g. *bytes.Buffer)")
	}
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// WriteSidecar writes the sidecar to path+".sha256" in the sha256sum -c
// compatible form ("<hex>  <basename>\n").
func WriteSidecar(path, sum string) error {
	line := sum + "  " + filepath.Base(path) + "\n"
	sp := path + ".sha256"
	if err := os.WriteFile(sp, []byte(line), 0o600); err != nil {
		return err
	}
	// Prevents a relaxed mode lingering on an existing sidecar file
	// (WriteFile's perm applies only to new files).
	return os.Chmod(sp, 0o600)
}

// VerifyFile computes the sha256 of the path original and compares it with
// the first token of the sidecar (path+".sha256"). On success it returns
// the computed hash. A missing sidecar yields ErrNoSidecar (returned with
// the computed hash — for warn-and-proceed); a mismatch yields
// ErrChecksumMismatch. The original file is not modified.
func VerifyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return VerifyBytes(data, path+".sha256")
}

// VerifyBytes — compares the sha256 of pre-read bytes against the sidecar
// (closes the restore TOCTOU — replaying the exact verified bytes leaves no
// window for file replacement between verification and replay).
func VerifyBytes(data []byte, sidecarPath string) (string, error) {
	digest := sha256.Sum256(data)
	got := hex.EncodeToString(digest[:])

	side, err := os.ReadFile(sidecarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return got, ErrNoSidecar
		}
		return got, err
	}
	want := strings.Fields(string(side))
	if len(want) == 0 || want[0] != got {
		return got, ErrChecksumMismatch
	}
	return got, nil
}
