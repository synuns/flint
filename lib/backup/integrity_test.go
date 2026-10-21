package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flint/lib/db"
)

// ADR-0006 백업 무결성 — Checksum·WriteSidecar·VerifyFile 단위 테스트(fast).
// 원본 덤프는 fake 버퍼로 대체하고, SUPERLITE_REAL=1에서만 실 Dump를 사용한다.

// fakeDumpBuffer — 테스트용 덤프 버퍼(fake). SUPERLITE_REAL=1이면 실DB Dump로.
func fakeDumpBuffer(t *testing.T) (*bytes.Buffer, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if os.Getenv("SUPERLITE_REAL") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		pool, err := db.Connect(ctx, testDSN())
		if err != nil {
			t.Skipf("real dump unavailable: %v", err)
		}
		pool.Close()
		if err := Dump(ctx, testDSN(), &buf); err != nil {
			t.Fatalf("real dump: %v", err)
		}
	} else {
		buf.WriteString("-- flint backup (ADR-0002) probe\nBEGIN;\nINSERT INTO t VALUES ('x');\nCOMMIT;\n")
	}
	return &buf, append([]byte(nil), buf.Bytes()...)
}

// Test code(계획 그대로): Dump→변조→Verify 실패.
// Review Focus(state): 원본 무결 — 변조 검사가 원본 파일을 건드리지 않는다.
func TestChecksumRoundtrip(t *testing.T) {
	buf, data := fakeDumpBuffer(t)

	written, sum, err := Checksum(buf)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if written != int64(len(data)) {
		t.Fatalf("written=%d, want %d", written, len(data))
	}
	want := sha256.Sum256(data)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum=%s, want %s", sum, hex.EncodeToString(want[:]))
	}

	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSidecar(path, sum); err != nil {
		t.Fatalf("sidecar: %v", err)
	}

	got, err := VerifyFile(path)
	if err != nil || got != sum {
		t.Fatalf("verify ok path: sum=%s err=%v, want %s nil", got, err, sum)
	}

	// 변조 — 원본 끝에 문장 추가 후 재검증 → 불일치.
	tampered := append(append([]byte(nil), data...), []byte("DELETE FROM knowledge;\n")...)
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("tampered verify err=%v, want ErrChecksumMismatch", err)
	}
	// 원본 무결: 되살리면 다시 통과(VerifyFile이 파일을 수정하지 않았다).
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path); err != nil {
		t.Fatalf("restored original fails verify: %v", err)
	}
}

// Review Focus(file): 사이드카 누락 경로 — 경고용 센티널 + 계산된 해시 반환.
func TestVerifyFileMissingSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.sql")
	data := []byte("BEGIN;\nCOMMIT;\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := VerifyFile(path)
	if !errors.Is(err, ErrNoSidecar) {
		t.Fatalf("err=%v, want ErrNoSidecar", err)
	}
	want := sha256.Sum256(data)
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("sum=%s, want %s", got, hex.EncodeToString(want[:]))
	}
	// 원본 무결 — 사이드카는 만들지 않는다.
	if _, statErr := os.Stat(path + ".sha256"); !os.IsNotExist(statErr) {
		t.Fatalf("sidecar should not be created by VerifyFile: %v", statErr)
	}
}

// 사이드카는 sha256sum -c 호환 형식("<hex>  <basename>\n")이어야 하며,
// VerifyFile은 첫 토큰만 대조한다(파일명 무시 — 경로 이동 허용).
func TestSidecarSha256sumCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dump.sql")
	data := []byte("-- dump\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if err := WriteSidecar(path, hex.EncodeToString(want[:])); err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if string(side) != hex.EncodeToString(want[:])+"  dump.sql\n" {
		t.Fatalf("sidecar=%q, want sha256sum format", side)
	}
	// 파일명이 달라져도(이동 복사) 해시 토큰만 보고 통과.
	moved := filepath.Join(dir, "moved.sql")
	if err := os.WriteFile(moved, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved+".sha256", []byte(hex.EncodeToString(want[:])+"  other-name.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(moved); err != nil {
		t.Fatalf("moved sidecar should verify: %v", err)
	}
}
