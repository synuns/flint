// encrypt.go — v0.28.0 R1: 백업 at-rest 암호화(age, ADR 계열 — PLAN F).
// X25519 identity 파일(설정 디렉터리 backup.key, 0600) 기반 — 자동
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
	"time"

	"filippo.io/age"
)

// ageHeader — age 바이너리 형식 고정 접두(형식 감지).
const ageHeader = "age-encryption.org/"

// ErrEncryptKeyMissing — identity 파일 부재(첫 사용 등).
var ErrEncryptKeyMissing = errors.New("backup encrypt: identity 키 파일 부재 — RUNBOOK '백업 암호화' 참조")

// ErrEncryptKeyCorrupt — identity 파일 존재하나 해석 불가(덮어쓰기 금지 —
// 기존 키 암호문의 영구 복호 불가 확정 방지).
var ErrEncryptKeyCorrupt = errors.New("backup encrypt: identity 키 파일 불량 — 새 키 생성은 기존 암호문을 해독 불능으로 만든다(수동 확인: RUNBOOK)")

// LoadIdentity — path에서 age X25519 identity 로드(부재 → ErrEncryptKeyMissing,
// 불량 → ErrEncryptKeyCorrupt — v0.28.0: 구분해 EnsureIdentity가 불량 키를
// 조용히 덮어쓰지 않게 한다).
func LoadIdentity(path string) (*age.X25519Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrEncryptKeyMissing
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, ErrEncryptKeyCorrupt
	}
	return id, nil
}

// EnsureIdentity — identity 로드, 부재 시에만 생성·저장. 불량 기존 키는
// 에러(기존 키로 암호화된 백업의 영구 복호 불가가 조용히 확정되는 것을
// 방지 — 감사 발견). 생성은 O_EXCL 선행 클레임(리뷰 1: 병렬 첫 암호화가
// 서로 다른 키를 만들어 패자 키 암호문을 영구 잃는 경합 방지 — 클레임
// 승자의 키 파일이 유일 원본이 된다). 첫 생성 중 크래시로 불량 파일이
// 남면 재생성은 거부된다(수동 확인 — RUNBOOK).
func EnsureIdentity(path string) (*age.X25519Identity, error) {
	if id, err := LoadIdentity(path); err == nil {
		return id, nil
	} else if !errors.Is(err, ErrEncryptKeyMissing) {
		return nil, err // 불량 키 — 덮어쓰지 않는다
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("backup encrypt: 키 생성: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, fmt.Errorf("backup encrypt: 키 디렉터리: %w", err)
	}
	// O_EXCL 선행 클레임 — 이미 있으면(병렬 승자) 그 키를 재로드해 쓴다.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return reloadIdentityRetry(path)
		}
		return nil, fmt.Errorf("backup encrypt: 키 생성 클레임: %w", err)
	}
	if _, err := f.WriteString(id.String() + "\n"); err != nil { // 단일 small write
		f.Close()
		os.Remove(path) // 클레임 실패 — 다음 시도 재생성 허용
		return nil, fmt.Errorf("backup encrypt: 키 기록: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("backup encrypt: 키 기록: %w", err)
	}
	return id, nil
}

// reloadIdentityRetry — 병렬 승자의 키 재로드(기록 직후 일독 실패 대비
// 짧게 2회 재시도).
func reloadIdentityRetry(path string) (*age.X25519Identity, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if id, err := LoadIdentity(path); err == nil {
			return id, nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, lastErr
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

// IsEncrypted — 시작 바이트로 age 형식 감지. 선두 17바이트를 소비한다
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
