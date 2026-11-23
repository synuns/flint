package knowledge

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // UUIDv5 is the RFC 4122 standard hash — not a security use
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/synuns/flint/lib/wire"
)

const pamSigningKeyName = "pam-signing-key"

func DefaultPAMSigningKeyPath() string {
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return filepath.Join(d, pamSigningKeyName)
	}
	if wire.ConfigDir != nil {
		if d := wire.ConfigDir(); d != "" {
			return filepath.Join(d, pamSigningKeyName)
		}
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "flint", pamSigningKeyName)
	}
	return ""
}

var PAMSigningKeyPathFn = DefaultPAMSigningKeyPath

var pamKeyReadFile = os.ReadFile

func loadPAMSigningKeyFile(path string) (ed25519.PrivateKey, error) {
	b, err := pamKeyReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, derr := hex.DecodeString(strings.TrimSpace(string(b)))
	if derr != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("pam signing key corrupt (want %d-byte hex): %s",
			ed25519.PrivateKeySize, path)
	}
	return ed25519.PrivateKey(raw), nil
}

const (
	pamKeyEEXISTRetries = 20
	pamKeyEEXISTBackoff = 5 * time.Millisecond
)

func LoadOrCreatePAMSigningKey() (ed25519.PrivateKey, error) {
	path := PAMSigningKeyPathFn()
	if path == "" {
		return nil, errors.New("pam signing key path unavailable (config dir unresolved)")
	}
	if k, err := loadPAMSigningKeyFile(path); err == nil {
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {

		return loadPAMSigningKeyAfterRace(path)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pam signing key generation failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("pam signing key dir create failed: %w", err)
	}
	f, oerr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if oerr != nil {
		if errors.Is(oerr, os.ErrExist) {

			return loadPAMSigningKeyAfterRace(path)
		}
		return nil, fmt.Errorf("pam signing key create failed: %w", oerr)
	}
	if _, werr := f.WriteString(hex.EncodeToString(priv) + "\n"); werr != nil {
		f.Close()
		return nil, fmt.Errorf("pam signing key write failed: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return nil, fmt.Errorf("pam signing key write failed: %w", cerr)
	}
	return priv, nil
}

func loadPAMSigningKeyAfterRace(path string) (ed25519.PrivateKey, error) {
	var lastErr error
	for i := 0; i < pamKeyEEXISTRetries; i++ {
		if i > 0 {
			time.Sleep(pamKeyEEXISTBackoff)
		}
		k, err := loadPAMSigningKeyFile(path)
		if err == nil {
			return k, nil
		}
		lastErr = err

	}
	return nil, fmt.Errorf("pam signing key concurrent create unreadable: %w", lastErr)
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58Encode(b []byte) string {
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, '1')
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func base58Decode(s string) ([]byte, error) {
	n := new(big.Int)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(base58Alphabet, s[i])
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", s[i])
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(idx)))
	}
	body := n.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	return append(make([]byte, zeros), body...), nil
}

const didKeyEd25519Prefix = "did:key:z"

func PAMOwnerDID(pub ed25519.PublicKey) string {
	b := make([]byte, 0, 2+len(pub))
	b = append(b, 0xed, 0x01)
	b = append(b, pub...)
	return didKeyEd25519Prefix + base58Encode(b)
}

func PAMDIDKeyPubkey(did string) (ed25519.PublicKey, error) {
	s, ok := strings.CutPrefix(did, didKeyEd25519Prefix)
	if !ok {
		return nil, fmt.Errorf("not a base58btc did:key: %q", did)
	}
	b, err := base58Decode(s)
	if err != nil {
		return nil, fmt.Errorf("did:key decode failed: %w", err)
	}
	if len(b) != 2+ed25519.PublicKeySize || b[0] != 0xed || b[1] != 0x01 {
		return nil, fmt.Errorf("did:key is not ed25519-pub multicodec: %q", did)
	}
	return ed25519.PublicKey(b[2:]), nil
}

type pamSignPayload struct {
	Checksum   string `json:"checksum"`
	ExportID   string `json:"export_id"`
	ExportDate string `json:"export_date"`
	OwnerID    string `json:"owner_id"`
}

func pamPayloadCanonical(p pamSignPayload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("pam sign payload marshal failed: %w", err)
	}
	canonical, err := jsoncanonicalizer.Transform(b)
	if err != nil {
		return nil, fmt.Errorf("pam sign payload JCS failed: %w", err)
	}
	return canonical, nil
}

func PAMSignPayload(key ed25519.PrivateKey, p pamSignPayload) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("pam signing key invalid (want %d bytes)", ed25519.PrivateKeySize)
	}
	canonical, err := pamPayloadCanonical(p)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, canonical)), nil
}

func PAMVerifySignature(pub ed25519.PublicKey, value string, p pamSignPayload) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	canonical, err := pamPayloadCanonical(p)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, canonical, raw)
}

var pamRelationNamespace = func() [16]byte {
	h := sha256.Sum256([]byte("flint:pam:relation:v1"))
	var ns [16]byte
	copy(ns[:], h[:16])
	return ns
}()

func pamRelationID(from, to, typ string) string {
	h := sha1.New() //nolint:gosec // RFC 4122 UUIDv5 standard algorithm — not a security hash use
	h.Write(pamRelationNamespace[:])
	h.Write([]byte(from + "\x1f" + to + "\x1f" + typ))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
