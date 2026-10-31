// doctor_v0102_test.go — T3: doctor --check backup-age 단독 실행(v0.10.2
// R3). 이름 공간 밖 별도 체인의 서브셋 특수경로 — DoctorChecks() 6종·port
// 마지막 계약은 불변(룰링). 무효 검사명 exit 3·안내 문구 불변.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDoctorSubsetBackupAge(t *testing.T) {
	fresh := time.Now().UTC().Format(time.RFC3339)
	var out bytes.Buffer
	code := runDoctor(context.Background(), asofFakeOrReal(t, &fakeStore{queryFn: backupAgeQueryFn(fresh, false)}), []string{"--check", "backup-age"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d (출력 %s)", code, out.String())
	}
	var d struct {
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Checks) != 1 || d.Checks[0].Name != "backup-age" {
		t.Fatalf("서브셋 결과 = %+v, want backup-age 1건", d.Checks)
	}
}

func TestDoctorSubsetUnknownStill3(t *testing.T) {
	var out bytes.Buffer
	code := runDoctor(context.Background(), nil, []string{"--check", "no-such"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("무효 이름 exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "unknown check") {
		t.Errorf("안내 문구 누락: %q", out.String())
	}
}
