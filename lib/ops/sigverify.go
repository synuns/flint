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

var ErrSignatureInvalid = errors.New("release signature: signature mismatch")

var ErrSignatureScheme = errors.New("release signature: unsupported scheme")

type ReleaseVerifier struct {
	PublicPEM []byte
}

func (v ReleaseVerifier) Configured() bool {
	return len(v.PublicPEM) > 0
}

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
