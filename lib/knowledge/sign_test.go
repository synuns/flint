// sign_test.go — v1.6.1 T2 A1: PAM 서명 키 생성 경로 원자성 회귀.
// LoadOrCreatePAMSigningKey 생성은 O_CREATE|O_EXCL 원자 오픈 — 동시
// 호출이 같은 키 파일을 두벌 생성·상호 덮어쓰지 않는다(EEXIST 강제
// 경로·동시 호출 전수 단일 키). 전부 합성 키(개인 데이터 금지).
package knowledge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// pamSyntheticKey — 고정 시드 합성 Ed25519 키(기존 파일 내용 재현용).
func pamSyntheticKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed := sha256.Sum256([]byte("flint-pam-eexist-demo-seed"))
	return ed25519.NewKeyFromSeed(seed[:])
}

// TestLoadOrCreatePAMSigningKeyForcedEEXIST — EEXIST 강제 경로: 파일이
// 있지만 최초 읽기가 ErrNotExist를 돌려주면(가시성 레이스) 생성 경로로
// 진입하고, O_EXCL 오픈이 EEXIST를 만나면 새 키 발급이 아니라 **기존
// 파일 키를 로드해 반환**한다 — 단일 키 보장(원자 생성 계약).
func TestLoadOrCreatePAMSigningKeyForcedEEXIST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pam-signing-key")
	pamSwapKeyPath(t, path)

	existing := pamSyntheticKey(t)
	if werr := os.WriteFile(path, []byte(hex.EncodeToString(existing)+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	// 첫 ReadFile만 ErrNotExist — 생성 경로 강제 유도(이후 읽기는 실파일).
	old := pamKeyReadFile
	calls := 0
	pamKeyReadFile = func(p string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, os.ErrNotExist
		}
		return old(p)
	}
	t.Cleanup(func() { pamKeyReadFile = old })

	got, err := LoadOrCreatePAMSigningKey()
	if err != nil {
		t.Fatalf("EEXIST 경로 로드 실패: %v", err)
	}
	if !bytes.Equal(got, existing) {
		t.Errorf("EEXIST 경로가 신규 키를 발급/반환 — 기존 파일 키여야 한다(단일 키 위반)")
	}
	// 파일도 기존 키 그대로(덮어쓰지 않음).
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.TrimSpace(string(b)) != hex.EncodeToString(existing) {
		t.Errorf("키 파일이 덮어써짐 — 원자 생성 위반")
	}
}

// TestLoadOrCreatePAMSigningKeyConcurrentSingleKey — 동시 호출 전수가
// 동일한 단일 키를 반환한다(배리어 동시 시작 — WriteFile truncate 레이스
// 회귀: 구현은 마지막 호출의 키로 갱신되어 호출마다 다른 키를 돌려준다).
func TestLoadOrCreatePAMSigningKeyConcurrentSingleKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pam-signing-key")
	pamSwapKeyPath(t, path)

	const n = 16
	keys := make([]ed25519.PrivateKey, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i], errs[i] = LoadOrCreatePAMSigningKey()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("동시 호출 %d 오류: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatalf("동시 호출 키 분기(호출 0 vs %d) — 원자 생성 위반", i)
		}
	}
	// 최종 파일도 동일 키.
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw, derr := hex.DecodeString(strings.TrimSpace(string(b)))
	if derr != nil || len(raw) != ed25519.PrivateKeySize {
		t.Fatalf("최종 키 파일 파손: derr=%v len=%d", derr, len(raw))
	}
	if !bytes.Equal(raw, keys[0]) {
		t.Errorf("최종 파일 키가 반환 키와 불일치 — 상호 덮어쓰기 잔존")
	}
}
