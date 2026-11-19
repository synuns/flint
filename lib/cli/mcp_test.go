// mcp_test.go — tests for the flint mcp subcommand registration and delegation. The mcp package
// imports cli (cycle prevention), so only the injection point (RegisterMCPServer) is verified here.
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// TestMCPCommandRegisteredDelegates — registry registration + delegation to the injected loop.
func TestMCPCommandRegisteredDelegates(t *testing.T) {
	h, ok := Lookup("mcp")
	if !ok {
		t.Fatal("mcp 미등록")
	}
	var gotIn io.Reader
	prev := mcpServe
	mcpServe = func(stdin io.Reader, stdout io.Writer) int {
		gotIn = stdin
		io.WriteString(stdout, "served\n")
		return 7
	}
	t.Cleanup(func() { mcpServe = prev })

	var in, out bytes.Buffer
	in.WriteString("x")
	if code := h(context.Background(), nil, nil, &in, &out); code != 7 {
		t.Fatalf("exit = %d, want 7(위임 코드 반환)", code)
	}
	if gotIn != &in {
		t.Error("stdin이 루프에 그대로 전달되지 않음")
	}
	if out.String() != "served\n" {
		t.Errorf("stdout = %q, want served\\n", out.String())
	}
}

// TestMCPCommandUnlinked — a build without the mcp package linked gets one-line JSON, exit 1.
func TestMCPCommandUnlinked(t *testing.T) {
	h, ok := Lookup("mcp")
	if !ok {
		t.Fatal("mcp 미등록")
	}
	prev := mcpServe
	mcpServe = nil
	t.Cleanup(func() { mcpServe = prev })

	var in, out bytes.Buffer
	if code := h(context.Background(), nil, nil, &in, &out); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("출력 = %q, want 한 줄 JSON 에러", out.String())
	}
}

// TestRegisterMCPServer — the injected function overwrites the point value.
func TestRegisterMCPServer(t *testing.T) {
	prev := mcpServe
	t.Cleanup(func() { mcpServe = prev })
	fn := func(stdin io.Reader, stdout io.Writer) int { return 3 }
	RegisterMCPServer(fn)
	if mcpServe == nil {
		t.Fatal("주입 후에도 nil")
	}
	if code := mcpServe(nil, nil); code != 3 {
		t.Errorf("주입 함수 반환 = %d, want 3", code)
	}
}
