// sign.go — v1.6.0 T4: PAM §18 Ed25519 문서 서명. 키 관리(생성/로드 —
// configDir()/pam-signing-key, 0600)·did:key:ed25519 공개키 인코딩·
// payload{checksum, export_id, export_date, owner_id} JCS → Ed25519
// 서명/검증. 스토어 무접촉 — 경로는 PAMSigningKeyPathFn seam(기본
// FLINT_CONFIG_DIR > wire.ConfigDir > os.UserConfigDir()/flint).
package knowledge

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // UUIDv5는 RFC 4122 표준 해시 — 보안 용도 아님
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

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/synuns/flint/lib/wire"
)

// pamSigningKeyName — 키 파일명(configDir 하위).
const pamSigningKeyName = "pam-signing-key"

// DefaultPAMSigningKeyPath — 서명 키 기본 경로: FLINT_CONFIG_DIR >
// wire.ConfigDir 주입(ops) > os.UserConfigDir()/flint. 어디로도 못 찾으면
// ""(호출측 오류).
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

// PAMSigningKeyPathFn — 경로 seam(테스트 격리 주입).
var PAMSigningKeyPathFn = DefaultPAMSigningKeyPath

// LoadOrCreatePAMSigningKey — 서명 키 로드, 없으면 생성해 0600으로
// 기록(부모 디렉터리 0700). 파일 포맷은 hex(64바이트 개인키 + 개행).
// 파손 키는 조용 재생성이 아니라 오류 — 재생성은 owner did:key를 갈아
// 놓아 기존 서명의 신뢰 사슬을 끊는다.
func LoadOrCreatePAMSigningKey() (ed25519.PrivateKey, error) {
	path := PAMSigningKeyPathFn()
	if path == "" {
		return nil, errors.New("pam signing key path unavailable (config dir unresolved)")
	}
	if b, err := os.ReadFile(path); err == nil {
		raw, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("pam signing key corrupt (want %d-byte hex): %s",
				ed25519.PrivateKeySize, path)
		}
		return ed25519.PrivateKey(raw), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("pam signing key read failed: %w", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pam signing key generation failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("pam signing key dir create failed: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("pam signing key write failed: %w", err)
	}
	return priv, nil
}

// base58Alphabet — bitcoin 계열 base58btc(did:key multibase z 접두 관례).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Encode — base58btc 인코딩(선행 0바이트는 '1').
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
	// 계산 순서상 역방향 적립 — 뒤집는다.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// base58Decode — base58btc 디코딩(역방향 대응).
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

// didKeyEd25519Prefix — did:key + multibase base58btc(z) 접두.
const didKeyEd25519Prefix = "did:key:z"

// PAMOwnerDID — Ed25519 공개키 → did:key(multicodec ed25519-pub 0xed 0x01
// + 키, base58btc) — ed25519 did:key는 항상 "z6Mk"로 시작한다.
func PAMOwnerDID(pub ed25519.PublicKey) string {
	b := make([]byte, 0, 2+len(pub))
	b = append(b, 0xed, 0x01)
	b = append(b, pub...)
	return didKeyEd25519Prefix + base58Encode(b)
}

// PAMDIDKeyPubkey — did:key(ed25519-pub multicodec) → 공개키. 접두·
// multicodec·길이 불일치는 오류.
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

// pamSignPayload — §18 서명 대상: 문서 식별·무결성 4필드. 필드 순서는
// JCS 정규화로 무의미(재현 보장).
type pamSignPayload struct {
	Checksum   string `json:"checksum"`
	ExportID   string `json:"export_id"`
	ExportDate string `json:"export_date"`
	OwnerID    string `json:"owner_id"`
}

// pamPayloadCanonical — payload → JCS 정규 바이트(서명·검증 공통).
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

// PAMSignPayload — payload JCS → Ed25519 서명 → base64(문서 value 필드).
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

// PAMVerifySignature — base64 서명 → payload JCS 검증.
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

// pamRelationNamespace — relation id(UUIDv5) 네임스페이스 — 고정 합성
// 값(표준 UUID가 아닌 flint 자체 파생 — 문서 간 재현만이 계약).
var pamRelationNamespace = func() [16]byte {
	h := sha256.Sum256([]byte("flint:pam:relation:v1"))
	var ns [16]byte
	copy(ns[:], h[:16])
	return ns
}()

// pamRelationID — §13.1 relation id: (from, to, type)에서 유도하는 결정적
// UUIDv5 — 동일 관계는 항상 동일 id로 재발행된다(외부 검증 재현성).
func pamRelationID(from, to, typ string) string {
	h := sha1.New() //nolint:gosec // RFC 4122 UUIDv5 표준 알고리즘 — 보안 해시 용도 아님
	h.Write(pamRelationNamespace[:])
	h.Write([]byte(from + "\x1f" + to + "\x1f" + typ))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
