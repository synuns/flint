package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

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
