package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNudgeShortDirectiveNormal(t *testing.T) {
	if nudgePendingLine != "flint: distillation pending — flint nudge" {
		t.Errorf("짧은 문구 = %q", nudgePendingLine)
	}
	if strings.Contains(nudgePendingLine, "flint write knowledge") {
		t.Error("명령문 잔존 — 최소화 위반")
	}
}

func TestNudgeMinimalAutoClears(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	mf := MarkerFile()
	if err := os.MkdirAll(filepath.Dir(mf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker("sess-1", "compact"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := runNudge(context.Background(), fakeProfileStore(t, "minimal"), nil, strings.NewReader(""), &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("출력 있음/exit=%d: %q", code, out.String())
	}
	if _, err := os.Stat(mf); !os.IsNotExist(err) {
		t.Error("마커 미소멸")
	}
}

func fakeProfileStore(t *testing.T, p string) Store {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		orig := exposureProfileFn
		t.Cleanup(func() { exposureProfileFn = orig })
		exposureProfileFn = func(func(string) (string, error)) string { return p }
	}
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if len(args) > 0 && args[0] == "exposure_profile" {
			return [][]any{{p}}, nil
		}
		return nil, nil
	}}
}
