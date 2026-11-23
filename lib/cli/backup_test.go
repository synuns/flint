package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

type fakeBackup struct {
	dumpErr    error
	dumpText   string
	restoreErr error

	gotRestore string
}

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
	decode(t, out)
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

func TestBackupBadPathExit1(t *testing.T) {
	f := &fakeBackup{dumpText: "x"}
	withBackupFake(t, f)
	out, code := mustBackup(t, []string{"--out"})
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)

	out, code = mustBackup(t, []string{"--out", filepath.Join(t.TempDir(), "no", "such", "dir", "d.sql")})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

func TestBackupUnknownFlagExit3(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	out, code := mustBackup(t, []string{"--bogus"})
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)
}

func TestRestoreFromFile(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, []byte("BEGIN;\nCOMMIT;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256([]byte("BEGIN;\nCOMMIT;\n"))
	if err := backup.WriteSidecar(path, hex.EncodeToString(digest[:])); err != nil {
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

func TestRestoreMissingFileFlagExit3(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	out, code := mustRestore(t, nil)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
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
	m := decode(t, out)
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

func TestBackupEncryptWritesAgeFile(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	withBackupFake(t, &fakeBackup{dumpText: "-- flint dump\nINSERT INTO knowledge VALUES (1,'평문 본문');"})
	out := filepath.Join(t.TempDir(), "b.sql.age")
	got, code := mustBackup(t, []string{"--out", out, "--encrypt"})
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, got)
	}
	if !strings.Contains(got, `"encrypted":true`) {
		t.Fatalf("출력: %s", got)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("age-encryption.org/")) {
		t.Error("age 헤더 없음")
	}
	if bytes.Contains(b, []byte("평문 본문")) {
		t.Error("평문 노출")
	}

	sum, verr := backup.VerifyFile(out)
	if verr != nil {
		t.Fatalf("사이드카 검증: %v", verr)
	}
	h := sha256.Sum256(b)
	if sum != hex.EncodeToString(h[:]) {
		t.Error("사이드카가 암호문 기준 아님")
	}
}

func TestBackupMaskDump(t *testing.T) {
	withBackupFake(t, &fakeBackup{dumpText: "-- flint dump\nINSERT INTO knowledge VALUES (1,'토큰 ghp_0123456789abcdefghijklmnopqrst');"})
	out := filepath.Join(t.TempDir(), "m.sql")
	got, code := mustBackup(t, []string{"--out", out, "--mask"})
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, got)
	}
	if !strings.Contains(got, `"masked":true`) {
		t.Fatalf("출력: %s", got)
	}
	b, _ := os.ReadFile(out)
	if !bytes.HasPrefix(b, []byte(backup.MaskDumpHeader)) {
		t.Error("마스킹 마커 없음")
	}
	if bytes.Contains(b, []byte("ghp_0123456789")) {
		t.Error("토큰 미치환")
	}
	if _, serr := os.Stat(out + ".sha256"); serr == nil {
		t.Error("마스킹 덤프에 사이드카 부여 — 비복원 계약 위반")
	}

	rgot, rcode := mustRestore(t, []string{"--file", out})
	if rcode != 1 || !strings.Contains(rgot, "sidecar") {
		t.Fatalf("마스킹 restore = exit %d %s, want 사이드카 차단", rcode, rgot)
	}
}

func TestRestoreDecryptsEncrypted(t *testing.T) {
	cfg := t.TempDir()
	key := filepath.Join(cfg, "backup.key")
	id, err := backup.EnsureIdentity(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := "-- flint dump\nINSERT INTO knowledge VALUES (1,'복호 검증 본문');"
	var ct bytes.Buffer
	if err := backup.EncryptTo(&ct, []byte(plain), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "b.sql.age")
	os.WriteFile(f, ct.Bytes(), 0o600)
	h := sha256.Sum256(ct.Bytes())
	backup.WriteSidecar(f, hex.EncodeToString(h[:]))

	t.Setenv("FLINT_CONFIG_DIR", cfg)
	fb := &fakeBackup{}
	withBackupFake(t, fb)

	restoreFn = func(ctx context.Context, dsn string, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		fb.gotRestore = string(data)
		return nil
	}
	got, code := mustRestore(t, []string{"--file", f})
	if code != 0 || !strings.Contains(got, `"status":"restored"`) {
		t.Fatalf("복호 restore = exit %d %s", code, got)
	}
	if !strings.Contains(fb.gotRestore, "복호 검증 본문") {
		t.Errorf("restoreFn 평문 미수신: %q", fb.gotRestore)
	}

	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	withBackupFake(t, &fakeBackup{})
	got2, code2 := mustRestore(t, []string{"--file", f})
	if code2 != 1 || !strings.Contains(got2, "key file required") {
		t.Fatalf("키 없음 = exit %d %s", code2, got2)
	}
}

func TestOutInsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	sub := filepath.Join(repo, "deep", "nested")
	os.MkdirAll(sub, 0o755)
	if !outInsideGitRepo(filepath.Join(sub, "dump.sql")) {
		t.Error("저장소 하위 디렉터리 미감지")
	}
	if !outInsideGitRepo(filepath.Join(repo, "dump.sql")) {
		t.Error("저장소 루트 미감지")
	}
	if outInsideGitRepo(filepath.Join(dir, "outside.sql")) {
		t.Error("저장소 밖 오탐")
	}
}

func TestFlintHomeOverrideAndMigration(t *testing.T) {
	legacy := t.TempDir()
	home := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", legacy)
	t.Setenv("FLINT_HOME", home)
	os.WriteFile(filepath.Join(legacy, "backup.key"), []byte("AGE-SECRET-KEY-1SAMPLE"), 0o600)
	got := BackupKeyPath()
	if got != filepath.Join(home, "backup.key") {
		t.Fatalf("BackupKeyPath = %q, want FLINT_HOME 하위", got)
	}
	b, err := os.ReadFile(got)
	if err != nil || string(b) != "AGE-SECRET-KEY-1SAMPLE" {
		t.Fatalf("이전된 키 미수신: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(legacy, "backup.key")); !os.IsNotExist(err) {
		t.Error("레거시 잔존 — 이전 후 제거 계약 위반")
	}

	os.WriteFile(filepath.Join(legacy, "backup.key"), []byte("OLD-SHOULD-NOT-WIN"), 0o600)
	b2, _ := os.ReadFile(BackupKeyPath())
	if string(b2) != "AGE-SECRET-KEY-1SAMPLE" {
		t.Errorf("신규 우선 위반: %q", b2)
	}
}
