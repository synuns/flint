// nudge_v0110_test.go — v0.11.0 R3: normal 넛지 짧은 문구(nudgeStopShort)·
// minimal 자동 마커 종결(출력 0바이트) 검증.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNudgeShortDirectiveNormal(t *testing.T) {
	if nudgeStopShort != "flint: 증류 대기 — flint nudge" {
		t.Errorf("짧은 문구 = %q", nudgeStopShort)
	}
	if strings.Contains(nudgeStopShort, "flint write knowledge") {
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
	code := runNudge(nil, fakeProfileStore("minimal"), nil, strings.NewReader(""), &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("출력 있음/exit=%d: %q", code, out.String())
	}
	if _, err := os.Stat(mf); !os.IsNotExist(err) {
		t.Error("마커 미소멸")
	}
}

// fakeProfileStore — meta exposure_profile 반환 페이크. 기본은 ExposureProfile
// seam을 페이크로 교체(T1 미구현 패닉 회피), SUPERLITE_REAL=1이면 실구현이
// 스토어 질의(meta exposure_profile=p)를 읽도록 놔둔다.
func fakeProfileStore(p string) Store {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		exposureProfileFn = func(func(string) (string, error)) string { return p }
	}
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if len(args) > 0 && args[0] == "exposure_profile" {
			return [][]any{{p}}, nil
		}
		return nil, nil
	}}
}
