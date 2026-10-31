// profile_v0110_test.go — v0.11.0 R1 노출 프로파일: 판독 기본 normal·
// setup --exposure 기록·무효 값 exit 3.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExposureProfileDefaults(t *testing.T) {
	cases := map[string]func(string) (string, error){
		"미설정": func(string) (string, error) { return "", nil },
		"에러":  func(string) (string, error) { return "", errFake{} },
		"파손":  func(string) (string, error) { return "bogus", nil },
	}
	for name, kv := range cases {
		if got := ExposureProfile(kv); got != "normal" {
			t.Errorf("%s = %q, want normal", name, got)
		}
	}
	if got := ExposureProfile(func(string) (string, error) { return "minimal", nil }); got != "minimal" {
		t.Errorf("minimal = %q, want minimal", got)
	}
}

// TestSetupExposureWrites — minimal 기록(meta upsert)·한 줄 JSON 보고 exit 0.
func TestSetupExposureWrites(t *testing.T) {
	var gotKey, gotVal string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INTO meta") && len(args) == 2 {
			gotKey, gotVal = args[0].(string), args[1].(string)
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := runSetup(context.Background(), st, []string{"--exposure", "minimal"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	var rep struct {
		Status   string `json:"status"`
		Exposure string `json:"exposure"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("보고 JSON 파싱 실패: %v (%s)", err, out.String())
	}
	if rep.Status != "ok" || rep.Exposure != "minimal" {
		t.Errorf("보고={status:%s exposure:%s}, want {ok minimal}", rep.Status, rep.Exposure)
	}
	if gotKey != ExposureKey || gotVal != "minimal" {
		t.Errorf("기록=(%q,%q), want (%q,minimal)", gotKey, gotVal, ExposureKey)
	}
}

// TestSetupExposureInvalid — 무효 값·값 누락은 무효 인자 exit 3(전역 계약).
func TestSetupExposureInvalid(t *testing.T) {
	cases := [][]string{
		{"--exposure", "bogus"},
		{"--exposure"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		code := runSetup(context.Background(), &fakeStore{}, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Errorf("args=%v exit=%d, want 3; out=%s", args, code, out.String())
		}
	}
}
