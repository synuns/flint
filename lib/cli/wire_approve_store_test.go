// wire_approve_store_test.go — 스펙 §1.4/R14: approve·done·backup가
// root가 주입한 Store를 쓰는지(자체 db.NewRepo/db.Connect 폐지)와
// 파일 내 중복 구현 수렴(quoteJSON·thresholds 로더)을 검증한다.
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
	"time"
)

// approveStoreFake — fakeStore에 Approve 승격 스텁을 얹은 fake.
// 승격 행위(실DB UPDATE)는 approve_test의 real seam이 담당 — 여기는
// 배선(주입 st 사용·자체 연결 미발동)만 계약으로 삼는다.
type approveStoreFake struct {
	*fakeStore

	approveN   int
	gotID      int
	approveErr error
}

func (a *approveStoreFake) Approve(ctx context.Context, id int) error {
	a.approveN++
	a.gotID = id
	return a.approveErr
}

// TestApproveUsesInjectedStore — Review Focus: fake st 주입 시 자체 연결
// 경로(approveFn 기본 실구현의 db.NewRepo) 미호출·승격은 주입 st 경유.
func TestApproveUsesInjectedStore(t *testing.T) {
	f := &approveStoreFake{fakeStore: &fakeStore{}}
	// sentinel — 재배선 전 runApprove는 st를 무시하고 이 seam을 부르므로
	// 호출 자체가 실패 신호다(기본 실구현 대신 호출 카운터로 안전하게).
	prev := approveFn
	selfConn := 0
	approveFn = func(ctx context.Context, id int) error {
		selfConn++
		return errSelfConnectDisabled
	}
	t.Cleanup(func() { approveFn = prev })

	var out bytes.Buffer
	code := runApprove(context.Background(), f,
		[]string{"--id", "7"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s", code, out.String())
	}
	if selfConn != 0 {
		t.Fatalf("자체 연결 경로(approveFn) 호출 %d회, want 0 — st 재배선 미적용", selfConn)
	}
	if f.approveN != 1 || f.gotID != 7 {
		t.Fatalf("주입 st Approve 호출 (n=%d, id=%d), want (1, 7)", f.approveN, f.gotID)
	}
	if !strings.Contains(out.String(), `"status":"approved"`) {
		t.Fatalf("out=%q, want approved JSON", out.String())
	}
}

// errSelfConnectDisabled — sentinel 전용 에러(재배선 후 도달 불가 경로).
var errSelfConnectDisabled = &selfConnectError{}

type selfConnectError struct{}

func (*selfConnectError) Error() string { return "self-connect path must not run" }

// TestQuoteJSONSingle — Review Focus: quoteJSON 1구현으로 수렴.
// approve.go의 quoteJSONApprove는 backup.go quoteJSON과 바이트 동일이므로
// 소스에 잔존하지 않아야 한다(행위는 quoteJSON 자체 검증).
func TestQuoteJSONSingle(t *testing.T) {
	b, err := os.ReadFile("approve.go")
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
// writeThresholds(write.go) 공용. done.go에 로컬 로더가 남으면 실패.
func TestThresholdsLoaderSingle(t *testing.T) {
	b, err := os.ReadFile("done.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "doneThresholds") {
		t.Fatalf("doneThresholds 잔존 — writeThresholds(write.go) 공용 로더로 수렴 필요")
	}
	if !strings.Contains(src, "writeThresholds") {
		t.Fatalf("done.go가 writeThresholds 공용 로더를 사용하지 않음")
	}
}

// TestDoneAutoBackupNoExtraPool — Review Focus: maybeAutoBackup이 자체
// 풀(db.Connect)을 열지 않고 주입 st로 meta에 접근한다. 도달 불가 DSN에서
// 자체 풀 우선 구현은 st 조회 전 조용히 반환되어 fake 기록이 비게 된다.
func TestDoneAutoBackupNoExtraPool(t *testing.T) {
	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())
	t.Setenv("FLINT_DSN", "postgresql://flint@127.0.0.1:1/flint?sslmode=disable&connect_timeout=1")
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			// last_backup=방금 → 덤프 미발동(연결 시도 자체가 없어야 한다).
			return [][]any{{time.Now().UTC().Format(time.RFC3339)}}, nil
		}
		return nil, nil
	}}
	maybeAutoBackup(context.Background(), f)
	metaSeen := false
	for _, q := range f.queries {
		if strings.Contains(q, "FROM meta") {
			metaSeen = true
		}
	}
	if !metaSeen {
		t.Fatalf("meta 조회가 주입 st를 경유하지 않음(queries=%d) — 자체 풀 경로 잔존 의심", len(f.queries))
	}
}
