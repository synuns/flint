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
	"log"
	"os"
	"path/filepath"
	"strconv"

	"flint/lib/backup"
	"flint/lib/store"
)

// dumpFn·restoreFn — seam. 기본 T12 실구현, 테스트는 fake 교체(SUPERLITE_REAL=1은 위임).
var (
	dumpFn    = backup.Dump
	restoreFn = backup.Restore
	drillFn   = backup.RunDrill
	rotateFn  = backup.Rotate
	keepFn    = backup.BackupKeep
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
	sumField := ""
	if _, sum, cerr := backup.Checksum(&buf); cerr == nil {
		if serr := backup.WriteSidecar(out, sum); serr == nil {
			sumField = `,"sha256":` + quoteJSON(sum)
		}
	}
	// T54: 백업 성공 경로 마지막에 로테이션 — keep은 meta backup_keep(기본 14).
	// 로테이션 실패도 백업 성공을 해치지 않는다. quoteJSON이 이미 따옴표를
	// 감싸므로 추가 `"` 금지(F-T54-1 회귀 — 겹따옴표로 JSON 파손).
	fmt.Fprintln(stdout, `{"status":"ok","path":`+quoteJSON(out)+sumField+rotateAfterBackup(ctx, st, out)+"}")
	return 0
}

// rotateAfterBackup — 성공한 --out 경로 뒤 로테이션 요약 필드(`,"removed":N`)를
// 반환한다. 삭제가 없거나 실패하면 빈 문자열(실패는 경고만, exit 불변).
func rotateAfterBackup(ctx context.Context, st Store, out string) string {
	getKV := func(key string) (string, error) {
		if st == nil {
			return "", errors.New("no store")
		}
		rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
		if err != nil {
			return "", err
		}
		if len(rows) == 0 || len(rows[0]) == 0 {
			return "", fmt.Errorf("meta %s 미설정", key)
		}
		s, ok := rows[0][0].(string)
		if !ok {
			return "", fmt.Errorf("meta %s 값이 문자열 아님", key)
		}
		return s, nil
	}
	removed, err := rotateFn(filepath.Dir(out), keepFn(getKV))
	if err != nil {
		log.Printf("[flint] backup: rotate 실패: %v", err)
		return ""
	}
	if len(removed) == 0 {
		return ""
	}
	return `,"removed":` + strconv.Itoa(len(removed))
}

// runRestore — 덤프 스크립트 재생. --file 필수. --drill은 실재생 대신
// flint_drill 임시 스키마 재생 훈련(T55) — 원본 불변, 결과만 보고.
func runRestore(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	file := ""
	drill := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--drill":
			drill = true
		case "--file":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("--file requires a path")))
				return 1
			}
			file = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
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

	// --drill — 실재생 대신 훈련: flint_drill 스키마에 재생해 행수 대조.
	// 원본은 불변이며, 결과는 한 줄 JSON(불일치 시 ok:false + exit 1).
	if drill {
		res, derr := drillFn(dsnFromEnv(), file)
		if derr != nil {
			fmt.Fprintln(stdout, errLine(derr))
			return store.MapExit(derr)
		}
		ok := "false"
		if res.Ok {
			ok = "true"
		}
		fmt.Fprintf(stdout, `{"status":"drill","expected":%d,"restored":%d,"ok":%s}`+"\n",
			res.Expected, res.Restored, ok)
		if !res.Ok {
			return 1
		}
		return 0
	}

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
