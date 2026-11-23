package cli

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withPortBindFn(t *testing.T, fn func(int) bool) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("SUPERLITE_REAL=1 — fake bind seam only")
	}
	prev := portBindFn
	portBindFn = fn
	t.Cleanup(func() { portBindFn = prev })
}

func withPortExecCommand(t *testing.T, fn func(string, ...string) (string, error)) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("SUPERLITE_REAL=1 — fake exec seam only")
	}
	prev := portExecCommand
	portExecCommand = fn
	t.Cleanup(func() { portExecCommand = prev })
}

func TestPickPortReturnsDefaultWhenFree(t *testing.T) {
	withPortBindFn(t, func(int) bool { return true })
	got, err := PickPort(5434)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got != 5434 {
		t.Fatalf("PickPort(5434)=%d, want 5434", got)
	}
}

func TestPickPortScansUpwardSequentially(t *testing.T) {
	var probed []int
	withPortBindFn(t, func(p int) bool {
		probed = append(probed, p)
		return p != 5434 && p != 5435
	})
	got, err := PickPort(5434)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got != 5436 {
		t.Fatalf("PickPort(5434)=%d, want 5436", got)
	}
	if len(probed) != 3 {
		t.Fatalf("probed=%v, want [5434 5435 5436]", probed)
	}
}

func TestPickPortSkipsReserved5499(t *testing.T) {
	var probed []int
	withPortBindFn(t, func(p int) bool {
		probed = append(probed, p)
		return p != 5498
	})
	got, err := PickPort(5498)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got != 5500 {
		t.Fatalf("PickPort(5498)=%d, want 5500", got)
	}
	for _, p := range probed {
		if p == 5499 {
			t.Fatalf("reserved port 5499 probed: %v", probed)
		}
	}
}

func TestPickPortSkipsReserved5499AsDefault(t *testing.T) {
	withPortBindFn(t, func(int) bool { return true })
	got, err := PickPort(5499)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got != 5500 {
		t.Fatalf("PickPort(5499)=%d, want 5500", got)
	}
}

func TestPickPortExhaustedAtUpperBound(t *testing.T) {
	withPortBindFn(t, func(int) bool { return false })
	if _, err := PickPort(65535); err == nil {
		t.Fatal("모든 포트 점유 시 오류가 필요하다")
	}
}

func TestPickPortRejectsInvalidDefault(t *testing.T) {
	withPortBindFn(t, func(int) bool { return true })
	if _, err := PickPort(0); err == nil {
		t.Fatal("PickPort(0)은 오류여야 한다")
	}
	if _, err := PickPort(65536); err == nil {
		t.Fatal("PickPort(65536)은 오류여야 한다")
	}
}

func TestPickPortRealBindAvoidsHeldPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("루프백 리슨 불가: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if port == 5499 || port == 65535 {
		t.Skipf("부적합 임시 포트 %d", port)
	}
	got, err := PickPort(port)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got == port {
		t.Fatalf("점유 중인 %d 반환됨", port)
	}
	if got == 5499 {
		t.Fatal("예약 포트 5499 반환됨")
	}
}

func TestSetPortConfigRoundTripPreservesFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	seed := `{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPortConfig(path, 5435); err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("config 파싱 실패: %v", err)
	}
	if cfg["port"] != float64(5435) {
		t.Fatalf("port=%v, want 5435", cfg["port"])
	}
	if cfg["dsn"] != "postgresql://flint:flint-dev@localhost:5434/flint" {
		t.Fatalf("dsn=%v, want 보존", cfg["dsn"])
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%o, want 600", fi.Mode().Perm())
	}
	port, ok := ReadPortConfig(path)
	if !ok || port != 5435 {
		t.Fatalf("ReadPortConfig=%d,%v, want 5435,true", port, ok)
	}
}

func TestSetPortConfigCreatesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SetPortConfig(path, 5500); err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	port, ok := ReadPortConfig(path)
	if !ok || port != 5500 {
		t.Fatalf("ReadPortConfig=%d,%v, want 5500,true", port, ok)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%o, want 600", fi.Mode().Perm())
	}
}

func TestSetPortConfigOverwritesExistingPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SetPortConfig(path, 5434); err != nil {
		t.Fatal(err)
	}
	if err := SetPortConfig(path, 5436); err != nil {
		t.Fatal(err)
	}
	port, ok := ReadPortConfig(path)
	if !ok || port != 5436 {
		t.Fatalf("ReadPortConfig=%d,%v, want 5436,true", port, ok)
	}
}

func TestSetPortConfigRejectsInvalidPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SetPortConfig(path, 0); err == nil {
		t.Fatal("port=0은 오류여야 한다")
	}
	if err := SetPortConfig(path, 65536); err == nil {
		t.Fatal("port=65536은 오류여야 한다")
	}
}

func TestReadPortConfigMissingOrInvalid(t *testing.T) {
	if _, ok := ReadPortConfig(filepath.Join(t.TempDir(), "config.json")); ok {
		t.Fatal("없는 파일은 ok=false여야 한다")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"port":"nope"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPortConfig(path); ok {
		t.Fatal("문자열 port는 ok=false여야 한다")
	}
}

func TestDescribeOccupantReportsProcess(t *testing.T) {
	var lsofArgs []string
	withPortExecCommand(t, func(name string, args ...string) (string, error) {
		if name == "lsof" {
			lsofArgs = args
			return "COMMAND   PID  USER   FD   TYPE  DEVICE SIZE/OFF NODE NAME\n" +
				"postgres  1234 flint  5u   IPv4  0x1a      0t0  TCP 127.0.0.1:5434 (LISTEN)\n", nil
		}
		return "", exec.ErrNotFound
	})
	got := DescribeOccupant(5434)
	if !strings.Contains(got, "postgres") || !strings.Contains(got, "1234") {
		t.Fatalf("DescribeOccupant=%q, want postgres/1234 포함", got)
	}
	wantArg := "-iTCP:5434"
	found := false
	for _, a := range lsofArgs {
		if a == wantArg {
			found = true
		}
	}
	if !found {
		t.Fatalf("lsof 인자 %v에 %s 필요", lsofArgs, wantArg)
	}
}

func TestDescribeOccupantReportsDockerContainer(t *testing.T) {
	var dockerArgs []string
	withPortExecCommand(t, func(name string, args ...string) (string, error) {
		if name == "docker" {
			dockerArgs = args
			return "flint-db\n", nil
		}
		return "", errors.New("lsof unavailable")
	})
	got := DescribeOccupant(5434)
	if !strings.Contains(got, "flint-db") {
		t.Fatalf("DescribeOccupant=%q, want flint-db 포함", got)
	}
	wantArg := "publish=5434"
	found := false
	for _, a := range dockerArgs {
		if a == wantArg {
			found = true
		}
	}
	if !found {
		t.Fatalf("docker 인자 %v에 %s 필요", dockerArgs, wantArg)
	}
}

func TestDescribeOccupantCombinesSources(t *testing.T) {
	withPortExecCommand(t, func(name string, args ...string) (string, error) {
		if name == "lsof" {
			return "COMMAND  PID USER NODE NAME\ncom.docker 99 u 0t0 TCP *:5434\n", nil
		}
		return "flint-db\n", nil
	})
	got := DescribeOccupant(5434)
	if !strings.Contains(got, "com.docker") || !strings.Contains(got, "flint-db") {
		t.Fatalf("DescribeOccupant=%q, want 두 소스 병합", got)
	}
}

func TestDescribeOccupantOmitsWhenCommandsMissing(t *testing.T) {
	withPortExecCommand(t, func(string, ...string) (string, error) {
		return "", exec.ErrNotFound
	})
	if got := DescribeOccupant(5434); got != "" {
		t.Fatalf("DescribeOccupant=%q, want 빈 문자열", got)
	}
}

func TestDescribeOccupantRealNoPanic(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("SUPERLITE_REAL=1 전용")
	}
	_ = DescribeOccupant(5434)
}
