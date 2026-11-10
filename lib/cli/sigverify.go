// sigverify.go — v0.31.0(D·ADR-0014): 릴리스 서명 검증. cosign 키 기반
// 서명(ECDSA P-256 기본·ed25519 지원)을 Go 표준 라이브러리만으로 검증
// 한다(신규 의존 0 — cosign 라이브러리 트리 회피). 신뢰 뿌리는 바이너리에
// 임베드된 공개키(cosign.pub) — GitHub 오리진과 분리된 제3 신뢰점.
package cli

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

// ErrSignatureInvalid — 서명 불일치(위조·손상 의심 — 업데이트 즉시 거부).
var ErrSignatureInvalid = errors.New("release signature: 서명 불일치")

// ErrSignatureScheme — 미지원 키/형식(공개키 스킴·서명 인코딩).
var ErrSignatureScheme = errors.New("release signature: 미지원 스킴")

// ReleaseVerifier — 임베드 공개키 기반 검증기(빈 공개키 = 미구성 전환 모드).
type ReleaseVerifier struct {
	PublicPEM []byte // cosign.pub 원문(비었으면 검증 건너뜀)
}

// Configured — 공개키 임베드 여부(미구성이면 .sig가 있어도 경고만).
func (v ReleaseVerifier) Configured() bool {
	return len(v.PublicPEM) > 0
}

// Verify — data(예: checksums.txt 원문)에 대한 base64 서명 검증.
// 공개키 타입에 따라 ECDSA(SHA-256+ASN.1)·ed25519(순수)로 분기 —
// cosign sign-blob --key의 두 표준 스킴.
func (v ReleaseVerifier) Verify(data []byte, sigB64 string) error {
	if !v.Configured() {
		return fmt.Errorf("release signature: 공개키 미구성(전환 모드)")
	}
	block, _ := pem.Decode(v.PublicPEM)
	if block == nil {
		return fmt.Errorf("release signature: 공개키 PEM 파싱 실패")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("release signature: 공개키 파싱: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("%w: 서명 base64", ErrSignatureScheme)
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
		return fmt.Errorf("%w: 공개키 타입 %T", ErrSignatureScheme, pubAny)
	}
}
