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

const ageHeader = "age-encryption.org/"

var ErrEncryptKeyMissing = errors.New("backup encrypt: identity key file missing — see RUNBOOK 'backup encryption'")

var ErrEncryptKeyCorrupt = errors.New("backup encrypt: identity key file corrupt — generating a new key would make existing ciphertext undecryptable (manual check: RUNBOOK)")

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

func EnsureIdentity(path string) (*age.X25519Identity, error) {
	if id, err := LoadIdentity(path); err == nil {
		return id, nil
	} else if !errors.Is(err, ErrEncryptKeyMissing) {
		return nil, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("backup encrypt: generate key: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, fmt.Errorf("backup encrypt: key directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return reloadIdentityRetry(path)
		}
		return nil, fmt.Errorf("backup encrypt: claim key creation: %w", err)
	}
	if _, err := f.WriteString(id.String() + "\n"); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("backup encrypt: write key: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("backup encrypt: write key: %w", err)
	}
	return id, nil
}

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

func dirOf(path string) string {
	return filepath.Dir(path)
}

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

func IsEncrypted(r io.Reader) (bool, error) {
	head := make([]byte, len(ageHeader))
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	return n == len(ageHeader) && bytes.Equal(head, []byte(ageHeader)), nil
}

func IsEncryptedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	ok, err := IsEncrypted(f)
	return err == nil && ok
}
