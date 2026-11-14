// export.go — v1.3.0 T7: flint export CLI 배선. 원천 조회(ExportRows)와
// 4형식 포맷터(jsonl·md·txt·csv)는 knowledge 원천(T2~T6)을 seam var로
// 소비한다(테스트 fake 주입·과도기 미연결 방어 겸용). 출력 계약: 본문은
// stdout(기본) 또는 --out 파일, 요약·덮어씀 경고는 stderr 1행씩.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// TEMP 완료 — ExportRow·ExportFilter·원천·포맷터 전부 knowledge 원천
// 연결(T2~T6 적립). seam은 테스트 fake 주입용 var로 유지(반환형 통일
// 래퍼 — FormatMD·FormatTXT는 단일 반환). SUPERLITE_REAL=1은 이 기본
// 실구현 경로가 곧 실연결 검증이다.
var (
	exportRowsFn  = knowledge.ExportRows
	formatJSONLFn = func(rows []knowledge.ExportRow) ([]byte, error) { return knowledge.FormatJSONL(rows) }
	formatMDFn    = func(rows []knowledge.ExportRow) ([]byte, error) { return knowledge.FormatMD(rows), nil }
	formatTXTFn   = func(rows []knowledge.ExportRow) ([]byte, error) { return knowledge.FormatTXT(rows), nil }
	formatCSVFn   = func(rows []knowledge.ExportRow) ([]byte, error) { return knowledge.FormatCSV(rows) }
)

// exportStderr — 요약·경고 챈널 seam(stderr 계약 — stdout 본문과 분리).
var exportStderr io.Writer = os.Stderr

// exportFormatters — 포맷명 → 포맷터 seam 조회(무효 포맷 판별 겸용).
func exportFormatters() map[string]func([]knowledge.ExportRow) ([]byte, error) {
	return map[string]func([]knowledge.ExportRow) ([]byte, error){
		"jsonl": formatJSONLFn,
		"md":    formatMDFn,
		"txt":   formatTXTFn,
		"csv":   formatCSVFn,
	}
}

func init() { Register("export", runExport) }

// runExport — `flint export [--format jsonl|md|txt|csv] [--out FILE]
// [--status active|all] [--tag X] [--since RFC3339]`. 기본 jsonl·stdout.
// 무효 format·status·since·unknown flag는 exit 3, 스토어 nil·서버다운은
// exit 2(MapExit 재사용). 성공 시 요약 1행 stderr:
// `exported N rows (format, M bytes)`.
func runExport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	format, outPath, status, tag := "jsonl", "", "active", ""
	var since time.Time
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		if i+1 < len(args) {
			v = args[i+1]
		}
		switch a {
		case "--format":
			if v == "" || !validExportFormat(v) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "format", Expect: "jsonl|md|txt|csv"}))
				return 3
			}
			format, i = v, i+1
		case "--out":
			if v == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "non-empty"}))
				return 3
			}
			outPath, i = v, i+1
		case "--status":
			if v != "active" && v != "all" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "status", Expect: "active|all"}))
				return 3
			}
			status, i = v, i+1
		case "--tag":
			// read 관례 승계: 반복 지정 시 마지막 값 우선.
			if v == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tag", Expect: "non-empty"}))
				return 3
			}
			tag, i = v, i+1
		case "--since":
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "since", Expect: "RFC3339"}))
				return 3
			}
			since, i = t, i+1
		default:
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("unknown flag: %s — 사용법: flint export [--format jsonl|md|txt|csv] [--out FILE] [--status active|all] [--tag X] [--since RFC3339]", a)))
			return 3
		}
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
	fn := exportFormatters()[format]
	if fn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: formatter %s not linked", format)))
		return 1
	}
	data, err := fn(rows)
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
	} else if _, werr := stdout.Write(data); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}
	fmt.Fprintf(exportStderr, "exported %d rows (%s, %d bytes)\n", len(rows), format, len(data))
	return 0
}

// validExportFormat — 플래그 검증 시점용(포맷터 미연결 판별은 조회 시).
func validExportFormat(v string) bool {
	switch v {
	case "jsonl", "md", "txt", "csv":
		return true
	}
	return false
}
