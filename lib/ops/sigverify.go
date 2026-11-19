// sigverify.go — release signature verification (ADR-0014). Verifies cosign key-based
// signatures (ECDSA P-256 default, ed25519 supported) using only the Go standard library
// (0 new dependencies — avoiding the cosign library tree). The trust root is the public key
// embedded in the binary (cosign.pub) — a third trust point separate from the GitHub origin.
package ops

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ErrSignatureInvalid — signature mismatch (suspected forgery or corruption — the update is rejected immediately).
var ErrSignatureInvalid = errors.New("release signature: signature mismatch")

// ErrSignatureScheme — unsupported key/format (public-key scheme, signature encoding).
var ErrSignatureScheme = errors.New("release signature: unsupported scheme")

// ReleaseVerifier — the verifier based on the embedded public key (an empty public key = unconfigured transition mode).
type ReleaseVerifier struct {
	PublicPEM []byte // the raw cosign.pub (verification skipped when empty)
}

// Configured — whether the public key is embedded (if unconfigured, even a .sig present is only a warning).
func (v ReleaseVerifier) Configured() bool {
	return len(v.PublicPEM) > 0
}

// Verify — verifies a base64 signature over data (e.g. the raw checksums.txt).
// Branches by public-key type into ECDSA (SHA-256+ASN.1) or ed25519 (raw) —
// the two standard schemes of cosign sign-blob --key.
func (v ReleaseVerifier) Verify(data []byte, sigB64 string) error {
	if !v.Configured() {
		return fmt.Errorf("release signature: public key not configured (transition mode)")
	}
	block, _ := pem.Decode(v.PublicPEM)
	if block == nil {
		return fmt.Errorf("release signature: cannot parse public key PEM")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("release signature: parse public key: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("%w: signature base64", ErrSignatureScheme)
	}
	switch pub := pubAny.(type) {
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(data)
		if ecdsa.VerifyASN1(pub, digest[:], sig) {
			return nil
		}
		return ErrSignatureInvalid
	case ed25519.PublicKey:
		if ed25519.Verify(pub, data, sig) {
			return nil
		}
		return ErrSignatureInvalid
	default:
		return fmt.Errorf("%w: public key type %T", ErrSignatureScheme, pubAny)
	}
}
