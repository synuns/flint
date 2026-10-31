package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpExitsZeroAndPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	if code := runHelp(&out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "usage: flint") {
		t.Errorf("usage 문구 누락: %q", got)
	}
	if !strings.Contains(got, "read") || !strings.Contains(got, "doctor") {
		t.Errorf("명령 요약 누락: %q", got)
	}
}

func TestNoArgsUnchanged(t *testing.T) {
	var out bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("no-args exit = %d, want 1", code)
	}
	if out.String() != usageLine()+"\n" {
		t.Errorf("no-args 출력 변형: %q", out.String())
	}
}
