// read_fix_v0131_test.go — strengthened --status spec validation.
// (1) A trailing --status without a value (standalone or combined with --as-of) exits 3 under the same rule as the valueless --as-of
// guard — no silent ignoring. (2) Invalid status values are always validated
// regardless of --as-of (the as-of-precedence ruling for combinations applies only to valid
// values).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestReadStatusTrailingExits3 — a trailing --status (no value) is not silently ignored
// but exits 3. Since silently ignoring it under --as-of combination was the defect itself,
// both variants are covered. Validation must finish before any query is issued.
func TestReadStatusTrailingExits3(t *testing.T) {
	for _, args := range [][]string{
		{"--status"},
		{"--as-of", "7d", "--status"},
	} {
		var out bytes.Buffer
		f := &fakeStore{queryFn: statusQueryFn()}
		if code := runRead(context.Background(), asofFakeOrReal(t, f), args, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("args %v exit = %d, want 3 (out %q)", args, code, out.String())
		}
		if !strings.Contains(out.String(), `"field":"status"`) {
			t.Errorf("args %v status 스키마 에러 미출력: %q", args, out.String())
		}
		for _, q := range f.queries {
			t.Errorf("args %v 검증 전 질의 발행: %q", args, q)
		}
	}
}

// TestReadStatusInvalidWithAsOf — even combined with --as-of, an invalid status value is
// rejected with exit 3 (previously the as-of path ran first and silently ignored it).
func TestReadStatusInvalidWithAsOf(t *testing.T) {
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{queryFn: statusQueryFn()}}
	if code := runRead(context.Background(), readAsofStoreOrReal(t, f),
		[]string{"--as-of", "7d", "--status", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit = %d, want 3 (out %q)", code, out.String())
	}
	if !strings.Contains(out.String(), `"field":"status"`) {
		t.Errorf("status 스키마 에러 미출력: %q", out.String())
	}
	for _, q := range f.queries {
		t.Errorf("검증 전 질의 발행: %q", q)
	}
}
