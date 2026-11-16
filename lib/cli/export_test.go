// export_test.go — v1.3.0 T7: flint export CLI 배선 계약 테스트.
// 포맷터·원천 조회(T2~T6)는 seam var로 fake 주입 — RunExport 자체
// 계약(플래그 파싱·stdout/--out·경고·요약 stderr·exit 0/2/3)이 검증
// 대상이다. 실구현 연결은 seam 기본값 대입 시점(컴파일 시점)에 활성화.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// installExportFakes — export seam 일괄 fake 설치 + stderr 캡처.
// rowsFn이 nil이면 기본 rows fake(2행)를, formatter는 포맷명을 그대로
// 돌려주는 최소 fake를 설치한다. 반환값은 캡처된 stderr 버퍼.
// 포맷터(T3~T6)는 seam 기본값이 실함수라 fake 설치로만 교체 검증되고,
// 원천 조회(T2)는 미적립 — 실구현 경로는 적립 후 활성화된다.
func installExportFakes(t *testing.T, rowsFn func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error)) *bytes.Buffer {
	t.Helper()
	if rowsFn == nil {
		rowsFn = func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
			return []knowledge.ExportRow{{ID: 1, Topic: "demo-a", Status: "active"}, {ID: 2, Topic: "demo-b", Status: "active"}}, nil
		}
	}
	// v1.5.5 B1: 포맷터 seam은 (본문, 요약 보고 행수, 오류) 3값 — fake는
	// 전행 수(len(rows))를 보고한다(pam 실구현만 발행 수 반환).
	mk := func(name string) func([]knowledge.ExportRow) ([]byte, int, error) {
		return func(rows []knowledge.ExportRow) ([]byte, int, error) {
			return []byte(fmt.Sprintf("<%s of %d rows>\n", name, len(rows))), len(rows), nil
		}
	}
	var stderr bytes.Buffer
	oldRows, oldJ, oldM, oldT, oldC, oldP, oldErr :=
		exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, formatPAMFn, exportStderr
	exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, formatPAMFn, exportStderr =
		rowsFn, mk("jsonl"), mk("md"), mk("txt"), mk("csv"), mk("pam"), &stderr
	t.Cleanup(func() {
		exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, formatPAMFn, exportStderr =
			oldRows, oldJ, oldM, oldT, oldC, oldP, oldErr
	})
	return &stderr
}

// RF1 — 기본값: jsonl 포맷·stdout 출력·exit 0·요약 stderr.
func TestRunExportDefaultJSONLToStdout(t *testing.T) {
	stderr := installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if got, want := stdout.String(), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF2 — --out: 기존 파일 덮어씀 경고 1행을 파일 기록보다 먼저 stderr에.
func TestRunExportOutFileOverwriteWarning(t *testing.T) {
	stderr := installExportFakes(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "export.jsonl")
	if err := os.WriteFile(path, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should stay empty with --out, got %q", stdout.String())
	}
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], path) {
		t.Fatalf("overwrite warning must be first stderr line, got %q", stderr.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("file body = %q, want %q", got, want)
	}
}

// RF2b — --out 신규 파일: 경고 없이 기록.
func TestRunExportOutNewFileNoWarning(t *testing.T) {
	stderr := installExportFakes(t, nil)
	path := filepath.Join(t.TempDir(), "new.jsonl")
	code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(stderr.String(), "overwrite") {
		t.Fatalf("no overwrite warning for new file, got %q", stderr.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("output file not written: %v", err)
	}
}

// RF3 — 각 format 스위치: 대응 포맷터 결과가 stdout으로, 요약에 포맷명.
func TestRunExportFormatSwitches(t *testing.T) {
	for _, format := range []string{"jsonl", "md", "txt", "csv"} {
		stderr := installExportFakes(t, nil)
		var stdout bytes.Buffer
		code := runExport(context.Background(), &fakeStore{}, []string{"--format", format}, strings.NewReader(""), &stdout)
		if code != 0 {
			t.Fatalf("format %s: exit = %d, want 0", format, code)
		}
		if want := fmt.Sprintf("<%s of 2 rows>\n", format); stdout.String() != want {
			t.Fatalf("format %s: stdout = %q, want %q", format, stdout.String(), want)
		}
		if !strings.Contains(stderr.String(), fmt.Sprintf("exported 2 rows (%s, ", format)) {
			t.Fatalf("format %s: summary missing format name: %q", format, stderr.String())
		}
	}
}

// RF4 — --since: RFC3339 파싱값이 원천 필터로 전달(컷오프).
func TestRunExportSinceCutoff(t *testing.T) {
	var got *knowledge.ExportFilter
	stderr := installExportFakes(t, func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
		got = &f
		return nil, nil
	})
	cutoff := "2026-11-01T00:00:00Z"
	code := runExport(context.Background(), &fakeStore{}, []string{"--since", cutoff}, strings.NewReader(""), &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if got == nil || !got.Since.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("--since not passed to source filter: %+v", got)
	}
}

// RF5 — 무효 format·status·since·unknown flag는 exit 3.
func TestRunExportInvalidValuesExit3(t *testing.T) {
	cases := [][]string{
		{"--format", "xml"},
		{"--status", "bogus"},
		{"--since", "not-a-date"},
		{"--format"}, // 값 없는 trailing 플래그
		{"--bogus-flag"},
	}
	for _, args := range cases {
		installExportFakes(t, nil)
		var stdout bytes.Buffer
		code := runExport(context.Background(), &fakeStore{}, args, strings.NewReader(""), &stdout)
		if code != 3 {
			t.Fatalf("args %v: exit = %d, want 3 (stdout %q)", args, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"error"`) {
			t.Fatalf("args %v: error JSON line expected on stdout, got %q", args, stdout.String())
		}
	}
}

// RF6 — 서버다운: Ping ErrServerDown → MapExit exit 2.
func TestRunExportServerDownExit2(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{pingErr: store.ErrServerDown}, nil, strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// RF6b — 스토어 nil → exit 2(계약: 스토어 nil·서버다운 exit 2).
func TestRunExportNilStoreExit2(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), nil, nil, strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// RF7 — 요약 stderr 정확 형식: exported N rows (format, M bytes).
func TestRunExportSummaryStderrFormat(t *testing.T) {
	stderr := installExportFakes(t, nil)
	var stdout bytes.Buffer
	if code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// fake jsonl 출력 "<jsonl of 2 rows>\n" = 18 bytes.
	want := "exported 2 rows (jsonl, 18 bytes)\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

// RF8 --status/--tag: 파싱값이 원천 필터로 전달(기본 active).
func TestRunExportStatusAndTagFilter(t *testing.T) {
	var got *knowledge.ExportFilter
	stderr := installExportFakes(t, func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
		got = &f
		return nil, nil
	})
	code := runExport(context.Background(), &fakeStore{}, []string{"--status", "all", "--tag", "demo"}, strings.NewReader(""), &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if got == nil || got.Status != "all" || got.Tag != "demo" {
		t.Fatalf("filters not passed: %+v", got)
	}
	// 기본값: status active.
	got = nil
	code = runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &bytes.Buffer{})
	if code != 0 || got == nil || got.Status != "active" {
		t.Fatalf("default status active expected: %+v (exit %d)", got, code)
	}
}

// exportSidecarHex — data의 sha256 hex(테스트 기대값 산출용).
func exportSidecarHex(t *testing.T, data []byte) string {
	t.Helper()
	d := sha256.Sum256(data)
	return hex.EncodeToString(d[:])
}

// RF9 — --out 성공 시 <FILE>.sha256 사이드카: sha256sum -c 호환 형식
// "<hex>  <basename>\n"(backup --out 관례와 동일 — ADR-0006).
func TestRunExportOutSidecarChecksum(t *testing.T) {
	stderr := installExportFakes(t, nil)
	path := filepath.Join(t.TempDir(), "export.jsonl")
	var stdout bytes.Buffer
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	want := exportSidecarHex(t, body) + "  export.jsonl\n"
	if string(side) != want {
		t.Fatalf("sidecar = %q, want %q", side, want)
	}
}

// RF10 — 재실행(내용 변화) 시 사이드카 갱신: 낡은 해시 잔존 없음.
func TestRunExportOutSidecarRefreshedOnRerun(t *testing.T) {
	installExportFakes(t, nil)
	path := filepath.Join(t.TempDir(), "export.jsonl")
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &bytes.Buffer{}); code != 0 {
		t.Fatalf("first run exit = %d, want 0", code)
	}
	first, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	// 두 번째 런: rows가 늘어 출력 바이트가 달라진다.
	old := exportRowsFn
	exportRowsFn = func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
		return []knowledge.ExportRow{{ID: 1, Topic: "demo-a"}, {ID: 2, Topic: "demo-b"}, {ID: 3, Topic: "demo-c"}}, nil
	}
	defer func() { exportRowsFn = old }()
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &bytes.Buffer{}); code != 0 {
		t.Fatalf("rerun exit = %d, want 0", code)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatalf("sidecar not refreshed on rerun: %q", second)
	}
	if want := exportSidecarHex(t, body) + "  export.jsonl\n"; string(second) != want {
		t.Fatalf("sidecar = %q, want %q", second, want)
	}
}

// RF11 — stdout 모드·--out 기록 실패 시 사이드카 미생성.
func TestRunExportNoSidecarOnStdoutOrOutFailure(t *testing.T) {
	// stdout 모드: cwd에 어떤 .sha256도 남기지 않는다.
	dir := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWD)
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	if code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("stdout mode exit = %d, want 0", code)
	}
	if _, err := os.Stat("out.jsonl.sha256"); err == nil {
		t.Fatal("stdout mode must not create a sidecar")
	}
	// --out 실패(경로가 디렉터리 → WriteFile 실패, exit 1): 사이드카 없음.
	installExportFakes(t, nil)
	d := t.TempDir()
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", d}, strings.NewReader(""), &bytes.Buffer{}); code != 1 {
		t.Fatalf("out failure exit = %d, want 1", code)
	}
	if _, err := os.Stat(d + ".sha256"); err == nil {
		t.Fatal("failed --out must not create a sidecar")
	}
}

// RF12 — 사이드카 기록 실패(사이드카 경로가 디렉터리): stderr 경고 +
// exit 0 유지(backup 관례 — 본문 기록 성공을 사이드카 실패가 해치지
// 않는다).
func TestRunExportSidecarFailureWarnsButExits0(t *testing.T) {
	stderr := installExportFakes(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "export.jsonl")
	if err := os.Mkdir(path+".sha256", 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", path}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("exit = %d, want 0 (sidecar failure must not fail export)", code)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "<jsonl of 2 rows>\n" {
		t.Fatalf("body must be written despite sidecar failure: %v", err)
	}
	if !strings.Contains(stderr.String(), "sidecar") {
		t.Fatalf("sidecar failure warning missing on stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("summary must survive sidecar failure: %q", stderr.String())
	}
}

// RF13 — --format pam: 파싱 통과(exit 0), pam 포맷터 결과가 stdout로,
// 요약에 포맷명. v1.4.0: PAM(Portable AI Memory) v1.0 교환 문서.
func TestRunExportFormatPAM(t *testing.T) {
	stderr := installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<pam of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (pam, ") {
		t.Fatalf("summary missing format name: %q", stderr.String())
	}
}

// RF13b — formatPAMFn seam 기본값은 실 FormatPAM에 "flint/"+버전으로
// 연결: 단일 JSON 문서·schema 필드·exported_by 계약.
func TestRunExportPAMSeamDefaultRealDoc(t *testing.T) {
	realPAM := formatPAMFn // installExportFakes가 fake를 깔기 전 기본 실구현
	if realPAM == nil {
		t.Fatal("formatPAMFn default must be the real FormatPAM wiring")
	}
	rows := []knowledge.ExportRow{{ID: 1, Topic: "demo-a", Status: "active"}, {ID: 2, Topic: "demo-b", Status: "active"}}
	doc, issued, err := realPAM(rows)
	if err != nil {
		t.Fatal(err)
	}
	if issued != 2 {
		t.Fatalf("issued count = %d, want 2(전행 발행)", issued)
	}
	var m struct {
		Schema        string `json:"schema"`
		SchemaVersion string `json:"schema_version"`
		ExportedBy    string `json:"exported_by"`
		Integrity     struct {
			TotalMemories int `json:"total_memories"`
		} `json:"integrity"`
	}
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatalf("pam output is not a JSON document: %v", err)
	}
	if m.Schema != "portable-ai-memory" || m.SchemaVersion != "1.0" {
		t.Fatalf("schema fields = %q/%q, want portable-ai-memory/1.0", m.Schema, m.SchemaVersion)
	}
	if want := "flint/" + CurrentVersion(); m.ExportedBy != want {
		t.Fatalf("exported_by = %q, want %q", m.ExportedBy, want)
	}
	if m.Integrity.TotalMemories != 2 {
		t.Fatalf("total_memories = %d, want 2", m.Integrity.TotalMemories)
	}
}

// RF13c — pam도 --out 사이드카 공통 적용(T2 구현이 전 형식 공통).
func TestRunExportPAMOutSidecar(t *testing.T) {
	stderr := installExportFakes(t, nil)
	path := filepath.Join(t.TempDir(), "export.pam.json")
	if code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam", "--out", path}, strings.NewReader(""), &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if _, err := os.Stat(path + ".sha256"); err != nil {
		t.Fatalf("pam sidecar missing: %v", err)
	}
}

// RF16 — v1.5.5 B1: --format pam 요약 행수는 pam 문서 발행 수
// (total_memories — pending은 pam 설계 제외)로 보고한다. 조회 전행이
// 3(1건 pending)이어도 요약은 "exported 2 rows".
func TestRunExportPAMSummaryCountsIssuedRows(t *testing.T) {
	realPAM := formatPAMFn // installExportFakes가 fake를 깔기 전 기본 실구현
	stderr := installExportFakes(t, func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
		return []knowledge.ExportRow{
			{ID: 1, Topic: "demo-a", Status: "active"},
			{ID: 2, Topic: "demo-b", Status: "active"},
			{ID: 3, Topic: "demo-pending", Status: "pending"},
		}, nil
	})
	formatPAMFn = realPAM
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (pam, ") {
		t.Fatalf("pam summary must count issued memories (pending excluded): %q", stderr.String())
	}
}

// RF16b — 포맷터가 보고한 발행 수가 요약에 그대로 쓰인다(전행 2·발행 1
// fake — 요약이 len(rows)가 아님을 증명).
func TestRunExportSummaryUsesFormatterIssuedCount(t *testing.T) {
	stderr := installExportFakes(t, nil)
	old := formatPAMFn
	formatPAMFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		return []byte("<pam issued 1>\n"), 1, nil
	}
	defer func() { formatPAMFn = old }()
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "exported 1 rows (pam, ") {
		t.Fatalf("summary must use formatter-issued count, not len(rows): %q", stderr.String())
	}
}

// maxRowsExportFake — thresholds IN 조회에 export_import_max_rows 오버라이드만
// 읽는 fake(나머지 조회는 0행). import maxRowsFake와 동일 응답 계약.
func maxRowsExportFake(max string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"export_import_max_rows", max}}, nil
		}
		return nil, nil
	}}
}

// RF14 — v1.4.1 B2: 조회 결과 행 수 == max(경계)는 통과. 상한 키는
// export_import_max_rows(import와 동일 키 — meta 오버라이드로 max=2).
func TestRunExportMaxRowsBoundary(t *testing.T) {
	stderr := installExportFakes(t, nil) // 기본 rows fake: 2행
	var stdout bytes.Buffer
	code := runExport(context.Background(), maxRowsExportFake("2"), nil, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(경계 ==max 통과; stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("summary missing: %q", stderr.String())
	}
}

// RF15 — v1.4.1 B2: 조회 결과 행 수 > max면 exit 3·오류 JSON {max,count}
// (import enforceImportMaxRows 형식 준용). 포맷터·출력은 실행되지 않는다.
func TestRunExportMaxRowsExceeded(t *testing.T) {
	installExportFakes(t, func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
		return []knowledge.ExportRow{
			{ID: 1, Topic: "demo-a", Status: "active"},
			{ID: 2, Topic: "demo-b", Status: "active"},
			{ID: 3, Topic: "demo-c", Status: "active"},
		}, nil
	})
	var stdout bytes.Buffer
	code := runExport(context.Background(), maxRowsExportFake("2"), nil, strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3(상한 초과; stdout %q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"max":2`) || !strings.Contains(stdout.String(), `"count":3`) {
		t.Fatalf("오류 JSON max·count 미포함: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "<jsonl") {
		t.Fatalf("상한 초과 시 포맷터 출력이 발생함: %q", stdout.String())
	}
}

// installExportTTY — v1.5.2 T1 적응형 진입 TTY 판정 seam 교체(기본
// 실구현 tui.IsTerminalStdout).
func installExportTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := exportTTYFn
	exportTTYFn = func() bool { return tty }
	t.Cleanup(func() { exportTTYFn = orig })
}

// installExportTUIFake — exportTUIFn seam 교체(호출 기록 + 지정 코드 반환).
func installExportTUIFake(t *testing.T, code int) *int {
	t.Helper()
	called := 0
	orig := exportTUIFn
	exportTUIFn = func(ctx context.Context, deps tui.ExportDeps) (int, error) {
		called++
		return code, nil
	}
	t.Cleanup(func() { exportTUIFn = orig })
	return &called
}

// RF-adaptive-② — 무인자·무플래그 + 터미널: TUI 흐름 위임·exit 통과.
func TestRunExportAdaptiveNoArgsTTYLaunchesTUI(t *testing.T) {
	installExportFakes(t, nil)
	called := installExportTUIFake(t, 7)
	installExportTTY(t, true)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (TUI 위임 코드 통과)", code)
	}
	if *called != 1 {
		t.Fatalf("exportTUIFn called %d times, want 1", *called)
	}
}

// RF-adaptive-③ — 무인자 + 비터미널(파이프): 기본 jsonl stdout 계약 불변.
func TestRunExportAdaptiveNoArgsPipedUnchanged(t *testing.T) {
	stderr := installExportFakes(t, nil)
	installExportTUIFake(t, 9) // 호출되면 9 — 파이프 경로가 우회하는지 검출
	installExportTTY(t, false)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF-adaptive-① — --tui 명시 + 비터미널: 기존 폴백 exit 3 불변
// (exportTUIFn 기본 실구현 = tui.RunExport — 폼 시작 전 즉시 오류).
func TestRunExportExplicitTUINonTerminalFallback(t *testing.T) {
	installExportFakes(t, nil)
	installExportTTY(t, false)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--tui"}, strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout %q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Fatalf("fallback message missing: %q", stdout.String())
	}
	// v1.5.5 T2(리뷰): 무효 인자 분류 — error:"internal" 폐지.
	if !strings.Contains(stdout.String(), `"error":"usage"`) {
		t.Fatalf("--tui 비터미널은 error:usage 여야 한다: %q", stdout.String())
	}
}

// RF-adaptive-④ — 플래그 존재 + 터미널: 현행 플래그 경로 유지(TUI 미진입).
func TestRunExportAdaptiveFlagWithTTYStaysFlagPath(t *testing.T) {
	stderr := installExportFakes(t, nil)
	installExportTUIFake(t, 9)
	installExportTTY(t, true)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "md"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<md of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (md, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF-noenv — FLINT_NO_TUI=1 + 터미널이어도 현행 jsonl 경로 강제(탈출구).
func TestRunExportNoTUIEnvForcesFlagPath(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	stderr := installExportFakes(t, nil)
	called := installExportTUIFake(t, 9) // 호출되면 9로 즉시 검출
	installExportTTY(t, true)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
	if *called != 0 {
		t.Fatalf("FLINT_NO_TUI must block TUI entry, called = %d", *called)
	}
}
