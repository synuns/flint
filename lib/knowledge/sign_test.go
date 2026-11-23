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
	"time"
)

func pamSyntheticKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed := sha256.Sum256([]byte("flint-pam-eexist-demo-seed"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func TestLoadOrCreatePAMSigningKeyForcedEEXIST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pam-signing-key")
	pamSwapKeyPath(t, path)

	existing := pamSyntheticKey(t)
	if werr := os.WriteFile(path, []byte(hex.EncodeToString(existing)+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}

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

	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.TrimSpace(string(b)) != hex.EncodeToString(existing) {
		t.Errorf("키 파일이 덮어써짐 — 원자 생성 위반")
	}
}

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

func TestLoadOrCreatePAMSigningKeyPartialFileRetriesUntilComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pam-signing-key")
	pamSwapKeyPath(t, path)

	if werr := os.WriteFile(path, []byte("abc\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	full := pamSyntheticKey(t)
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(path, []byte(hex.EncodeToString(full)+"\n"), 0o600)
	}()

	got, err := LoadOrCreatePAMSigningKey()
	if err != nil {
		t.Fatalf("불완전 파일 재시도 실패: %v", err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("완전 기록 이후의 키와 불일치")
	}
}
