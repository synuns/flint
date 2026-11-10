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

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/store"
)

// dumpFn·restoreFn — seam. 기본 T12 실구현, 테스트는 fake 교체(SUPERLITE_REAL=1은 위임).
var (
	dumpFn       = backup.Dump
	restoreFn    = backup.Restore
	drillDataFn  = backup.RunDrillData
	rotateFn     = backup.Rotate
	keepFn       = backup.BackupKeep
	ensureKeyFn  = backup.EnsureIdentity
	encryptToFn  = backup.EncryptTo
	maskSecretsF = store.MaskSecrets
)

// BackupKeyPath — 암호화 백업 identity 경로(FLINT_HOME 인프라 루트,
// v0.28.3 — config에서 데이터 공간으로 이전·레거시 자동 마이그레이션).
func BackupKeyPath() string {
	return dataFile("backup.key")
}

// dumpStore — 전체 덤프 접근(approveStore와 동일 관례의 타입 단언 seam).
// v0.34.3 R14: runBackup이 주입 st 경유로 덤프한다(미구현 st만 dumpFn
// seam 폴백). 물리적 풀은 lib/backup.Dump 내부에서 열고 닫는다(defer).
type dumpStore interface {
	Dump(ctx context.Context, w io.Writer) error
}

// realStore 위임 메서드 — 같은 패키지 파일이라 여기서 추가만 한다.
func (s *realStore) Dump(ctx context.Context, w io.Writer) error {
	return backup.Dump(ctx, dsnFromEnv(), w)
}

// backupEncryptOn — meta backup_encrypt 값 판정의 단일 소스(리뷰 8:
// 수동·자동·doctor 3곳이 각자 판정하면 형식 확장 시 정합 분기).
func backupEncryptOn(v string) bool {
	return v == "true" || v == "1"
}

// runBackup — 덤프 생성. Store는 root 디스패치 호환용이며 사용하지 않는다
// (Dump가 자체 풀을 연다). stdout 모드는 버퍼 경유로 깨끗한 출력을 보장한다.
// v0.28.0(H②): --encrypt(age 암호화·--out 필수)·--mask(비밀 패턴 치환
// 진단 덤프 — 사이드카 미부여 비복원 아티팩트).
func runBackup(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	out := ""
	encrypt, mask := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("--out requires a path")))
				return 1
			}
			out = args[i]
		case "--encrypt":
			encrypt = true
		case "--mask":
			mask = true
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}
	if encrypt && out == "" {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("--encrypt requires --out (암호문은 파일로만)")))
		return 1
	}
	// v0.28.2: --out이 git 저장소 내부면 경고(진행 유지 — 사용자 명시
	// 선택이나 개인 데이터 덤프의 repo 반입·커밋·동기화 유출 사고 예방).
	if out != "" && outInsideGitRepo(out) {
		fmt.Fprintf(os.Stderr, "[flint] backup: --out 경로가 git 저장소 내부 — 덤프 커밋·동기화 유출 위험. ~/.flint/backup 권장\n")
	}
	// v0.28.0: meta backup_encrypt=true면 수동 백업도 암호화(일관성 —
	// 플래그 없이 평문·자동만 암호문인 혼재 방지). stdout 파이프는
	// 평문 계약 유지(암호화는 파일 출력만).
	if !encrypt && out != "" && backupEncryptMeta(ctx, st) {
		encrypt = true
	}

	var buf bytes.Buffer
	// v0.34.3 R14: 주입 st 우선(realStore는 Dump 위임). 미구현 st만
	// dumpFn seam 폴백(기존 테스트 fake) — 폴백 시에만 자체 연결이 열린다.
	var dumpErr error
	if ds, ok := st.(dumpStore); ok {
		dumpErr = ds.Dump(ctx, &buf)
	} else {
		dumpErr = dumpFn(ctx, dsnFromEnv(), &buf)
	}
	if dumpErr != nil {
		fmt.Fprintln(stdout, errLine(dumpErr))
		return store.MapExit(dumpErr)
	}
	data := buf.Bytes()

	extra := ""
	if mask {
		// 마스킹 — 진단·공유용: 비밀 패턴 치환 + 비복원 마커 선두 부착.
		data = append([]byte(backup.MaskDumpHeader+"\n"), maskSecretsF(string(data))...)
		extra += `,"masked":true`
	}
	if encrypt {
		key := BackupKeyPath()
		if key == "" {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("config 디렉터리 미확인 — 키 경로 조립 불가")))
			return 1
		}
		id, err := ensureKeyFn(key)
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		var eb bytes.Buffer
		if err := encryptToFn(&eb, data, id.Recipient()); err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		data = eb.Bytes()
		extra += `,"encrypted":true`
	}

	if out == "" {
		// v0.28.0(H③): 무인자 stdout은 전체 DB 평문 노출(터미널 스크롤백·
		// 로그·에이전트 컨텍스트) — 파일 0600 정책과의 자세 상충을 경고로
		// 메운다(사용은 허용 — 파이프 리다이렉션 관례).
		if !encrypt && !mask {
			fmt.Fprintln(os.Stderr, "[flint] backup: 평문 덤프를 stdout에 출력 — 터미널·로그 유출 주의(파일 저장 권장)")
		}
		stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(out, data, 0o600); err != nil { // v0.20.6 S3: 민감 평문 덤프
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	// v0.28.0(H③): WriteFile의 퍼모드는 신규 파일에만 적용 — 기존 파일
	// 재사용 시 완화된 모드가 남아 민감 덤프가 느슨하게 노출된다. 강제.
	if err := os.Chmod(out, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "[flint] backup: 권한 0600 강제 실패:", err)
	}
	// ADR-0006: 원본+.sha256 사이드카 생성(restore 검증용). 실패는 덤프 성공을 해치지 않는다.
	// v0.28.0: 암호화 백업의 사이드카는 암호문 기준(저장 아티팩트 무결성 —
	// 평문 무결성은 age AEAD가 담보). 마스킹 덤프는 비복원 아티팩트라
	// 사이드카를 부여하지 않는다(restore 원천 차단 + 마커 이중 방어).
	sumField := ""
	if !mask {
		if _, sum, cerr := backup.Checksum(bytes.NewBuffer(data)); cerr == nil {
			if serr := backup.WriteSidecar(out, sum); serr == nil {
				sumField = `,"sha256":` + quoteJSON(sum)
			}
		}
	}
	// T54: 백업 성공 경로 마지막에 로테이션 — keep은 meta backup_keep(기본 14).
	// 로테이션 실패도 백업 성공을 해치지 않는다. quoteJSON이 이미 따옴표를
	// 감싸므로 추가 `"` 금지(F-T54-1 회귀 — 겹따옴표로 JSON 파손).
	fmt.Fprintln(stdout, `{"status":"ok","path":`+quoteJSON(out)+sumField+extra+rotateAfterBackup(ctx, st, out)+"}")
	return 0
}

// backupEncryptMeta — meta backup_encrypt 판정(오류·미설정=false 폴백).
// v0.34.3 T4: meta 접근은 getMetaKV 단일 진입(meta_kv.go) 경유.
func backupEncryptMeta(ctx context.Context, st Store) bool {
	if st == nil {
		return false
	}
	v, ok := getMetaKV(ctx, st, "backup_encrypt")
	return ok && backupEncryptOn(v)
}

// rotateAfterBackup — 성공한 --out 경로 뒤 로테이션 요약 필드(`,"removed":N`)를
// 반환한다. 삭제가 없거나 실패하면 빈 문자열(실패는 경고만, exit 불변).
func rotateAfterBackup(ctx context.Context, st Store, out string) string {
	getKV := func(key string) (string, error) {
		if st == nil {
			return "", errors.New("no store")
		}
		// v0.34.3 T2: 조회 본체는 getMetaKVErr 단일 진입 위임 — 키 부재
		// ("", false, nil)를 기존 미설정 오류로 치환한다.
		v, ok, err := getMetaKVErr(ctx, st, key)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("meta %s 미설정", key)
		}
		return v, nil
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
// flint_drill_db 임시 데이터베이스 재생 훈련(T55) — 원본 불변, 결과만 보고.
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
	// v0.28.0(H③): 파일을 1회만 읽고 그 바이트를 검증·재생한다 —
	// 검증 후 재오픈(VerifyFile→os.Open)의 TOCTOU 창을 제거.
	raw, rerr2 := os.ReadFile(file)
	if rerr2 != nil {
		fmt.Fprintln(stdout, errLine(rerr2))
		return 1
	}
	sum, verr := backup.VerifyBytes(raw, file+".sha256")
	if errors.Is(verr, backup.ErrChecksumMismatch) {
		fmt.Fprintln(stdout, mismatchLine(file))
		return 1
	}
	// v0.20.6(패치 루프 6차 S4): 사이드카 부재도 재생 전 차단 — 백업은
	// 임의 SQL 매체라 무결 검증 없는 재생은 ADR-0006 '위조 감지 시 즉시
	// 중단' 취지 위반이었다(종료 후 경고만으로 진행되던 것을 가드로).
	// sidecar 재생성은 `flint backup --out <동일경로>`로 가능.
	if errors.Is(verr, backup.ErrNoSidecar) {
		fmt.Fprintln(stdout, `{"error":"checksum sidecar missing — 무결 검증 없는 재생 차단(백업 재생성: flint backup --out)"}`)
		return 1
	}
	if verr != nil {
		fmt.Fprintln(stdout, errLine(verr))
		return 1
	}
	_ = sum

	// v0.28.0(H②): 판독 단일 경로 — age 복호·마스킹 거부가 검증된 바이트에
	// 적용된다. 키 경로는 항상 전달(평문이면 사용하지 않는다).
	key := BackupKeyPath()
	data, rerr := backup.ReadDumpFrom(raw, key)
	if errors.Is(rerr, backup.ErrMaskedDump) {
		fmt.Fprintln(stdout, `{"error":"masked dump — 마스킹 덤프는 진단·공유 전용(복원 불가)"}`)
		return 1
	}
	if errors.Is(rerr, backup.ErrKeyPathRequired) || errors.Is(rerr, backup.ErrEncryptKeyMissing) || errors.Is(rerr, backup.ErrEncryptKeyCorrupt) {
		fmt.Fprintf(stdout, `{"error":"암호화 백업 복호 불가 — 키 파일 필요/불량(%s). 키 분실·손상 시 해독 불가(RUNBOOK)"}`+"\n", key)
		return 1
	}
	if rerr != nil {
		fmt.Fprintln(stdout, errLine(rerr))
		return 1
	}

	// --drill — 실재생 대신 훈련: flint_drill_db 데이터베이스에 재생해 행수 대조.
	// 원본은 불변이며, 결과는 한 줄 JSON(불일치 시 ok:false + exit 1).
	// v0.28.0(H③): 검증·복호된 바이트를 그대로 재생(drillDataFn).
	if drill {
		res, derr := drillDataFn(dsnFromEnv(), data)
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

	if err := restoreFn(ctx, dsnFromEnv(), bytes.NewReader(data)); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// 결과 한 줄 JSON — v0.20.6 S4 이후 사이드카 누락은 재생 전 차단되므로
	// 여기는 항상 검증 통과 분기다.
	fmt.Fprintln(stdout, `{"status":"restored","sha256":`+quoteJSON(sum)+"}")
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

// outInsideGitRepo — 경로가 git 저장소(작업 트리) 내부인지 판정(v0.28.2).
// .git 디렉터리(일반)·파일(워크트리) 모두 인식, 상위로 최대 64단계.
func outInsideGitRepo(out string) bool {
	dir, err := filepath.Abs(filepath.Dir(out))
	if err != nil {
		return false
	}
	for i := 0; i < 64; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	return false
}
