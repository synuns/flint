package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

func TestQuoteJSONSingle(t *testing.T) {
	b, err := os.ReadFile("../knowledge/approve.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "quoteJSONApprove") {
		t.Fatalf("quoteJSONApprove 잔존 — quoteJSON(backup.go 소유)로 수렴 필요")
	}
	for _, s := range []string{"", `경로 "x"`, "줄바꿈\n들어감", "tab\tnext"} {
		want, merr := json.Marshal(s)
		if merr != nil {
			t.Fatal(merr)
		}
		if got := quoteJSON(s); got != string(want) {
			t.Fatalf("quoteJSON(%q) = %s, want %s", s, got, want)
		}
	}
}

type dumpStoreFake struct {
	*fakeStore

	dumpN int
}

func (d *dumpStoreFake) Dump(ctx context.Context, w io.Writer) error {
	d.dumpN++
	fmt.Fprint(w, "-- flint backup (fake)\nBEGIN;\nCOMMIT;\n")
	return nil
}

func TestBackupUsesInjectedStore(t *testing.T) {
	prev := dumpFn
	selfConn := 0
	dumpFn = func(ctx context.Context, dsn string, w io.Writer) error {
		selfConn++
		return errSelfConnectDisabled
	}
	t.Cleanup(func() { dumpFn = prev })

	f := &dumpStoreFake{fakeStore: &fakeStore{}}
	out := filepath.Join(t.TempDir(), "b.sql")
	var buf bytes.Buffer
	code := runBackup(context.Background(), f,
		[]string{"--out", out}, strings.NewReader(""), &buf)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s", code, buf.String())
	}
	if selfConn != 0 {
		t.Fatalf("자체 연결 경로(dumpFn) 호출 %d회, want 0 — st 재배선 미적용", selfConn)
	}
	if f.dumpN != 1 {
		t.Fatalf("주입 st Dump 호출 %d회, want 1", f.dumpN)
	}
	if !strings.Contains(buf.String(), `"status":"ok"`) {
		t.Fatalf("out=%q, want ok JSON", buf.String())
	}
}

func TestThresholdsLoaderSingle(t *testing.T) {
	b, err := os.ReadFile("../knowledge/done.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "doneThresholds") {
		t.Fatalf("doneThresholds 잔존 — WriteThresholds(write.go) 공용 로더로 수렴 필요")
	}
	if !strings.Contains(src, "WriteThresholds") {
		t.Fatalf("knowledge/done.go가 WriteThresholds 공용 로더를 사용하지 않음")
	}
}
