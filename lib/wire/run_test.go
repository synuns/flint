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

func decodeErrLine(t *testing.T, line string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("ErrLine JSON 디코드 실패: %v (%q)", err, line)
	}
	return m
}

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

	var out bytes.Buffer
	if code := Run([]string{"--versio"}, strings.NewReader(""), &out, func(ctx context.Context, dsn string) (Store, error) {
		return nil, errors.New("unused")
	}); code != 3 {
		t.Fatalf("Run(--versio) exit=%d, want 3(unknown command 계약 유지)", code)
	}
}

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
