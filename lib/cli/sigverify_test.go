// sigverify_test.go — v0.31.0: cosign 키 서명 검증(실제 cosign v2.6 생성
// 픽스처 — 합성 테스트 키). 표준 라이브러리 경로(ECDSA·ed25519) 계약.
package cli

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

// TestReleaseVerifierCosignFixture — 실제 cosign이 생성·서명한 픽스처 검증.
func TestReleaseVerifierCosignFixture(t *testing.T) {
	pub, err := os.ReadFile("testdata/cosign-ecdsa.pub")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile("testdata/checksums-blob.txt")
	sig, _ := os.ReadFile("testdata/checksums-ecdsa.sig")
	v := ReleaseVerifier{PublicPEM: pub}
	if !v.Configured() {
		t.Fatal("Configured=false")
	}
	if err := v.Verify(data, string(sig)); err != nil {
		t.Fatalf("cosign 서명 검증 실패: %v", err)
	}
	// 내용 변조 → 불일치.
	if err := v.Verify([]byte("tampered"), string(sig)); err == nil {
		t.Fatal("변조 데이터 통과 — 서명 무용")
	}
	// 서명 변조 → 불일치.
	bad := []byte(strings.TrimSpace(string(sig)))
	bad[10] ^= 0xFF
	if err := v.Verify(data, string(bad)); err == nil {
		t.Fatal("변조 서명 통과")
	}
}

// TestReleaseVerifierEd25519 — ed25519 스킴(순수 서명·SPKI PEM).
func TestReleaseVerifierEd25519(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	data := []byte("checksums payload")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	v := ReleaseVerifier{PublicPEM: pemBytes}
	if err := v.Verify(data, sig); err != nil {
		t.Fatalf("ed25519 검증: %v", err)
	}
	if err := v.Verify(data, base64.StdEncoding.EncodeToString([]byte("garbage"))); err == nil {
		t.Fatal("쓰레기 서명 통과")
	}
}

// TestReleaseVerifierECDSAStd — ECDSA 스킴(SHA-256+ASN.1 — cosign 기본).
func TestReleaseVerifierECDSAStd(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	data := []byte("checksums payload")
	digest := sha256.Sum256(data)
	sigASN, _ := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	v := ReleaseVerifier{PublicPEM: pemBytes}
	if err := v.Verify(data, base64.StdEncoding.EncodeToString(sigASN)); err != nil {
		t.Fatalf("ecdsa 검증: %v", err)
	}
}

// TestReleaseVerifierUnconfigured — 미구성 전환 모드(빈 공개키 경고 계약).
func TestReleaseVerifierUnconfigured(t *testing.T) {
	v := ReleaseVerifier{}
	if v.Configured() {
		t.Fatal("빈 키인데 Configured")
	}
	if err := v.Verify([]byte("x"), "c2ln"); err == nil {
		t.Fatal("미구성인데 검증 통과")
	}
}
