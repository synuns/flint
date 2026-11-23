package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureIdentityCreatesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "backup.key")
	id1, err := EnsureIdentity(path)
	if err != nil {
		t.Fatalf("생성: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("키 파일: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("권한 = %o, want 0600", fi.Mode().Perm())
	}
	id2, err := EnsureIdentity(path)
	if err != nil {
		t.Fatalf("재로드: %v", err)
	}
	if id1.String() != id2.String() {
		t.Error("재로드 identity 불일치 — 멱등 위반")
	}
}

func TestEncryptRoundtrip(t *testing.T) {
	id, err := EnsureIdentity(filepath.Join(t.TempDir(), "backup.key"))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("-- flint dump\nINSERT INTO knowledge VALUES ('한글 본문');")
	var ct bytes.Buffer
	if err := EncryptTo(&ct, plain, id.Recipient()); err != nil {
		t.Fatalf("암호화: %v", err)
	}
	if !bytes.HasPrefix(ct.Bytes(), []byte(ageHeader)) {
		t.Error("암호문에 age 헤더 없음 — 형식 감지 불가")
	}
	if bytes.Contains(ct.Bytes(), plain) {
		t.Error("평문이 암호문에 노출")
	}
	got, err := DecryptFrom(bytes.NewReader(ct.Bytes()), id)
	if err != nil {
		t.Fatalf("복호: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("roundtrip 불일치: %q", got)
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	id, _ := EnsureIdentity(filepath.Join(t.TempDir(), "backup.key"))
	var ct bytes.Buffer
	EncryptTo(&ct, []byte("덤프 본문 — 최소 길이"), id.Recipient())
	b := ct.Bytes()
	b[len(b)-1] ^= 0xFF
	if _, err := DecryptFrom(bytes.NewReader(b), id); err == nil {
		t.Error("변조 암호문 복호 성공 — AEAD 무결성 위반")
	}
}

func TestReadDumpBytesPlainEncryptedMasked(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "backup.key")
	id, err := EnsureIdentity(key)
	if err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(dir, "p.sql")
	os.WriteFile(p, []byte("-- dump\nINSERT 1;"), 0o600)
	got, err := ReadDumpBytes(p, "")
	if err != nil || string(got) != "-- dump\nINSERT 1;" {
		t.Fatalf("평문 판독: %v %q", err, got)
	}

	e := filepath.Join(dir, "e.sql.age")
	var ct bytes.Buffer
	if err := EncryptTo(&ct, []byte("-- dump\nINSERT 2;"), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e, ct.Bytes(), 0o600)
	got, err = ReadDumpBytes(e, key)
	if err != nil || !strings.Contains(string(got), "INSERT 2;") {
		t.Fatalf("암호문 판독: %v %q", err, got)
	}

	if _, err := ReadDumpBytes(e, ""); err != ErrKeyPathRequired {
		t.Errorf("키 없음 = %v, want ErrKeyPathRequired", err)
	}

	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("AGE-SECRET-KEY-NOTVALID"), 0o600)
	if _, err := ReadDumpBytes(e, bad); err != ErrEncryptKeyCorrupt {
		t.Errorf("불량 키 = %v, want ErrEncryptKeyCorrupt", err)
	}

	m := filepath.Join(dir, "m.sql")
	os.WriteFile(m, []byte(MaskDumpHeader+"\n-- masked\nINSERT 3;"), 0o600)
	if _, err := ReadDumpBytes(m, ""); err != ErrMaskedDump {
		t.Errorf("마스킹 = %v, want ErrMaskedDump", err)
	}
}

func TestIsEncryptedFile(t *testing.T) {
	dir := t.TempDir()
	id, _ := EnsureIdentity(filepath.Join(dir, "backup.key"))
	var ct bytes.Buffer
	EncryptTo(&ct, []byte("x"), id.Recipient())
	e := filepath.Join(dir, "e.age")
	os.WriteFile(e, ct.Bytes(), 0o600)
	if !IsEncryptedFile(e) {
		t.Error("age 파일 미감지")
	}
	p := filepath.Join(dir, "p.sql")
	os.WriteFile(p, []byte("-- plain"), 0o600)
	if IsEncryptedFile(p) {
		t.Error("평문 파일 오탐")
	}
	if IsEncryptedFile(filepath.Join(dir, "없음")) {
		t.Error("부재 파일 오탐")
	}
}

func TestEnsureIdentityRefusesCorruptKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.key")
	os.WriteFile(path, []byte("AGE-SECRET-KEY-NOTVALID"), 0o600)
	if _, err := EnsureIdentity(path); err != ErrEncryptKeyCorrupt {
		t.Fatalf("불량 키 EnsureIdentity = %v, want ErrEncryptKeyCorrupt", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "AGE-SECRET-KEY-NOTVALID" {
		t.Errorf("불량 키가 덮어쓰임: %q", b)
	}
}
