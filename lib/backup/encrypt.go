// encrypt.go — v0.28.0 R1: 백업 at-rest 암호화(age, ADR 계열 — PLAN F).
// X25519 identity 파일(~/.config/flint/backup.key, 0600) 기반 — 자동
// 백업이 비대화형으로 돌아야 하므로 passphrase 프롬프트가 아닌 키 파일
// 방식(룰링 ②). 위협 모델: 백업 파일이 기기를 벗어나는 유출(동기화·
// 복사·업로드 사고) 방지 — 키 파일까지 함께 탈취되는 기기 전체 침해는
// 방어 대상이 아니다(RUNBOOK).
package backup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// ageHeader — age 바이너리 형식 고정 접두(형식 감지).
const ageHeader = "age-encryption.org/"

// EncryptKeyMissing — identity 파일 부재(첫 사용 등).
var EncryptKeyMissing = errors.New("backup encrypt: identity 키 파일 부재 — RUNBOOK '백업 암호화' 참조")

// EncryptKeyCorrupt — identity 파일 존재하나 해석 불가(덮어쓰기 금지 —
// 기존 키 암호문의 영구 복호 불가 확정 방지).
var EncryptKeyCorrupt = errors.New("backup encrypt: identity 키 파일 불량 — 새 키 생성은 기존 암호문을 해독 불능으로 만든다(수동 확인: RUNBOOK)")

// LoadIdentity — path에서 age X25519 identity 로드(부재 → EncryptKeyMissing,
// 불량 → EncryptKeyCorrupt — v0.28.0: 구분해 EnsureIdentity가 불량 키를
// 조용히 덮어쓰지 않게 한다).
func LoadIdentity(path string) (*age.X25519Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, EncryptKeyMissing
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, EncryptKeyCorrupt
	}
	return id, nil
}

// EnsureIdentity — identity 로드, 부재 시에만 생성·저장. 불량 기존 키는
// 에러(기존 키로 암호화된 백업의 영구 복호 불가가 조용히 확정되는 것을
// 방지 — 감사 발견). 저장은 임시 파일+rename 원자화(잘린 키 잔존 방지).
func EnsureIdentity(path string) (*age.X25519Identity, error) {
	if id, err := LoadIdentity(path); err == nil {
		return id, nil
	} else if !errors.Is(err, EncryptKeyMissing) {
		return nil, err // 불량 키 — 덮어쓰지 않는다
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("backup encrypt: 키 생성: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, fmt.Errorf("backup encrypt: 키 디렉터리: %w", err)
	}
	// 원자 쓰기 — 같은 디렉터리 임시 파일(O_EXCL)+rename(0600).
	tmp, err := os.CreateTemp(dirOf(path), ".backup.key-*")
	if err != nil {
		return nil, fmt.Errorf("backup encrypt: 임시 파일: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(id.String() + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, fmt.Errorf("backup encrypt: 키 기록: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("backup encrypt: 키 기록: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("backup encrypt: 키 권한: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("backup encrypt: 키 확정: %w", err)
	}
	return id, nil
}

// dirOf — 경로의 디렉터리(빈 값 → ".").
func dirOf(path string) string {
	return filepath.Dir(path)
}

// EncryptTo — plaintext를 recipient용 age 암호문으로 dst에 기록.
func EncryptTo(dst io.Writer, plaintext []byte, recipient *age.X25519Recipient) error {
	w, err := age.Encrypt(dst, recipient)
	if err != nil {
		return fmt.Errorf("backup encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("backup encrypt: 기록: %w", err)
	}
	return w.Close()
}

// DecryptFrom — age 암호문을 identity로 복호해 평문 반환.
func DecryptFrom(src io.Reader, identity *age.X25519Identity) ([]byte, error) {
	r, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, fmt.Errorf("backup decrypt: %w", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("backup decrypt: %w", err)
	}
	return b, nil
}

// IsEncrypted — 시작 바이트로 age 형식 감지(peek 후 되돌림 — r 재사용 가능).
func IsEncrypted(r io.Reader) (bool, error) {
	head := make([]byte, len(ageHeader))
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	return n == len(ageHeader) && bytes.Equal(head, []byte(ageHeader)), nil
}

// IsEncryptedFile — 파일 경로의 age 형식 감지(부재 파일은 false).
func IsEncryptedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	ok, err := IsEncrypted(f)
	return err == nil && ok
}
