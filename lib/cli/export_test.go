// export_test.go — v1.3.0 T7: flint export CLI 배선 계약 테스트.
// 포맷터·원천 조회(T2~T6)는 seam var로 fake 주입 — RunExport 자체
// 계약(플래그 파싱·stdout/--out·경고·요약 stderr·exit 0/2/3)이 검증
// 대상이다. 실구현 연결은 seam 기본값 대입 시점(컴파일 시점)에 활성화.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
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
	mk := func(name string) func([]knowledge.ExportRow) ([]byte, error) {
		return func(rows []knowledge.ExportRow) ([]byte, error) {
			return []byte(fmt.Sprintf("<%s of %d rows>\n", name, len(rows))), nil
		}
	}
	var stderr bytes.Buffer
	oldRows, oldJ, oldM, oldT, oldC, oldErr :=
		exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, exportStderr
	exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, exportStderr =
		rowsFn, mk("jsonl"), mk("md"), mk("txt"), mk("csv"), &stderr
	t.Cleanup(func() {
		exportRowsFn, formatJSONLFn, formatMDFn, formatTXTFn, formatCSVFn, exportStderr =
			oldRows, oldJ, oldM, oldT, oldC, oldErr
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
