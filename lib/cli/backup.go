// backup 커맨드 — ADR-0002 자체 SQL 덤프의 CLI 진입점.
// `flint backup [--out file]`(미지정 시 stdout) · `flint restore --file path`.
// 실패는 한 줄 JSON 에러 + exit 코드 계약(MapExit: 1 재시도/일반, 2 서버 다운).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"flint/lib/backup"
	"flint/lib/store"
)

// dumpFn·restoreFn — seam. 기본 T12 실구현, 테스트는 fake 교체(SUPERLITE_REAL=1은 위임).
var (
	dumpFn    = backup.Dump
	restoreFn = backup.Restore
)

// runBackup — 덤프 생성. Store는 root 디스패치 호환용이며 사용하지 않는다
// (Dump가 자체 풀을 연다). stdout 모드는 버퍼 경유로 깨끗한 출력을 보장한다.
func runBackup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	out := ""
	for i := 0; i < len(args); i++ {
		if args[i] != "--out" {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
		i++
		if i >= len(args) {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("--out requires a path")))
			return 1
		}
		out = args[i]
	}

	var buf bytes.Buffer
	if err := dumpFn(ctx, dsnFromEnv(), &buf); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}

	if out == "" {
		buf.WriteTo(stdout)
		return 0
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	// ADR-0006: 원본+.sha256 사이드카 생성(restore 검증용). 실패는 덤프 성공을 해치지 않는다.
	if _, sum, cerr := backup.Checksum(&buf); cerr == nil {
		if serr := backup.WriteSidecar(out, sum); serr == nil {
			fmt.Fprintln(stdout, `{"status":"ok","path":`+quoteJSON(out)+`,"sha256":`+quoteJSON(sum)+"}")
			return 0
		}
	}
	fmt.Fprintln(stdout, `{"status":"ok","path":`+quoteJSON(out)+"}")
	return 0
}

// runRestore — 덤프 스크립트 재생. --file 필수.
func runRestore(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	file := ""
	for i := 0; i < len(args); i++ {
		if args[i] != "--file" {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
		i++
		if i >= len(args) {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("--file requires a path")))
			return 1
		}
		file = args[i]
	}
	if file == "" {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("restore requires --file")))
		return 1
	}

	// ADR-0006 restore 가드 — 재생 전 체크섬 검증(가드는 이 1곳뿐).
	// 백업 파일은 임의 SQL을 실행하므로 위조 감지 시 즉시 중단한다.
	sum, verr := backup.VerifyFile(file)
	if errors.Is(verr, backup.ErrChecksumMismatch) {
		fmt.Fprintln(stdout, mismatchLine(file))
		return 1
	}
	if verr != nil && !errors.Is(verr, backup.ErrNoSidecar) {
		fmt.Fprintln(stdout, errLine(verr))
		return 1
	}

	f, err := os.Open(file)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	defer f.Close()

	if err := restoreFn(ctx, dsnFromEnv(), f); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// 결과 한 줄 JSON — 검증 통과 시 sha256, 사이드카 누락 시 경고(진행은 함).
	if errors.Is(verr, backup.ErrNoSidecar) {
		fmt.Fprintln(stdout, `{"status":"restored","warn":`+quoteJSON("checksum sidecar missing — integrity unverified")+"}")
	} else {
		fmt.Fprintln(stdout, `{"status":"restored","sha256":`+quoteJSON(sum)+"}")
	}
	return 0
}

// mismatchLine — 체크섬 불일치 경고 한 줄 JSON(임의 SQL 실행 차단 안내 포함).
func mismatchLine(path string) string {
	m := map[string]string{
		"error":   "checksum_mismatch",
		"message": "백업 파일은 임의 SQL을 실행함 — 체크섬 위조 감지",
		"path":    path,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// quoteJSON — 경로 등 문자열을 JSON 문자열 리터럴로 감싼다.
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
