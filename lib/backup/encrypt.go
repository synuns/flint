// encrypt.go — at-rest backup encryption (age, ADR lineage — PLAN F).
// Based on an X25519 identity file (backup.key in the config directory,
// 0600) — a key file rather than a passphrase prompt because automatic
// backups must run non-interactively (ruling ②). Threat model: preventing
// backup files from leaking off the device (sync, copy, or upload
// accidents) — full device compromise that steals the key file too is out
// of scope (RUNBOOK).
package backup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

// ageHeader — the fixed prefix of the age binary format (format detection).
const ageHeader = "age-encryption.org/"

// ErrEncryptKeyMissing — identity file absent (first use, etc.).
var ErrEncryptKeyMissing = errors.New("backup encrypt: identity key file missing — see RUNBOOK 'backup encryption'")

// ErrEncryptKeyCorrupt — the identity file exists but cannot be parsed (no
// overwriting — prevents silently making existing key ciphertext permanently
// undecryptable).
var ErrEncryptKeyCorrupt = errors.New("backup encrypt: identity key file corrupt — generating a new key would make existing ciphertext undecryptable (manual check: RUNBOOK)")

// LoadIdentity — loads an age X25519 identity from path (absent ->
// ErrEncryptKeyMissing, corrupt -> ErrEncryptKeyCorrupt — the distinction
// keeps EnsureIdentity from silently overwriting a corrupt key).
func LoadIdentity(path string) (*age.X25519Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrEncryptKeyMissing
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, ErrEncryptKeyCorrupt
	}
	return id, nil
}

// EnsureIdentity — loads the identity, creating and storing it only when
// absent. A corrupt existing key is an error (preventing a silent, permanent
// loss of decryptability for backups encrypted with the old key — audit
// finding). Creation claims first with O_EXCL (avoiding a race where parallel
// first-time encryptions each make different keys and the loser's ciphertext
// is lost forever — the claim winner's key file becomes the single
// original). If a crash during first creation leaves a corrupt file behind,
// regeneration is refused (manual inspection — RUNBOOK).
func EnsureIdentity(path string) (*age.X25519Identity, error) {
	if id, err := LoadIdentity(path); err == nil {
		return id, nil
	} else if !errors.Is(err, ErrEncryptKeyMissing) {
		return nil, err // corrupt key — do not overwrite
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("backup encrypt: generate key: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, fmt.Errorf("backup encrypt: key directory: %w", err)
	}
	// O_EXCL claims first — if it already exists (a parallel winner), reload and use that key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return reloadIdentityRetry(path)
		}
		return nil, fmt.Errorf("backup encrypt: claim key creation: %w", err)
	}
	if _, err := f.WriteString(id.String() + "\n"); err != nil { // a single small write
		f.Close()
		os.Remove(path) // claim failed — allow regeneration on the next attempt
		return nil, fmt.Errorf("backup encrypt: write key: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("backup encrypt: write key: %w", err)
	}
	return id, nil
}

// reloadIdentityRetry — reloads the parallel winner's key (two brief retries
// in case the first read right after writing fails).
func reloadIdentityRetry(path string) (*age.X25519Identity, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if id, err := LoadIdentity(path); err == nil {
			return id, nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, lastErr
}

// dirOf — the directory of a path (empty -> ".").
func dirOf(path string) string {
	return filepath.Dir(path)
}

// EncryptTo — writes plaintext to dst as age ciphertext for the recipient.
func EncryptTo(dst io.Writer, plaintext []byte, recipient *age.X25519Recipient) error {
	w, err := age.Encrypt(dst, recipient)
	if err != nil {
		return fmt.Errorf("backup encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("backup encrypt: write: %w", err)
	}
	return w.Close()
}

// DecryptFrom — decrypts age ciphertext with the identity and returns the plaintext.
func DecryptFrom(src io.Reader, identity *age.X25519Identity) ([]byte, error) {
	r, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, fmt.Errorf("backup decrypt: %w", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("backup decrypt: %w", err)
	}
	return b, nil
}

// IsEncrypted — detects the age format from the leading bytes. Consumes the first 17 bytes.
func IsEncrypted(r io.Reader) (bool, error) {
	head := make([]byte, len(ageHeader))
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	return n == len(ageHeader) && bytes.Equal(head, []byte(ageHeader)), nil
}

// IsEncryptedFile — detects the age format of a file path (absent file -> false).
func IsEncryptedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	ok, err := IsEncrypted(f)
	return err == nil && ok
}
