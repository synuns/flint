// ADR-0006 백업 무결성 — 덤프 원본 + .sha256 사이드카, restore 전 검증.
// 백업 파일은 임의 SQL을 실행하므로, 재생 전 체크섬 위조 여부를 대조한다.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNoSidecar — .sha256 사이드카 없음. 원본 무결 확인 불가: 경고 후 진행.
	ErrNoSidecar = errors.New("checksum sidecar missing")
	// ErrChecksumMismatch — 원본 해시와 사이드카 불일치. restore 거부 대상.
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

// Checksum 은 덤프가 기록된 w(*bytes.Buffer처럼 io.Reader를 함께 구현하는
// writer)의 내용에서 기록량과 sha256 hex 다이제스트를 반환한다.
// 버퍼를 소모하므로 원본 바이트는 호출 전에 보존해 둔다.
func Checksum(w io.Writer) (written int64, sum string, err error) {
	r, ok := w.(io.Reader)
	if !ok {
		return 0, "", fmt.Errorf("checksum: w must be readable (e.g. *bytes.Buffer)")
	}
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// WriteSidecar 은 path+".sha256"에 sha256sum -c 호환 형식
// ("<hex>  <basename>\n")으로 사이드카를 기록한다.
func WriteSidecar(path, sum string) error {
	line := sum + "  " + filepath.Base(path) + "\n"
	return os.WriteFile(path+".sha256", []byte(line), 0o644)
}

// VerifyFile 은 path 원본의 sha256를 계산해 사이드카(path+".sha256")의
// 첫 토큰과 대조한다. 성공 시 계산된 해시 반환. 사이드카 누락은
// ErrNoSidecar(계산된 해시와 함께 반환 — 경고 후 진행용), 불일치는
// ErrChecksumMismatch. 원본 파일은 수정하지 않는다.
func VerifyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	got := hex.EncodeToString(digest[:])

	side, err := os.ReadFile(path + ".sha256")
	if err != nil {
		if os.IsNotExist(err) {
			return got, ErrNoSidecar
		}
		return got, err
	}
	want := strings.Fields(string(side))
	if len(want) == 0 || want[0] != got {
		return got, ErrChecksumMismatch
	}
	return got, nil
}
