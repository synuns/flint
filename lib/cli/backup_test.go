package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flint/lib/backup"
	"flint/lib/db"
	"flint/lib/store"
)

// fakeBackup — lib/backup(T12) fake. stub(dumpErr·dumpText·restoreErr) 미지정
// 동작은 영(零) 덤프/성공. SUPERLITE_REAL=1이면 stub 미지정 시 실구현 위임.
type fakeBackup struct {
	dumpErr    error
	dumpText   string
	restoreErr error

	// 기록
	gotRestore string
}

// withBackupFake — dumpFn·restoreFn seam 교체. 기본 fake, FLINT_TEST_DSN 설정
// + SUPERLITE_REAL=1이면 stub 미설정 연산을 실Dump/Restore에 위임(위임 DSN도
// FLINT_TEST_DSN — 생산 코드가 넘긴 dsnFromEnv 결과에 우선). 실DB 미기동 시
// 스킵. FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(N1 — seamRealDSN).
func withBackupFake(t *testing.T, f *fakeBackup) {
	t.Helper()
	prevDump, prevRestore := dumpFn, restoreFn
	seamDSN, real := seamRealDSN()
	if real {
		pool, err := db.Connect(context.Background(), seamDSN)
		if err != nil {
			t.Skipf("real backup unavailable: %v", err)
		}
		pool.Close()
	}
	dumpFn = func(ctx context.Context, dsn string, w io.Writer) error {
		if f.dumpErr != nil {
			return f.dumpErr
		}
		if f.dumpText != "" || !real {
			_, err := io.WriteString(w, f.dumpText)
			return err
		}
		return backup.Dump(ctx, seamDSN, w)
	}
	restoreFn = func(ctx context.Context, dsn string, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		f.gotRestore = string(data)
		if f.restoreErr != nil {
			return f.restoreErr
		}
		if !real {
			return nil
		}
		return backup.Restore(ctx, seamDSN, strings.NewReader(string(data)))
	}
	t.Cleanup(func() { dumpFn, restoreFn = prevDump, prevRestore })
}

// mustBackup — runBackup/runRestore 직접 실행(root.go 등록 전 테스트 경로).
func mustBackup(t *testing.T, args []string) (string, int) {
	t.Helper()
	return runCmd(t, "backup", args)
}

func mustRestore(t *testing.T, args []string) (string, int) {
	t.Helper()
	return runCmd(t, "restore", args)
}

func runCmd(t *testing.T, cmd string, args []string) (string, int) {
	t.Helper()
	var out strings.Builder
	var code int
	full := append([]string{cmd}, args...)
	switch cmd {
	case "backup":
		code = runBackup(context.Background(), nil, full[1:], nil, &out)
	case "restore":
		code = runRestore(context.Background(), nil, full[1:], nil, &out)
	default:
		t.Fatalf("unknown cmd %q", cmd)
	}
	return out.String(), code
}

// Review Focus(file): fake Dump로 파일 생성.
func TestBackupWritesFile(t *testing.T) {
	f := &fakeBackup{dumpText: "-- flint backup probe\n"}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	out, code := mustBackup(t, []string{"--out", path})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("파일 미생성: %v", err)
	}
	if string(b) != f.dumpText {
		t.Fatalf("파일 내용=%q, want %q", b, f.dumpText)
	}
	m := decode(t, out)
	if m["status"] != "ok" || m["path"] != path {
		t.Fatalf("성공 JSON=%v", m)
	}
}

func TestBackupStdout(t *testing.T) {
	f := &fakeBackup{dumpText: "BEGIN;\nCOMMIT;\n"}
	withBackupFake(t, f)
	out, code := mustBackup(t, nil)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(f.dumpText) {
		t.Fatalf("stdout 덤프=%q, want %q", out, f.dumpText)
	}
}

func TestBackupDumpErrorExit1(t *testing.T) {
	f := &fakeBackup{dumpErr: errors.New("dump blew up")}
	withBackupFake(t, f)
	out, code := mustBackup(t, nil)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out) // 한 줄 JSON 에러
}

func TestBackupServerDownExit2(t *testing.T) {
	f := &fakeBackup{dumpErr: store.ErrServerDown}
	withBackupFake(t, f)
	out, code := mustBackup(t, []string{"--out", filepath.Join(t.TempDir(), "x.sql")})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if strings.TrimSpace(out) != `{"error":"server_down"}` {
		t.Fatalf("조용히 한 줄 JSON만: %q", out)
	}
}

// Review Focus(file): 경로 에러 — --out 경로값 누락·쓸 수 없는 경로.
func TestBackupBadPathExit1(t *testing.T) {
	f := &fakeBackup{dumpText: "x"}
	withBackupFake(t, f)
	out, code := mustBackup(t, []string{"--out"})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)

	out, code = mustBackup(t, []string{"--out", filepath.Join(t.TempDir(), "no", "such", "dir", "d.sql")})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestBackupUnknownFlagExit1(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	out, code := mustBackup(t, []string{"--bogus"})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestRestoreFromFile(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, []byte("BEGIN;\nCOMMIT;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := mustRestore(t, []string{"--file", path})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "restored" {
		t.Fatalf("status=%v, want restored", m["status"])
	}
	if !strings.Contains(f.gotRestore, "BEGIN;") {
		t.Fatalf("Restore에 파일 내용 전달 안 됨: %q", f.gotRestore)
	}
}

// Review Focus(file): 경로 에러 — --file 누락·존재하지 않는 파일.
func TestRestoreMissingFileFlagExit1(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	out, code := mustRestore(t, nil)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestRestoreFileNotFoundExit1(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	out, code := mustRestore(t, []string{"--file", filepath.Join(t.TempDir(), "absent.sql")})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestRestoreErrorExit1(t *testing.T) {
	f := &fakeBackup{restoreErr: store.ErrRetriesExhausted}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, []byte("-- x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := mustRestore(t, []string{"--file", path})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

// 실 Doltgres 왕복 — 덤프→전체 삭제→복구로 행 보존 검증(SUPERLITE_REAL=1 +
// FLINT_TEST_DSN 설정 시만 — N1, 전체 삭제를 하므로 운영 기본 DSN 금지).
func TestBackupRestoreRealRoundtrip(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("doltgres unavailable: %v", err)
	}
	defer pool.Close()

	// 고정 해시 프로브 — 재실행 시에도 1행만 존재(멱등).
	probe := `INSERT INTO knowledge (topic, summary, tags, domain, confidence, content_hash)
		VALUES ('backup rt', 't13 roundtrip probe', ':t13:', 'main', 0.9, 't13-roundtrip-probe')
		ON CONFLICT (content_hash) DO NOTHING`
	if _, err := pool.Exec(ctx, probe); err != nil {
		t.Fatalf("probe insert: %v", err)
	}

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	var dump bytes.Buffer
	if err := backup.Dump(ctx, dsn, &dump); err != nil {
		t.Fatalf("dump: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM knowledge`); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if err := backup.Restore(ctx, dsn, &dump); err != nil {
		t.Fatalf("restore: %v", err)
	}

	var after, probeN int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE content_hash = 't13-roundtrip-probe'`).Scan(&probeN); err != nil {
		t.Fatalf("probe count: %v", err)
	}
	if after != before || probeN != 1 {
		t.Fatalf("roundtrip: before=%d after=%d probe=%d, want %d/1", before, after, probeN, before)
	}
}

// TestBackupOutJSONWithRotate — F-T54-1 회귀: --out 성공 출력은 유효한 한 줄
// JSON이어야 한다(quoteJSON 뒤 겹따옴표로 파손 금지). 로테이션 삭제분은
// "removed":N 요약으로, 사이드카 성공 시 sha256과 공존한다.
func TestBackupOutJSONWithRotate(t *testing.T) {
	f := &fakeBackup{dumpText: "-- flint backup probe\n"}
	withBackupFake(t, f)
	prevRotate, prevKeep := rotateFn, keepFn
	rotateFn = func(dir string, keep int) ([]string, error) {
		return []string{"b.sql", "b.sql.sha256"}, nil
	}
	keepFn = func(getKV func(string) (string, error)) int { return 1 }
	t.Cleanup(func() { rotateFn, keepFn = prevRotate, prevKeep })

	path := filepath.Join(t.TempDir(), "a.sql")
	out, code := mustBackup(t, []string{"--out", path})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out) // 겹따옴표면 여기서 파싱 실패(F-T54-1 재현 지점)
	if m["status"] != "ok" || m["path"] != path {
		t.Fatalf("성공 JSON=%v, want ok/path", m)
	}
	if m["removed"] != float64(2) {
		t.Fatalf("removed=%v, want 2(.sql+.sha256)", m["removed"])
	}
	if m["sha256"] == nil {
		t.Fatalf("사이드카 성공 시 sha256 누락: %v", m)
	}
}

// TestBackupRotateFailureQuiet — 로테이션 실패는 백업 성공을 해치지 않는다
// (removed 필드 없음·exit 0, §13 비간섭).
func TestBackupRotateFailureQuiet(t *testing.T) {
	f := &fakeBackup{dumpText: "-- dump\n"}
	withBackupFake(t, f)
	prevRotate, prevKeep := rotateFn, keepFn
	rotateFn = func(dir string, keep int) ([]string, error) { return nil, errors.New("boom") }
	keepFn = func(getKV func(string) (string, error)) int { return 1 }
	t.Cleanup(func() { rotateFn, keepFn = prevRotate, prevKeep })

	path := filepath.Join(t.TempDir(), "a.sql")
	out, code := mustBackup(t, []string{"--out", path})
	if code != 0 {
		t.Fatalf("exit=%d, want 0(로테이션 실패는 무시); out=%s", code, out)
	}
	m := decode(t, out)
	if _, ok := m["removed"]; ok {
		t.Fatalf("로테이션 실패 시 removed 필드 있음: %v", m)
	}
}
