package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func extractImageLine(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "image:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
		}
	}
	t.Fatal("image 라인 없음")
	return ""
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", rel, err)
	}
	return string(b)
}

func TestPinnedImageLiteral(t *testing.T) {
	want := "dolthub/doltgresql@sha256:3a119bb6726a0be283cfda2a73fa9fbdff58df06c0b850f245cd1828f57281a6"
	if got := PinnedImage(); got != want {
		t.Fatalf("PinnedImage()=%q, want %q", got, want)
	}
}

func TestPinEquivalenceFourWays(t *testing.T) {
	pin := PinnedImage()
	ci := extractImageLine(t, readRepoFile(t, filepath.Join(".github", "workflows", "ci.yml")))
	testCompose := extractImageLine(t, readRepoFile(t, "docker-compose.test.yml"))
	embedded := extractImageLine(t, EmbeddedCompose())
	if ci != pin {
		t.Fatalf("ci.yml 이미지=%q, want PinnedImage()=%q", ci, pin)
	}
	if testCompose != pin {
		t.Fatalf("docker-compose.test.yml 이미지=%q, want PinnedImage()=%q", testCompose, pin)
	}
	if embedded != pin {
		t.Fatalf("내장 compose 이미지=%q, want PinnedImage()=%q", embedded, pin)
	}
}

func TestEmbeddedComposeTemplateShape(t *testing.T) {
	c := EmbeddedCompose()
	if !strings.HasPrefix(c, "name: flint") {
		t.Fatalf("내장 compose가 \"name: flint\"로 시작해야 한다: %q", firstLine(c))
	}
	if !strings.Contains(c, "${FLINT_PORT:-5434}") {
		t.Fatalf("호스트 포트 변수 ${FLINT_PORT:-5434} 없음")
	}
	if !strings.Contains(c, "image: "+PinnedImage()) {
		t.Fatalf("핀 이미지 없음: %q", PinnedImage())
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestEmbeddedSetupWritesTempCompose(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	withDockerPS(t, "", nil)
	var gotArgs []string
	var gotPath string
	prevUp, prevRun := composeUpFn, composeRunFn
	composeRunFn = func(ctx context.Context, args ...string) error {
		gotArgs = append([]string{}, args...)
		for i, a := range args {
			if a == "-f" && i+1 < len(args) {
				gotPath = args[i+1]
			}
		}
		info, err := os.Stat(gotPath)
		if err != nil {
			t.Fatalf("compose 파일 미기록: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("권한=%o, want 600", perm)
		}
		b, err := os.ReadFile(gotPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != EmbeddedCompose() {
			t.Fatalf("기록 내용이 내장 템플릿과 다름 (%d bytes vs %d bytes)", len(b), len(EmbeddedCompose()))
		}
		if filepath.Dir(gotPath) != dir {
			t.Fatalf("기록 위치=%q, want config dir %q", filepath.Dir(gotPath), dir)
		}
		return nil
	}
	t.Cleanup(func() { composeUpFn, composeRunFn = prevUp, prevRun })

	if err := composeUpFn(context.Background()); err != nil {
		t.Fatalf("composeUpFn 오류: %v", err)
	}
	want := []string{"-f", gotPath, "--project-name", "flint", "up", "-d", "--wait", "--wait-timeout", "60"}
	if len(gotArgs) != len(want) {
		t.Fatalf("인자=%v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("인자[%d]=%q, want %q (전체 %v)", i, gotArgs[i], want[i], gotArgs)
		}
	}
	if _, err := os.Stat(gotPath); !os.IsNotExist(err) {
		t.Fatalf("실행 후 임시 파일이 남아 있음: %v", err)
	}
}
