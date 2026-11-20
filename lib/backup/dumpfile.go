// dumpfile.go — the single path for reading dump files (shared by restore and
// drill). Owns age format detection -> decryption and masked-dump rejection
// (a non-restorable artifact) in one place, per the no-sidecar principle and
// as a second line of defense.
package backup

import (
	"bytes"
	"errors"
	"os"
	"strings"
)

// ErrMaskedDump — a masked dump is a non-restorable artifact for diagnostics and sharing.
var ErrMaskedDump = errors.New("backup: masked dump is not restorable (diagnostics/sharing only)")

// ErrKeyPathRequired — the dump is encrypted but no key path was provided.
var ErrKeyPathRequired = errors.New("backup: encrypted dump — decryption key path required")

// MaskDumpHeader — the leading marker of masked dump files (the basis for restore/drill rejection).
const MaskDumpHeader = "-- flint-masked-dump: never restore (masked diagnostic artifact)"

// ReadDumpBytes — reads a dump file and returns replayable plaintext bytes.
// An age-format file is decrypted with the keyPath identity; a masked marker
// yields ErrMaskedDump. An encrypted file with a blank keyPath yields
// ErrKeyPathRequired.
func ReadDumpBytes(path, keyPath string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadDumpFrom(b, keyPath)
}

// ReadDumpFrom — reads back pre-read (verified) bytes (closes the restore
// TOCTOU — decrypts and inspects the verified bytes without re-reading the
// file).
func ReadDumpFrom(b []byte, keyPath string) ([]byte, error) {
	if ok, _ := IsEncrypted(bytes.NewReader(b)); ok {
		if keyPath == "" {
			return nil, ErrKeyPathRequired
		}
		id, err := LoadIdentity(keyPath)
		if err != nil {
			return nil, err
		}
		b, err = DecryptFrom(bytes.NewReader(b), id)
		if err != nil {
			return nil, err
		}
	}
	if isMaskedDump(b) {
		return nil, ErrMaskedDump
	}
	return b, nil
}

// isMaskedDump — checks the plaintext leading marker (comment form — harmless to the SQL parser).
func isMaskedDump(b []byte) bool {
	head := b
	if len(head) > 512 {
		head = head[:512]
	}
	for _, line := range strings.Split(string(head), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			if strings.HasPrefix(t, MaskDumpHeader) {
				return true
			}
			continue // skip leading comments and blank lines while scanning for the marker
		}
		return false // body starts — no marker
	}
	return false
}
