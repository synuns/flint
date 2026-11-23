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
