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
	if err := os.WriteFile(path, []byte("BEGIN;\nCOMMIT;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// v0.20.6 S4: 사이드카 필수 — 정상 경로는 무결 검증 통과 분기.
	// 체크섬은 VerifyFile과 동일 알고리즘(sha256 hex)으로 계산해 기록.
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

// TestBackupEncryptWritesAgeFile — v0.28.0(H②): --encrypt은 age 암호문
// 파일+사이드카(암호문 기준)·출력에 encrypted:true.
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
	// 사이드카는 암호문 체크섬.
	sum, verr := backup.VerifyFile(out)
	if verr != nil {
		t.Fatalf("사이드카 검증: %v", verr)
	}
	h := sha256.Sum256(b)
	if sum != hex.EncodeToString(h[:]) {
		t.Error("사이드카가 암호문 기준 아님")
	}
}

// TestBackupMaskDump — v0.28.0: --mask은 마커 선두·패턴 치환·사이드카
// 미부여(비복원 아티팩트)·restore 거부.
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
	// restore 거부(사이드카 부재 가드가 먼저 — 마커 가드와 이중 방어).
	rgot, rcode := mustRestore(t, []string{"--file", out})
	if rcode != 1 || !strings.Contains(rgot, "sidecar") {
		t.Fatalf("마스킹 restore = exit %d %s, want 사이드카 차단", rcode, rgot)
	}
}

// TestRestoreDecryptsEncrypted — v0.28.0: 암호화 백업 restore 복호 경로
// (restoreFn이 평문을 받는다) + 키 없으면 명확 에러.
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

	// 키 있는 config — 복호되어 restoreFn 도달.
	t.Setenv("FLINT_CONFIG_DIR", cfg)
	fb := &fakeBackup{}
	withBackupFake(t, fb)
	got, code := mustRestore(t, []string{"--file", f})
	if code != 0 || !strings.Contains(got, `"status":"restored"`) {
		t.Fatalf("복호 restore = exit %d %s", code, got)
	}
	if !strings.Contains(fb.gotRestore, "복호 검증 본문") {
		t.Errorf("restoreFn 평문 미수신: %q", fb.gotRestore)
	}

	// 키 없는 config — 명확 에러(암호문 임의 재생 차단).
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	withBackupFake(t, &fakeBackup{})
	got2, code2 := mustRestore(t, []string{"--file", f})
	if code2 != 1 || !strings.Contains(got2, "키 파일 필요") {
		t.Fatalf("키 없음 = exit %d %s", code2, got2)
	}
}

// TestOutInsideGitRepo — v0.28.2: --out의 git 저장소 내부 판정(.git
// 디렉터리·하위 디렉터리 탐색).
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

// TestFlintHomeOverrideAndMigration — v0.28.3: FLINT_HOME 오버라이드와
// 레거시(configDir) 아티팩트 자동 이전 — 백업 키가 설정 공간에서 인프라
// 데이터 공간(~/.flint)으로 옮겨질 때 기존 키가 따라온다.
func TestFlintHomeOverrideAndMigration(t *testing.T) {
	legacy := t.TempDir()
	home := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", legacy) // flintHome 폴백 겸 레거시 원본
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
	// 신규가 이미 있으면 레거시를 덮어쓰지 않는다(both → new wins).
	os.WriteFile(filepath.Join(legacy, "backup.key"), []byte("OLD-SHOULD-NOT-WIN"), 0o600)
	b2, _ := os.ReadFile(BackupKeyPath())
	if string(b2) != "AGE-SECRET-KEY-1SAMPLE" {
		t.Errorf("신규 우선 위반: %q", b2)
	}
}
