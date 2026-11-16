// export.go — v1.3.0 T7: flint export CLI 배선. 원천 조회(ExportRows)와
// 4형식 포맷터(jsonl·md·txt·csv)는 knowledge 원천(T2~T6)을 seam var로
// 소비한다(테스트 fake 주입·과도기 미연결 방어 겸용). 출력 계약: 본문은
// stdout(기본) 또는 --out 파일, 요약·덮어씀 경고는 stderr 1행씩.
package cli

import (
	"context"
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
// 연결(T2~T6 적립). seam은 테스트 fake 주입용 var로 유지(반환형 통일
// 래퍼 — FormatMD·FormatTXT는 단일 반환). SUPERLITE_REAL=1은 이 기본
// 실구현 경로가 곧 실연결 검증이다.
var (
	exportRowsFn = knowledge.ExportRows
	// v1.5.5 B1: 포맷터 seam은 (본문, 요약 보고 행수, 오류)를 반환한다.
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
	// v1.4.0 T2 후속: PAM v1.0 교환 문서 — exported_by는 "flint/"+버전
	// (CurrentVersion 관례: ldflags→buildinfo→dev 폴백). v1.5.5 B1: 발행
	// 수는 직렬화한 문서의 integrity.total_memories에서 읽는다(산출 규칙
	// 재구현이 아니라 문서가 단일 원천).
	formatPAMFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		data, err := knowledge.FormatPAM(rows, "flint/"+CurrentVersion())
		if err != nil {
			return nil, 0, err
		}
		var doc struct {
			Integrity struct {
				TotalMemories int `json:"total_memories"`
			} `json:"integrity"`
		}
		if jerr := json.Unmarshal(data, &doc); jerr != nil {
			return nil, 0, fmt.Errorf("pam issued-count read failed: %w", jerr)
		}
		return data, doc.Integrity.TotalMemories, nil
	}
)

// exportStderr — 요약·경고 챈널 seam(stderr 계약 — stdout 본문과 분리).
var exportStderr io.Writer = os.Stderr

// exportTUIFn — `--tui` 진입 분기 위임 seam(v1.5.0 T8). 기본값은
// tui.RunExport(폼→미리보기→인자 조립→runExport 재실행) — 테스트 fake
// 주입으로 진입 분기만 검증한다(폼 흐름 자체는 lib/tui/export_test.go).
var exportTUIFn = tui.RunExport

// exportTTYFn — v1.5.2 T1 적응형 진입 TTY 판정 seam(기본 실구현
// tui.IsTerminalStdout, 테스트 fake 교체).
var exportTTYFn = tui.IsTerminalStdout

// exportFormatters — 포맷명 → 포맷터 seam 조회(무효 포맷 판별 겸용).
// 반환 행수는 요약 보고 기준(v1.5.5 B1 — pam은 발행 수).
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
// [--status active|all] [--tag X] [--since RFC3339]`. 기본 jsonl·stdout.
// 무효 format·status·since·unknown flag는 exit 3, 스토어 nil·서버다운은
// exit 2(MapExit 재사용). 성공 시 요약 1행 stderr:
// `exported N rows (format, M bytes)`.
func runExport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// v1.5.0 T8 진입 분기 + v1.5.2 T1 적응형 전환: --tui 명시 또는
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
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "format", Expect: "jsonl|md|txt|csv|pam"}))
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
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint export [--format jsonl|md|txt|csv|pam] [--out FILE] [--status active|all] [--tag X] [--since RFC3339]", a)))
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
	// v1.4.1 B2: 조회 결과 행 수 상한(등록표 키 export_import_max_rows —
	// import와 동일 키, 대량 export의 메모리·출력 방어). 조회 직후·포맷팅
	// 전 판정이므로 전량 버퍼링 이전은 아니지만 포맷터·기록 부작용은 차단
	// 한다. v1.5 스트리밍 export로 이관 시 이 판정은 커서 기준으로 재정의.
	if code := enforceExportMaxRows(ctx, st, stdout, len(rows)); code != 0 {
		return code
	}
	fn := exportFormatters()[format]
	if fn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: formatter %s not linked", format)))
		return 1
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
		// v1.4.0 T2: 기록 성공 시 .sha256 사이드카(backup --out 관례
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
	// v1.5.5 B1: 요약 행수는 포맷터 보고값 — pam은 문서 발행 수(pending
	// 제외), 나머지 형식은 전행.
	fmt.Fprintf(exportStderr, "exported %d rows (%s, %d bytes)\n", summaryRows, format, len(data))
	return 0
}

// runExportTUI — `flint export --tui` 배선(v1.5.0 T8). 폼·확인은
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
