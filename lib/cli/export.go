// export.go — flint export CLI 배선. 원천 조회(ExportRows)와
// 4형식 포맷터(jsonl·md·txt·csv)는 knowledge 원천을 seam var로
// 소비한다(테스트 fake 주입·과도기 미연결 방어 겸용). 출력 계약: 본문은
// stdout(기본) 또는 --out 파일, 요약·덮어씀 경고는 stderr 1행씩.
package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// TEMP 완료 — ExportRow·ExportFilter·원천·포맷터 전부 knowledge 원천
// 연결. seam은 테스트 fake 주입용 var로 유지(반환형 통일
// 래퍼 — FormatMD·FormatTXT는 단일 반환). SUPERLITE_REAL=1은 이 기본
// 실구현 경로가 곧 실연결 검증이다.
var (
	exportRowsFn = knowledge.ExportRows
	// 포맷터 seam은 (본문, 요약 보고 행수, 오류)를 반환한다.
	// jsonl·md·txt·csv는 전행 수(len(rows)), pam은 문서 발행 수
	// (total_memories — pending은 pam 설계 제외)를 보고한다.
	formatJSONLFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		b, err := knowledge.FormatJSONL(rows)
		return b, len(rows), err
	}
	formatMDFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		return knowledge.FormatMD(rows), len(rows), nil
	}
	formatTXTFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		return knowledge.FormatTXT(rows), len(rows), nil
	}
	formatCSVFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		b, err := knowledge.FormatCSV(rows)
		return b, len(rows), err
	}
	// PAM v1.0 교환 문서 — exported_by는 "flint/"+버전
	// (CurrentVersion 관례: ldflags→buildinfo→dev 폴백).
	// 발행 수(total_memories)를 포맷터에서 직접 받는다.
	formatPAMFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		return knowledge.FormatPAMWithCount(rows, "flint/"+CurrentVersion())
	}
	// §18: `--format pam --sign` 서명 경로 — 키는
	// knowledge.LoadOrCreatePAMSigningKey(configDir()/pam-signing-key).
	// 발행 수 반환은 formatPAMFn과 동일 경유.
	formatPAMSignedFn = func(rows []knowledge.ExportRow, key ed25519.PrivateKey) ([]byte, int, error) {
		return knowledge.FormatPAMSignedWithCount(rows, "flint/"+CurrentVersion(), key)
	}
)

// exportSignKeyFn — §18 서명 키 로드 seam(테스트 fake 주입).
var exportSignKeyFn = knowledge.LoadOrCreatePAMSigningKey

// exportStderr — 요약·경고 챈널 seam(stderr 계약 — stdout 본문과 분리).
var exportStderr io.Writer = os.Stderr

// exportTUIFn — `--tui` 진입 분기 위임 seam. 기본값은
// tui.RunExport(폼→미리보기→인자 조립→runExport 재실행) — 테스트 fake
// 주입으로 진입 분기만 검증한다(폼 흐름 자체는 lib/tui/export_test.go).
var exportTUIFn = tui.RunExport

// exportTTYFn — 적응형 진입 TTY 판정 seam(기본 실구현
// tui.IsTerminalStdout, 테스트 fake 교체).
var exportTTYFn = tui.IsTerminalStdout

// exportFormatters — 포맷명 → 포맷터 seam 조회(무효 포맷 판별 겸용).
// 반환 행수는 요약 보고 기준(pam은 발행 수).
func exportFormatters() map[string]func([]knowledge.ExportRow) ([]byte, int, error) {
	return map[string]func([]knowledge.ExportRow) ([]byte, int, error){
		"jsonl": formatJSONLFn,
		"md":    formatMDFn,
		"txt":   formatTXTFn,
		"csv":   formatCSVFn,
		"pam":   formatPAMFn,
	}
}

func init() { Register("export", runExport) }

// runExport — `flint export [--format jsonl|md|txt|csv|pam] [--out FILE]
// [--status active|all] [--tag X] [--since RFC3339] [--sign (pam 전용 —
// §18 서명)]`. 기본 jsonl·stdout.
// 무효 format·status·since·unknown flag는 exit 3, 스토어 nil·서버다운은
// exit 2(MapExit 재사용). 성공 시 요약 1행 stderr:
// `exported N rows (format, M bytes)`.
func runExport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// 진입 분기 + 적응형 전환: --tui 명시 또는
	// 무인자·무플래그 + 터미널이면 폼 흐름으로 위임하고 플래그 파싱은
	// 하지 않는다(폼에서 조립된 인자가 runExport를 재진입한다 — 재진입
	// 시 인자가 있으므로 적응형 재발산 없음). 플래그 경로는 불변.
	tuiEntry := false
	for _, a := range args {
		if a == "--tui" {
			tuiEntry = true
			break
		}
	}
	// 3층 방어 1층 — FLINT_NO_TUI 설정 시 TTY 여부와 무관하게 차단.
	if !tuiEntry && len(args) == 0 && !tui.NoTUIForced() && exportTTYFn() {
		tuiEntry = true
	}
	if tuiEntry {
		return runExportTUI(ctx, st, stdin, stdout)
	}
	// 수동 루프를 wire.ParseArgs 사양 테이블로 이관(위치 인자
	// 불허 — read 계열 소비 관례 승계: 값 무조건 소비·trailing 값 부재는
	// 호출부 빈 값 검증이 exit 3). 값 검증은 발생 순(AllOccs) 재현.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--format", Value: true},
			{Name: "--out", Value: true},
			{Name: "--status", Value: true},
			{Name: "--tag", Value: true},
			{Name: "--since", Value: true},
			// §18 — pam 전용 서명 플래그(값 소비 없음).
			{Name: "--sign"},
		},
		Usage: "usage: flint export [--format jsonl|md|txt|csv|pam] [--out FILE] [--status active|all] [--tag X] [--since RFC3339] [--sign (pam only)]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	format, outPath, status, tag := "jsonl", "", "active", ""
	sign := res.Given("--sign")
	var since time.Time
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--format":
			if o.Value == "" || !validExportFormat(o.Value) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "format", Expect: "jsonl|md|txt|csv|pam"}))
				return 3
			}
			format = o.Value
		case "--out":
			if o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "non-empty"}))
				return 3
			}
			// POSIX 관례: 경로 "-"는 stdout(미지정과 동일).
			if o.Value == "-" {
				outPath = ""
			} else {
				outPath = o.Value
			}
		case "--status":
			if o.Value != "active" && o.Value != "all" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "status", Expect: "active|all"}))
				return 3
			}
			status = o.Value
		case "--tag":
			// read 관례 승계: 반복 지정 시 마지막 값 우선.
			if o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tag", Expect: "non-empty"}))
				return 3
			}
			tag = o.Value
		case "--since":
			t, err := time.Parse(time.RFC3339, o.Value)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "since", Expect: "RFC3339"}))
				return 3
			}
			since = t
		}
	}
	// --sign는 PAM §18 서명 경로 전용 — 다른 포맷 조합은 사용법 오류.
	if sign && format != "pam" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "sign", Expect: "requires --format pam"}))
		return 3
	}
	// 계약: 스토어 nil·서버다운 exit 2. export는 store-free가 아니므로
	// nil은 서버다운과 동일 취급(ErrLine 한 줄 + MapExit).
	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return store.MapExit(store.ErrServerDown)
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if exportRowsFn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: source not linked")))
		return 1
	}
	rows, err := exportRowsFn(ctx, st, knowledge.ExportFilter{Status: status, Tag: tag, Since: since})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// 조회 결과 행 수 상한(등록표 키 export_import_max_rows —
	// import와 동일 키, 대량 export의 메모리·출력 방어). 조회 직후·포맷팅
	// 전 판정이므로 전량 버퍼링 이전은 아니지만 포맷터·기록 부작용은 차단
	// 한다.
	if code := enforceExportMaxRows(ctx, st, stdout, len(rows)); code != 0 {
		return code
	}
	fn := exportFormatters()[format]
	if fn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: formatter %s not linked", format)))
		return 1
	}
	if sign {
		// §18: 서명 키 로드(없으면 생성 — 0600) 후 서명 포맷터로 교체.
		key, kerr := exportSignKeyFn()
		if kerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: pam signing key unavailable: %v", kerr)))
			return 1
		}
		sfn := formatPAMSignedFn
		fn = func(rows []knowledge.ExportRow) ([]byte, int, error) { return sfn(rows, key) }
	}
	data, summaryRows, err := fn(rows)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	if outPath != "" {
		// 기존 파일 덮어씀 — 경고 1행을 기록보다 먼저 stderr에.
		if _, serr := os.Stat(outPath); serr == nil {
			fmt.Fprintf(exportStderr, "export: overwriting existing file: %s\n", outPath)
		}
		if werr := os.WriteFile(outPath, data, 0o644); werr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(werr))
			return 1
		}
		// 기록 성공 시 .sha256 사이드카(backup --out 관례
		// 승계 — backup.WriteSidecar로 형식 통일, sha256sum -c 호환).
		// 실패는 stderr 경고만: 본문 기록 성공을 해치지 않는다(ADR-0006).
		digest := sha256.Sum256(data)
		if serr := backup.WriteSidecar(outPath, hex.EncodeToString(digest[:])); serr != nil {
			fmt.Fprintf(exportStderr, "export: checksum sidecar write failed: %v\n", serr)
		}
	} else if _, werr := stdout.Write(data); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}
	// 요약 행수는 포맷터 보고값 — pam은 문서 발행 수(pending
	// 제외), 나머지 형식은 전행.
	fmt.Fprintf(exportStderr, "exported %d rows (%s, %d bytes)\n", summaryRows, format, len(data))
	return 0
}

// runExportTUI — `flint export --tui` 배선. 폼·확인은
// tui.RunExport에, 조회·포맷터·상한·실행은 기존 seam(exportRowsFn·
// exportFormatters·exportMaxRowsLimit·runExport)을 래핑해 주입한다 —
// TUI가 원천 로직을 재구현하지 않는다(사이드카·상한·요약은 runExport
// 재실행으로 자동 적용). 오류(비터미널 포함)는 ErrLine 한 줄 + exit 3.
func runExportTUI(ctx context.Context, st Store, stdin io.Reader, stdout io.Writer) int {
	code, err := exportTUIFn(ctx, tui.ExportDeps{
		Preview: func(ctx context.Context, sel tui.ExportSelection) (tui.ExportPreview, error) {
			if exportRowsFn == nil {
				return tui.ExportPreview{}, fmt.Errorf("export: source not linked")
			}
			rows, err := exportRowsFn(ctx, st, knowledge.ExportFilter{Status: sel.Status})
			if err != nil {
				return tui.ExportPreview{}, err
			}
			fn := exportFormatters()[sel.Format]
			if fn == nil {
				return tui.ExportPreview{}, fmt.Errorf("export: formatter %s not linked", sel.Format)
			}
			data, _, err := fn(rows)
			if err != nil {
				return tui.ExportPreview{}, err
			}
			return tui.ExportPreview{Rows: len(rows), Bytes: len(data)}, nil
		},
		MaxRows: func(ctx context.Context) int { return exportMaxRowsLimit(ctx, st) },
		Run:     func(assembled []string) int { return runExport(ctx, st, assembled, stdin, stdout) },
	})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return code
}

// exportMaxRowsLimit — export 1회 행 수 상한 로드(등록표 키
// export_import_max_rows — import와 동일 키 공유). 조회 실패·미설정(0)은
// DefaultThresholds 폴백(importMaxRowsLimit과 동일 규약).
func exportMaxRowsLimit(ctx context.Context, st Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

// enforceExportMaxRows — 행 수 상한 초과 시 오류 JSON(max·count 포함)을
// stdout에 내고 3을 반환(호출부는 그대로 반환). 형식은 import
// enforceImportMaxRows를 준용한다. 포맷터·파일 기록 전 단계라 부작용 없음.
func enforceExportMaxRows(ctx context.Context, st Store, stdout io.Writer, count int) int {
	max := exportMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("export row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"export row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
}

// validExportFormat — 플래그 검증 시점용(포맷터 미연결 판별은 조회 시).
func validExportFormat(v string) bool {
	switch v {
	case "jsonl", "md", "txt", "csv", "pam":
		return true
	}
	return false
}
