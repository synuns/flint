// sign.go — PAM §18 Ed25519 document signing. Key management (generation/
// loading — configDir()/pam-signing-key, 0600)·did:key:ed25519 public-key
// encoding·payload{checksum, export_id, export_date, owner_id} JCS →
// Ed25519 signing/verification. Never touches the store — paths go through
// the PAMSigningKeyPathFn seam (default FLINT_CONFIG_DIR > wire.ConfigDir >
// os.UserConfigDir()/flint).
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

// pamSigningKeyName — the key file name (under configDir).
const pamSigningKeyName = "pam-signing-key"

// DefaultPAMSigningKeyPath — the default signing key path: FLINT_CONFIG_DIR >
// injected wire.ConfigDir (ops) > os.UserConfigDir()/flint. If none resolves,
// "" (caller-side error).
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

// PAMSigningKeyPathFn — the path seam (test isolation injection).
var PAMSigningKeyPathFn = DefaultPAMSigningKeyPath

// pamKeyReadFile — the key file read seam (default os.ReadFile — atomicity
// regression tests such as the forced EEXIST path inject the read sequence).
var pamKeyReadFile = os.ReadFile

// loadPAMSigningKeyFile — loads the key file hex (64-byte private key). A
// corrupt key is an error, not a silent regeneration — regenerating would
// swap the owner did:key and break the trust chain of existing signatures.
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

// pamKeyEEXISTRetries·pamKeyEEXISTBackoff — the EEXIST retry budget:
// absorbs the instant a concurrent creator made the file but has not yet
// flushed the write (incomplete contents). Budget exhaustion is an error
// (inheriting the no-silent-regeneration rule).
const (
	pamKeyEEXISTRetries = 20
	pamKeyEEXISTBackoff = 5 * time.Millisecond
)

// LoadOrCreatePAMSigningKey — loads the signing key, or generates one and
// writes it 0600 (parent directory 0700). File format is hex (64-byte
// private key + newline). v1.6.1 T2 A1: creation uses an atomic
// O_CREATE|O_EXCL open — fundamentally blocking the race where concurrent
// calls each issue a key and overwrite each other (returning different keys
// per call). EEXIST is absorbed by loading the preceding creator's existing
// file.
func LoadOrCreatePAMSigningKey() (ed25519.PrivateKey, error) {
	path := PAMSigningKeyPathFn()
	if path == "" {
		return nil, errors.New("pam signing key path unavailable (config dir unresolved)")
	}
	if k, err := loadPAMSigningKeyFile(path); err == nil {
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// A corruption verdict may be the moment just before a concurrent
		// creator's write lands — converge on the completed write with a
		// bounded retry instead of failing immediately.
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
			// A preceding concurrent creator already made the file — discard
			// the new key and load the existing one (single-key guarantee).
			// Incomplete contents from the pre-write instant are absorbed
			// with a short retry.
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

// loadPAMSigningKeyAfterRace — loads the existing file after EEXIST. The
// preceding creator may be just after the O_EXCL open and just before the
// write, so a corruption (incomplete hex) verdict retries within the budget.
// If it is still unreadable after the budget is spent, an error (no
// regeneration).
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
		// Both ErrNotExist (file gone) and corruption (incomplete hex) may
		// be the write-in-progress instant, so retry within the budget.
	}
	return nil, fmt.Errorf("pam signing key concurrent create unreadable: %w", lastErr)
}

// base58Alphabet — bitcoin-style base58btc (the did:key multibase z-prefix
// convention).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Encode — base58btc encoding (leading zero bytes become '1').
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
	// The accumulation order is reversed by construction — flip it.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// base58Decode — base58btc decoding (the reverse correspondence).
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

// didKeyEd25519Prefix — the did:key + multibase base58btc (z) prefix.
const didKeyEd25519Prefix = "did:key:z"

// PAMOwnerDID — Ed25519 public key → did:key (multicodec ed25519-pub 0xed
// 0x01 + key, base58btc) — an ed25519 did:key always starts with "z6Mk".
func PAMOwnerDID(pub ed25519.PublicKey) string {
	b := make([]byte, 0, 2+len(pub))
	b = append(b, 0xed, 0x01)
	b = append(b, pub...)
	return didKeyEd25519Prefix + base58Encode(b)
}

// PAMDIDKeyPubkey — did:key (ed25519-pub multicodec) → public key. Prefix·
// multicodec·length mismatches are errors.
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

// pamSignPayload — the §18 signing target: four fields of document identity
// and integrity. Field order is irrelevant under JCS normalization
// (reproduction guaranteed).
type pamSignPayload struct {
	Checksum   string `json:"checksum"`
	ExportID   string `json:"export_id"`
	ExportDate string `json:"export_date"`
	OwnerID    string `json:"owner_id"`
}

// pamPayloadCanonical — payload → JCS canonical bytes (shared by signing
// and verification).
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

// PAMSignPayload — payload JCS → Ed25519 signature → base64 (the document
// value field).
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

// PAMVerifySignature — base64 signature → payload JCS verification.
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

// pamRelationNamespace — the relation id (UUIDv5) namespace — a fixed
// synthetic value (a flint derivation, not a standard UUID — only
// cross-document reproducibility is the contract).
var pamRelationNamespace = func() [16]byte {
	h := sha256.Sum256([]byte("flint:pam:relation:v1"))
	var ns [16]byte
	copy(ns[:], h[:16])
	return ns
}()

// pamRelationID — the §13.1 relation id: a deterministic UUIDv5 derived
// from (from, to, type) — the same relation is always reissued with the
// same id (external verification reproducibility).
func pamRelationID(from, to, typ string) string {
	h := sha1.New() //nolint:gosec // RFC 4122 UUIDv5 standard algorithm — not a security hash use
	h.Write(pamRelationNamespace[:])
	h.Write([]byte(from + "\x1f" + to + "\x1f" + typ))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
