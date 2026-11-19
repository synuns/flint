// run_test.go — the ErrLine usage-classification contract. Invalid arguments (unknown
// flag, missing value, etc.) yield error:"usage" (a value extension — structure unchanged,
// downstream-parser compatible); internal defects stay internal; SchemaError stays
// schema (regression). Plus the --version/-v global alias and the help/unknown
// guidance form (error key abolished) contracts.
package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// decodeErrLine — decodes the ErrLine one-line JSON into a map.
func decodeErrLine(t *testing.T, line string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("ErrLine JSON 디코드 실패: %v (%q)", err, line)
	}
	return m
}

// TestErrLineUsage — a usage error's error value is
// "usage" (only the value replaced from the old internal).
func TestErrLineUsage(t *testing.T) {
	t.Run("usage 오류 — error:usage", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(UsageErrorf("unknown flag: %s", "--wat")))
		if m["error"] != "usage" {
			t.Errorf("error = %q, want usage", m["error"])
		}
		if !strings.Contains(m["message"], "unknown flag: --wat") {
			t.Errorf("message = %q, want 원 메시지 보존", m["message"])
		}
	})
	t.Run("내부 결함 — internal 유지", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(errors.New("boom")))
		if m["error"] != "internal" {
			t.Errorf("error = %q, want internal(내부 결함은 유지)", m["error"])
		}
	})
	t.Run("SchemaError — schema 유지(회귀)", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(&store.SchemaError{Field: "id", Expect: "integer"}))
		if m["error"] != "schema" {
			t.Errorf("error = %q, want schema", m["error"])
		}
	})
}

// TestUsageErrorfWrap — UsageErrorf supports %w wrapping, and errors.Is can
// resolve the original (UsageError is a classification marker — the original is preserved).
func TestUsageErrorfWrap(t *testing.T) {
	inner := errors.New("bad arg")
	ue := UsageErrorf("wrap: %w", inner)
	if !errors.Is(ue, inner) {
		t.Errorf("errors.Is(UsageError, 원본) = false, want true")
	}
	if ue.Error() != "wrap: bad arg" {
		t.Errorf("Error() = %q, want wrap: bad arg", ue.Error())
	}
}

// TestVersionFlagAlias — `flint --version`·
// `flint -v` is a global flag alias — the same output and exit 0 as `flint version`.
// Being a store-free pre-step, it does not conflict with the unknown-command exit 3.
func TestVersionFlagAlias(t *testing.T) {
	probeHandler(t, "version", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
		if st != nil {
			t.Error("version: store-free 경로인데 store 주입됨")
		}
		io.WriteString(stdout, "flint v9.9.9-test\n")
		return 0
	})
	for _, argv := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		var out bytes.Buffer
		if code := Run(argv, strings.NewReader(""), &out, failFactory(t)); code != 0 {
			t.Fatalf("Run(%v) exit=%d, want 0(version과 동일)", argv, code)
		}
		if got := strings.TrimSpace(out.String()); got != "flint v9.9.9-test" {
			t.Fatalf("Run(%v) 출력=%q, want version과 동일 출력", argv, got)
		}
	}
	// a near miss (--versio) is not an alias — unknown command keeps exit 3.
	var out bytes.Buffer
	if code := Run([]string{"--versio"}, strings.NewReader(""), &out, func(ctx context.Context, dsn string) (Store, error) {
		return nil, errors.New("unused")
	}); code != 3 {
		t.Fatalf("Run(--versio) exit=%d, want 3(unknown command 계약 유지)", code)
	}
}

// TestHelpFlagAlias — `flint -h` and `flint --help` are
// global aliases — the same output and exit 0 as `flint help` (inheriting the
// --version/-v convention, a pre-dispatch rewrite). A near miss (--hel) is not an alias (unknown 3).
func TestHelpFlagAlias(t *testing.T) {
	var want bytes.Buffer
	if code := RunHelp(&want); code != 0 {
		t.Fatalf("RunHelp exit=%d, want 0", code)
	}
	for _, argv := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		var out bytes.Buffer
		if code := Run(argv, strings.NewReader(""), &out, failFactory(t)); code != 0 {
			t.Fatalf("Run(%v) exit=%d, want 0(help와 동일)", argv, code)
		}
		if out.String() != want.String() {
			t.Fatalf("Run(%v) 출력이 flint help와 불일치 — got %q, want %q", argv, out.String(), want.String())
		}
	}
	var out bytes.Buffer
	if code := Run([]string{"--hel"}, strings.NewReader(""), &out, func(ctx context.Context, dsn string) (Store, error) {
		return nil, errors.New("unused")
	}); code != 3 {
		t.Fatalf("Run(--hel) exit=%d, want 3(오타는 별칭 아님 — unknown command 유지)", code)
	}
}

// TestHelpInformationalFirstLine — help
// The output's first line is the guidance form without an error key — the
// informational content (the command list) is kept, and the exit 0 contract too.
func TestHelpInformationalFirstLine(t *testing.T) {
	var out bytes.Buffer
	if code := RunHelp(&out); code != 0 {
		t.Fatalf("RunHelp exit=%d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("help 출력 라인 수 부족: %q", out.String())
	}
	first := decodeErrLine(t, lines[0])
	if _, hasErr := first["error"]; hasErr {
		t.Errorf("help 첫 줄에 error 키 잔존: %q", lines[0])
	}
	if !strings.Contains(lines[0], "flint <") || !strings.Contains(lines[0], "write") {
		t.Errorf("help 첫 줄 usage 정보 내용 유지 실패: %q", lines[0])
	}
}

// TestUnknownCommandGuidanceNoErrorKey — the unknown-command guidance is also in
// the guidance form without an error key — the unknown-command fact (message) and the usage information are kept, and the exit stays at the existing contract of 3.
func TestUnknownCommandGuidanceNoErrorKey(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"no-such-cmd"}, strings.NewReader(""), &out, func(ctx context.Context, dsn string) (Store, error) {
		return nil, errors.New("unused")
	}); code != 3 {
		t.Fatalf("exit=%d, want 3(기존 계약 유지)", code)
	}
	line := strings.TrimSpace(out.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("unknown command 안내=%q, want 한 줄 JSON", line)
	}
	m := decodeErrLine(t, line)
	if _, hasErr := m["error"]; hasErr {
		t.Errorf("unknown command 안내에 error 키 잔존: %q", line)
	}
	if !strings.Contains(m["message"], "unknown command: no-such-cmd") {
		t.Errorf("unknown command 사실 유지 실패: message=%q", m["message"])
	}
	if !strings.Contains(m["usage"], "flint <") || !strings.Contains(m["usage"], "read") {
		t.Errorf("usage 정보 내용 유지 실패: usage=%q", m["usage"])
	}
}

// TestUnknownCommandLineEscapesCommand (an escaping regression):
// the guidance-line assembly passes the caller-controlled string (cmd)
// through json.Marshal — raw concatenation cannot corrupt the JSON structure.
func TestUnknownCommandLineEscapesCommand(t *testing.T) {
	line := UnknownCommandLine(`x"}{"injected":1`)
	m := decodeErrLine(t, line)
	if !strings.Contains(m["message"], `unknown command: x"}{"injected":1`) {
		t.Errorf("원 메시지 보존 실패: %q", m["message"])
	}
	if _, injected := m["injected"]; injected {
		t.Errorf("JSON 주입 성공 — 이스케이프 위반: %q", line)
	}
}
