// dumpfile.go — v0.28.0: 덤프 파일 판독 단일 경로(restore·drill 공용).
// age 형식 감지→복호, 마스킹 덤프 거부(비복원 아티팩트 — 사이드카 미부여
// 원칙과 이중 방어)를 한 곳에서 담당한다.
package backup

import (
	"bytes"
	"errors"
	"os"
	"strings"
)

// ErrMaskedDump — 마스킹 덤프는 진단·공유용 비복원 아티팩트다.
var ErrMaskedDump = errors.New("backup: 마스킹 덤프는 복원 대상이 아님(진단·공유 전용)")

// ErrKeyPathRequired — 암호화 덤프인데 키 경로가 제공되지 않음.
var ErrKeyPathRequired = errors.New("backup: 암호화 덤프 — 복호 키 경로 필요")

// MaskDumpHeader — 마스킹 덤프 파일 선두 마커(restore·drill 거부 근거).
const MaskDumpHeader = "-- flint-masked-dump: never restore (masked diagnostic artifact)"

// ReadDumpBytes — 덤프 파일을 읽어 재생 가능한 평문 바이트를 반환.
// age 형식이면 keyPath의 identity로 복호하고, 마스킹 마커가 있으면
// ErrMaskedDump. keyPath 공백이면 암호화 파일은 ErrKeyPathRequired.
func ReadDumpBytes(path, keyPath string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadDumpFrom(b, keyPath)
}

// ReadDumpFrom — 미리 읽은(검증된) 바이트의 판독(v0.28.0 H③: restore
// TOCTOU 폐쇄 — 파일 재판독 없이 검증한 바이트를 복호·검사한다).
func ReadDumpFrom(b []byte, keyPath string) ([]byte, error) {
	if ok, _ := IsEncrypted(bytes.NewReader(b)); ok {
		if keyPath == "" {
			return nil, ErrKeyPathRequired
		}
		id, err := LoadIdentity(keyPath)
		if err != nil {
			return nil, err
		}
		b, err = DecryptFrom(bytes.NewReader(b), id)
		if err != nil {
			return nil, err
		}
	}
	if isMaskedDump(b) {
		return nil, ErrMaskedDump
	}
	return b, nil
}

// isMaskedDump — 평문 선두 마커 검사(주석 형식 — SQL 파서에 무해).
func isMaskedDump(b []byte) bool {
	head := b
	if len(head) > 512 {
		head = head[:512]
	}
	for _, line := range strings.Split(string(head), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			if strings.HasPrefix(t, MaskDumpHeader) {
				return true
			}
			continue // 선행 주석·빈 줄은 건너뛰며 마커 탐색
		}
		return false // 본문 시작 — 마커 없음
	}
	return false
}
