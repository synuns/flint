package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-0006 — restore 체크섬 가드(T47). 가드는 runRestore 1곳에만 존재한다.
// fake backup seam(backup_test.go) 재사용 — restoreFn 위임 여부로 가드 선행 검증.

// 불일치 — 한 줄 JSON 경고 + exit 1, restoreFn 미호출(임의 SQL 실행 차단).
func TestRestoreChecksumMismatchExit1(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, []byte("BEGIN;\nCOMMIT;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".sha256", []byte("0000000000000000000000000000000000000000000000000000000000000000  dump.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := mustRestore(t, []string{"--file", path})
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	m := decode(t, out)
	if m["error"] != "checksum_mismatch" {
		t.Fatalf("error=%v, want checksum_mismatch", m["error"])
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "체크섬 위조") {
		t.Fatalf("message=%v, 위조 감지 안내 누락", m["message"])
	}
	if f.gotRestore != "" {
		t.Fatalf("불일치 시 restore 실행 금지: %q", f.gotRestore)
	}
}

// Review Focus(file): 사이드카 누락 — 경고(warn) + 진행, exit 0.
// v0.20.6(S4) 재작성 — 이 테스트는 '무검증 재생 진행'을 계약으로
// 문서화했으나 보안 스윕이 사이드카 부재 재생을 차단 계약으로 바꿨다
// (백업=임의 SQL 매체, ADR-0006 즉시 중단 취지).
func TestRestoreMissingSidecarBlocked(t *testing.T) {
	f := &fakeBackup{}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(path, []byte("BEGIN;\nCOMMIT;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := mustRestore(t, []string{"--file", path})
	if code != 1 {
		t.Fatalf("exit=%d, want 1(무검증 재생 차단); out=%s", code, out)
	}
	if !strings.Contains(out, "sidecar") {
		t.Fatalf("차단 사유에 sidecar 없음: %s", out)
	}
	if f.gotRestore != "" {
		t.Fatalf("차단됐는데 재생 실행: %q", f.gotRestore)
	}
}

// backup --out 이 원본+.sha256 사이드카를 함께 생성하는지(ADR-0006).
func TestBackupWritesSidecar(t *testing.T) {
	f := &fakeBackup{dumpText: "-- flint backup probe\nBEGIN;\nCOMMIT;\n"}
	withBackupFake(t, f)
	path := filepath.Join(t.TempDir(), "dump.sql")
	out, code := mustBackup(t, []string{"--out", path})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	side, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatalf("사이드카 미생성: %v", err)
	}
	fields := strings.Fields(string(side))
	if len(fields) < 2 || fields[1] != "dump.sql" {
		t.Fatalf("sidecar=%q, want sha256sum 형식", side)
	}
	if m := decode(t, out); m["sha256"] != fields[0] {
		t.Fatalf("출력 sha256=%v, sidecar=%s", m["sha256"], fields[0])
	}
}
