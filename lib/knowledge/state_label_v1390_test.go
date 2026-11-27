package knowledge

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func setStateLabelFile(t *testing.T, content string) {
	t.Helper()
	saved := wire.ConfigDir
	dir := t.TempDir()
	wire.ConfigDir = func() string { return dir }
	t.Cleanup(func() { wire.ConfigDir = saved })
	if err := os.WriteFile(wire.SessionLabelFile("v1390-fallback"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStateOpenFallbackLabelValidated(t *testing.T) {
	f := &stateStoreFake{}
	ctx := context.Background()

	setStateLabelFile(t, "bad\x00label\n")
	var out bytes.Buffer
	if code := RunState(ctx, f, []string{"open", "--task", "v1390 폴백 제어문자 데모"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("제어문자 폴백 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.openCalls) != 0 {
		t.Errorf("제어문자 라벨로 OpenWorkState 호출됨: %v", f.openCalls)
	}

	setStateLabelFile(t, strings.Repeat("라", 201)+"\n")
	out.Reset()
	if code := RunState(ctx, f, []string{"open", "--task", "v1390 폴백 초장 라벨 데모"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("초장 폴백 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.openCalls) != 0 {
		t.Errorf("초장 라벨로 OpenWorkState 호출됨: %v", f.openCalls)
	}

	setStateLabelFile(t, "v1390-ok-label\n")
	out.Reset()
	if code := RunState(ctx, f, []string{"open", "--task", "v1390 폴백 정상 라벨 데모"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("정상 폴백 exit=%d, want 0: %s", code, out.String())
	}
	if got, _ := f.openCalls[0]["session_label"].(string); got != "v1390-ok-label" {
		t.Errorf("session_label = %q, want v1390-ok-label(정상 폴백 통과)", got)
	}
}

func TestValidateSessionLabelControlAndLength(t *testing.T) {
	for _, bad := range []string{"ctl\x00x", "esc\x1b[1m", "tab\tlabel", strings.Repeat("s", 201)} {
		if err := ValidateSessionLabel(true, bad); err == nil {
			t.Errorf("ValidateSessionLabel(true, %.20q) = nil, want error", bad)
		}
	}
	if err := ValidateSessionLabel(true, strings.Repeat("s", 200)); err != nil {
		t.Errorf("200자 경계 = %v, want nil", err)
	}
}
