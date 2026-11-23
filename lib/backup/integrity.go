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
	ErrNoSidecar = errors.New("checksum sidecar missing")

	ErrChecksumMismatch = errors.New("checksum mismatch")
)

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

func WriteSidecar(path, sum string) error {
	line := sum + "  " + filepath.Base(path) + "\n"
	sp := path + ".sha256"
	if err := os.WriteFile(sp, []byte(line), 0o600); err != nil {
		return err
	}

	return os.Chmod(sp, 0o600)
}

func VerifyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return VerifyBytes(data, path+".sha256")
}

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
