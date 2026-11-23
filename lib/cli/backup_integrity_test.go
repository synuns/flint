package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if msg, _ := m["message"].(string); !strings.Contains(msg, "checksum tampering detected") {
		t.Fatalf("message=%v, 위조 감지 안내 누락", m["message"])
	}
	if f.gotRestore != "" {
		t.Fatalf("불일치 시 restore 실행 금지: %q", f.gotRestore)
	}
}

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
