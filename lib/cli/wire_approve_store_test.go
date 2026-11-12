// wire_approve_store_test.go — 스펙 §1.4/R14: approve·done·backup이
// root가 주입한 Store를 쓰는지(자체 db.NewRepo/db.Connect 폐지)와 파일 내
// 중복 구현 수렴(quoteJSON·thresholds 로더)을 검증한다.
// v0.39.0 T4: approve·done 구현은 lib/knowledge로 이동 — approve 재배선
// 검증(TestApproveUsesInjectedStore)·maybeAutoBackup st 경유 검증
// (TestDoneAutoBackupNoExtraPool)은 knowledge 패키지 테스트로 이동했고,
// 여기선 cli 소유 경로(backup)와 이동 위치의 소스 가드만 담는다.
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

// errSelfConnectDisabled — sentinel 전용 에러(재배선 후 도달 불가 경로).
var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

// TestQuoteJSONSingle — Review Focus: quoteJSON 1구현으로 수렴.
// knowledge/approve.go의 quoteJSONApprove는 backup.go quoteJSON과 바이트
// 동일이므로 소스에 잔존하지 않아야 한다(행위는 quoteJSON 자체 검증).
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

// dumpStoreFake — fakeStore에 전체 덤프 스텁을 얹은 fake(backup 경로).
type dumpStoreFake struct {
	*fakeStore

	dumpN int
}

func (d *dumpStoreFake) Dump(ctx context.Context, w io.Writer) error {
	d.dumpN++
	fmt.Fprint(w, "-- flint backup (fake)\nBEGIN;\nCOMMIT;\n")
	return nil
}

// TestBackupUsesInjectedStore — dumpFn 자체 풀 경로 폐지: 주입 st가 덤프를
// 위임받고 dumpFn(기본 자체 연결)은 호출되지 않는다.
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

// TestThresholdsLoaderSingle — Review Focus: doneThresholds 제거·
// WriteThresholds(write.go) 공용. knowledge/done.go에 로컬 로더가 남으면
// 실패(v0.39.0 T4: done.go 이동 위치 검사).
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
