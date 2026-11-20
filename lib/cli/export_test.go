// export_test.go — contract tests for the flint export CLI wiring.
// The formatters and source query are faked via seam vars — the RunExport contract
// itself (flag parsing, stdout/--out, warnings, summary on stderr, exit 0/2/3) is the
// verification target. The real implementations are wired at the seam default assignment (compile time).
package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// installExportFakes — installs export-seam fakes in bulk + captures stderr.
// If rowsFn is nil, installs the default rows fake (2 rows); the formatter is a minimal fake that
// returns the format name as-is. The return value is the captured stderr buffer.
func installExportFakes(t *testing.T, rowsFn func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error)) *bytes.Buffer {
	t.Helper()
	if rowsFn == nil {
		rowsFn = func(ctx context.Context, st wire.Store, f knowledge.ExportFilter) ([]knowledge.ExportRow, error) {
			return []knowledge.ExportRow{{ID: 1, Topic: "demo-a", Status: "active"}, {ID: 2, Topic: "demo-b", Status: "active"}}, nil
		}
	}
	// the formatter seam is a 3-tuple (body, summary-reported row count, error) — the fake
	// reports the total row count (len(rows)) (only the real pam returns the published count).
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

// RF1 — defaults: jsonl format, stdout output, exit 0, summary on stderr.
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

// RF2 — --out: the one-line overwrite warning for an existing file goes to stderr before the file write.
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

// RF2b — --out new file: written with no warning.
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

// RF3 — each format switch: the corresponding formatter result to stdout, format name in the summary.
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

// RF4 — --since: the parsed RFC3339 value is passed to the source filter (cutoff).
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

// RF5 — invalid format, status, since, or unknown flag exits 3.
func TestRunExportInvalidValuesExit3(t *testing.T) {
	cases := [][]string{
		{"--format", "xml"},
		{"--status", "bogus"},
		{"--since", "not-a-date"},
		{"--format"}, // trailing flag without a value
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

// RF6 — server-down: Ping ErrServerDown -> MapExit exit 2.
func TestRunExportServerDownExit2(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{pingErr: store.ErrServerDown}, nil, strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// RF6b — nil store -> exit 2 (contract: nil store and server-down exit 2).
func TestRunExportNilStoreExit2(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), nil, nil, strings.NewReader(""), &stdout)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// RF7 — exact summary stderr format: exported N rows (format, M bytes).
func TestRunExportSummaryStderrFormat(t *testing.T) {
	stderr := installExportFakes(t, nil)
	var stdout bytes.Buffer
	if code := runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// the fake jsonl output "<jsonl of 2 rows>\n" = 18 bytes.
	want := "exported 2 rows (jsonl, 18 bytes)\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

// RF8 --status/--tag: parsed values passed to the source filter (default active).
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
	// default: status active.
	got = nil
	code = runExport(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &bytes.Buffer{})
	if code != 0 || got == nil || got.Status != "active" {
		t.Fatalf("default status active expected: %+v (exit %d)", got, code)
	}
}

// exportSidecarHex — sha256 hex of data (for computing test expectations).
func exportSidecarHex(t *testing.T, data []byte) string {
	t.Helper()
	d := sha256.Sum256(data)
	return hex.EncodeToString(d[:])
}

// RF9 — on --out success a <FILE>.sha256 sidecar: sha256sum -c compatible format
// "<hex>  <basename>\n" (same as the backup --out convention — ADR-0006).
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

// RF10 — on re-run (content change) the sidecar refreshes: no stale hash left behind.
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
	// second run: rows grow, so the output bytes differ.
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

// RF11 — stdout mode or a failed --out write creates no sidecar.
func TestRunExportNoSidecarOnStdoutOrOutFailure(t *testing.T) {
	// stdout mode: leaves no .sha256 in cwd.
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
	// --out failure (path is a directory -> WriteFile fails, exit 1): no sidecar.
	installExportFakes(t, nil)
	d := t.TempDir()
	if code := runExport(context.Background(), &fakeStore{}, []string{"--out", d}, strings.NewReader(""), &bytes.Buffer{}); code != 1 {
		t.Fatalf("out failure exit = %d, want 1", code)
	}
	if _, err := os.Stat(d + ".sha256"); err == nil {
		t.Fatal("failed --out must not create a sidecar")
	}
}

// RF18 — `--out -`: POSIX convention (path "-" = stdout) — exactly the same as
// omitting --out. The body goes to stdout; no `-` or `-.sha256` files are left in cwd.
func TestRunExportOutDashIsStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	stderr := installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--out", "-"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if got, want := stdout.String(), "<jsonl of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, name := range []string{"-", "-.sha256"} {
		if _, err := os.Stat(name); err == nil {
			t.Fatalf("`--out -` must not create %q in cwd", name)
		}
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (jsonl, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF12 — sidecar write failure (sidecar path is a directory): stderr warning +
// exit 0 kept (the backup convention — a sidecar failure must not harm the successful
// body write).
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

// RF13 — --format pam: parses fine (exit 0), the pam formatter result to stdout,
// format name in the summary. A PAM (Portable AI Memory) v1.0 exchange document.
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

// RF13b — the formatPAMFn seam default connects to the real FormatPAM with "flint/"+version:
// a single JSON document, schema field, and exported_by contract.
func TestRunExportPAMSeamDefaultRealDoc(t *testing.T) {
	realPAM := formatPAMFn // the default real implementation before installExportFakes lays down its fake
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

// RF13c — pam also gets the --out sidecar (the implementation is common to all formats).
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

// RF16 — the --format pam summary row count is reported as the pam published-document count
// (total_memories — pending excluded by pam design). Even with 3 total query rows
// (1 pending), the summary is "exported 2 rows".
func TestRunExportPAMSummaryCountsIssuedRows(t *testing.T) {
	realPAM := formatPAMFn // the default real implementation before installExportFakes lays down its fake
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

// RF16b — the published count reported by the formatter is used as-is in the summary (2 total rows, 1 published
// fake — proves the summary is not len(rows)).
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

// --- §18 `--sign` entry (pam-only via --format pam) ---

// installPAMSignFakes — swaps exportSignKeyFn and formatPAMSignedFn (call-recording
// wrappers). Defaults are a fixed synthetic key and a minimal fake body; with SUPERLITE_REAL=1,
// the same tests are re-verified against the real implementation (real key creation in a
// temp path, real FormatPAMSigned wiring) (seam check). Returns: (call count, passed key).
func installPAMSignFakes(t *testing.T) (*int, *ed25519.PrivateKey) {
	t.Helper()
	var keyFn func() (ed25519.PrivateKey, error)
	var inner func([]knowledge.ExportRow, ed25519.PrivateKey) ([]byte, int, error)
	if os.Getenv("SUPERLITE_REAL") == "1" {
		path := filepath.Join(t.TempDir(), "pam-signing-key")
		knowledge.PAMSigningKeyPathFn = func() string { return path }
		keyFn = knowledge.LoadOrCreatePAMSigningKey
		inner = func(rows []knowledge.ExportRow, k ed25519.PrivateKey) ([]byte, int, error) {
			data, err := knowledge.FormatPAMSigned(rows, "flint/"+CurrentVersion(), k)
			if err != nil {
				return nil, 0, err
			}
			var doc struct {
				Integrity struct {
					TotalMemories int `json:"total_memories"`
				} `json:"integrity"`
			}
			if jerr := json.Unmarshal(data, &doc); jerr != nil {
				return nil, 0, jerr
			}
			return data, doc.Integrity.TotalMemories, nil
		}
	} else {
		seed := sha256.Sum256([]byte("flint-cli-pam-sign-demo-seed"))
		key := ed25519.NewKeyFromSeed(seed[:])
		keyFn = func() (ed25519.PrivateKey, error) { return key, nil }
		inner = func(rows []knowledge.ExportRow, k ed25519.PrivateKey) ([]byte, int, error) {
			return []byte("<pam signed>\n"), 2, nil
		}
	}
	calls := 0
	var gotKey ed25519.PrivateKey
	oldKey, oldFn, oldPath := exportSignKeyFn, formatPAMSignedFn, knowledge.PAMSigningKeyPathFn
	exportSignKeyFn = keyFn
	formatPAMSignedFn = func(rows []knowledge.ExportRow, k ed25519.PrivateKey) ([]byte, int, error) {
		calls++
		gotKey = k
		return inner(rows, k)
	}
	t.Cleanup(func() {
		exportSignKeyFn, formatPAMSignedFn, knowledge.PAMSigningKeyPathFn = oldKey, oldFn, oldPath
	})
	return &calls, &gotKey
}

// RF17 — `--format pam --sign`: key loader -> signed-formatter entry, exit 0.
// With SUPERLITE_REAL=1, the real FormatPAMSigned document goes to stdout.
func TestRunExportPAMSignEntry(t *testing.T) {
	stderr := installExportFakes(t, nil)
	calls, gotKey := installPAMSignFakes(t)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam", "--sign"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if *calls != 1 {
		t.Fatalf("formatPAMSignedFn 호출 = %d회, want 1", *calls)
	}
	if *gotKey == nil {
		t.Fatalf("서명 포맷터에 키가 전달되지 않음")
	}
	if os.Getenv("SUPERLITE_REAL") == "1" {
		var doc map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			t.Fatalf("실구현 stdout이 PAM 문서 아님: %v\n%s", err, stdout.String())
		}
		if _, ok := doc["signature"]; !ok {
			t.Fatalf("실구현 서명 문서에 signature 블록 없음: %s", stdout.String())
		}
	} else if got, want := stdout.String(), "<pam signed>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (pam, ") {
		t.Fatalf("summary missing: %q", stderr.String())
	}
}

// RF17b — --sign is pam-only: combining with other formats is a usage error, exit 3.
func TestRunExportSignRequiresPAM(t *testing.T) {
	installExportFakes(t, nil)
	calls, _ := installPAMSignFakes(t)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "jsonl", "--sign"}, strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (--sign는 --format pam 전용)", code)
	}
	if *calls != 0 {
		t.Fatalf("무효 조합인데 서명 포맷터 호출됨")
	}
	if !strings.Contains(stdout.String(), "sign") {
		t.Fatalf("오류에 sign 필드 없음: %q", stdout.String())
	}
}

// RF17c — key load failure is an error exit 1 (no document published).
func TestRunExportSignKeyFailureExit1(t *testing.T) {
	installExportFakes(t, nil)
	calls, _ := installPAMSignFakes(t)
	old := exportSignKeyFn
	exportSignKeyFn = func() (ed25519.PrivateKey, error) { return nil, errors.New("demo key failure") }
	defer func() { exportSignKeyFn = old }()
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "pam", "--sign"}, strings.NewReader(""), &stdout)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (키 로드 실패)", code)
	}
	if *calls != 0 {
		t.Fatalf("키 실패인데 서명 포맷터 호출됨")
	}
	if !strings.Contains(stdout.String(), "demo key failure") {
		t.Fatalf("오류 원인 누락: %q", stdout.String())
	}
}

// maxRowsExportFake — a fake that reads only the export_import_max_rows override from
// thresholds IN lookups (0 rows for the rest). Same response contract as the import maxRowsFake.
func maxRowsExportFake(max string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"export_import_max_rows", max}}, nil
		}
		return nil, nil
	}}
}

// RF14 — query result rows == max (boundary) passes. The cap key is
// export_import_max_rows (shared with import — meta override sets max=2).
func TestRunExportMaxRowsBoundary(t *testing.T) {
	stderr := installExportFakes(t, nil) // default rows fake: 2 rows
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

// RF15 — query result rows > max exits 3 with the error JSON {max,count}
// (following the import enforceImportMaxRows format). Formatters and output never run.
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

// installExportTTY — swaps the adaptive-entry TTY-check seam (default real
// implementation tui.IsTerminalStdout).
func installExportTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := exportTTYFn
	exportTTYFn = func() bool { return tty }
	t.Cleanup(func() { exportTTYFn = orig })
}

// installExportTUIFake — swaps the exportTUIFn seam (records calls + returns the given code).
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

// RF-adaptive-2 — no args or flags + terminal: delegates to the TUI flow, exit passes through.
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

// RF-adaptive-3 — no args + non-terminal (pipe): the default jsonl stdout contract is unchanged.
func TestRunExportAdaptiveNoArgsPipedUnchanged(t *testing.T) {
	stderr := installExportFakes(t, nil)
	installExportTUIFake(t, 9) // would return 9 — detects whether the pipe path bypasses it
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

// RF-adaptive-1 — explicit --tui + non-terminal: the existing fallback exit 3 unchanged
// (exportTUIFn default = tui.RunExport — immediate error before the form starts).
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
	if !strings.Contains(stdout.String(), `"error":"usage"`) {
		t.Fatalf("--tui 비터미널은 error:usage 여야 한다: %q", stdout.String())
	}
}

// RF-adaptive-4 — flags present + terminal: the current flag path is kept (no TUI entry).
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

// RF-noenv — FLINT_NO_TUI=1 forces the current jsonl path even on a terminal (escape hatch).
func TestRunExportNoTUIEnvForcesFlagPath(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	stderr := installExportFakes(t, nil)
	called := installExportTUIFake(t, 9) // would return 9 for instant detection
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
