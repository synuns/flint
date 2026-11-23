package backup

import (
	"bytes"
	"errors"
	"os"
	"strings"
)

var ErrMaskedDump = errors.New("backup: masked dump is not restorable (diagnostics/sharing only)")

var ErrKeyPathRequired = errors.New("backup: encrypted dump — decryption key path required")

const MaskDumpHeader = "-- flint-masked-dump: never restore (masked diagnostic artifact)"

func ReadDumpBytes(path, keyPath string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadDumpFrom(b, keyPath)
}

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
			continue
		}
		return false
	}
	return false
}
