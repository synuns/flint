// version.go — flint version: 빌드 메타데이터 한 줄 JSON(ldflags 주입, 기본 dev).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// version·commit — Makefile build의 -ldflags -X 주입 지점(미주입 시 dev 폴백).
var version = "dev"
var commit = "dev"

// CurrentVersion — 유효 버전 문자열 반환. v0.22.0: ldflags 미주입
// 빌드(`go install github.com/synuns/flint/cmd/flint@vX.Y.Z`)는
// debug.ReadBuildInfo의 모듈 버전으로 폴백한다 — go install 배포 경로의
// 버전 자가 보고. 해석 불가면 "dev".
func CurrentVersion() string {
	if validVersion(version) {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimPrefix(bi.Main.Version, "v"); validVersion(v) {
			return v
		}
	}
	return version
}

// VersionString — {"version":...,"commit":...} 한 줄 JSON.
func VersionString() string {
	b, err := json.Marshal(map[string]string{"version": CurrentVersion(), "commit": commit})
	if err != nil { // map[string]string은 사실상 실패 불가 — 예비 경로
		return `{"version":"dev","commit":"dev"}`
	}
	return string(b)
}

// runVersion — 버전 출력. store 불필요(순수 로컬 연산).
func runVersion(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	fmt.Fprintln(stdout, VersionString())
	return 0
}

func init() { Register("version", runVersion) }
