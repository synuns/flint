// doctor_v07_test.go — the port check. Verifies both JSONL and array parsing, flint
// running/stopped, foreign-container occupation, and docker-absent skip via the composePsFn
// seam. The existing 5 checks are covered by doctor_test.go.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// withComposePs — swaps the composePsFn seam (restored after the test).
func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := composePsFn
	composePsFn = fn
	t.Cleanup(func() { composePsFn = old })
}

// doctorPortCheck — parses both the standalone portCheck output (a single object) and the runDoctor --check port
// output ({checks:[...]}) and returns the port check row.
func doctorPortCheck(t *testing.T, out string) map[string]any {
	t.Helper()
	var m struct {
		Checks []map[string]any `json:"checks"`
	}
	body := strings.TrimSpace(out)
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if len(m.Checks) == 1 {
		return m.Checks[0]
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(body), &single); err != nil || single["name"] != "port" {
		t.Fatalf("port 검사 행 아님: %q", out)
	}
	return single
}

// pass — the flint container is running + 5434 mapped (consecutive JSONL objects) -> ok, exit 0.
// portCheck itself writes one-line JSON to out and returns 0.
func TestPortPass(t *testing.T) {
	line := `{"Name":"flint-doltgres-1","State":"running",` +
		`"Publishers":[{"URL":"","TargetPort":5432,"PublishedPort":5434,"Protocol":"tcp"}]}`
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(line + "\n" + `{"Name":"flint-cache-1","State":"running","Publishers":[]}` + "\n"), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("runDoctor exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("runDoctor status=%v, want ok: %q", pc["status"], out)
	}
}

// foreign — another container (branding) holds 5434 -> fail + the occupant's name +
// a port-move fix. exit 1.
func TestPortForeign(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"branding-web-1","State":"running",` +
			`"Publishers":[{"TargetPort":5434,"PublishedPort":5434,"Protocol":"tcp"}]}`), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("runDoctor exit=%d, want 1 (출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] != "fail" {
		t.Fatalf("status=%v, want fail: %q", c["status"], out)
	}
	if !strings.Contains(fmt.Sprint(c["detail"]), "branding-web-1") {
		t.Errorf("점유자 이름 없음: %v", c["detail"])
	}
	fix, _ := c["fix"].(string)
	if !strings.Contains(fix, "FLINT_DSN") || !strings.Contains(fix, "compose") {
		t.Errorf("포트 이동 fix 문구 부족: %q", fix)
	}
}

// docker missing — a composePsFn error (including docker absent) -> skip, no exit impact.
func TestPortDockerMissing(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("exec: docker: executable file not found")
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("skip가 exit에 영향: exit=%d (출력 %q)", code, out)
	}
	if c := doctorPortCheck(t, out); c["status"] != "skip" {
		t.Errorf("status=%v, want skip: %q", c["status"], out)
	}
	if strings.Contains(out, `"fail"`) {
		t.Errorf("skip인데 fail 존재: %q", out)
	}
}

// stopped — the flint container exists but is stopped (Exited, Publishers null) -> fail +
// "docker compose up -d" fix.
func TestPortStopped(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"flint-doltgres-1","State":"exited","Publishers":null}`), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] != "fail" {
		t.Fatalf("status=%v, want fail: %q", c["status"], out)
	}
	if fix, _ := c["fix"].(string); !strings.Contains(fix, "docker compose up -d") {
		t.Errorf("up -d fix 문구 없음: %q", fix)
	}
}

// no flint — if no container is flint, the same up -d fail.
func TestPortNoFlintContainer(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"other-app-1","State":"running","Publishers":[]}`), nil
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "docker compose up -d") {
		t.Errorf("up -d 안내 없음: %q", out)
	}
}

// array format — the older docker compose ps --format json array output also passes.
// Both contracts: JSONL pass and array pass.
func TestPortJSONArray(t *testing.T) {
	arr := `[{"Name":"flint-doltgres-1","State":"running",` +
		`"Publishers":[{"PublishedPort":5434}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(arr), nil })
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("배열 pass exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("배열 status=%v, want ok: %q", pc["status"], out)
	}

	// array + foreign-container occupation also reports the occupant.
	foreign := `[{"Name":"flint-doltgres-1","State":"running","Publishers":[]},` +
		`{"Name":"branding-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(foreign), nil })
	out2, code2 := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code2 != 1 {
		t.Fatalf("배열 점유 exit=%d, want 1 (출력 %q)", code2, out2)
	}
	if !strings.Contains(out2, "branding-1") {
		t.Errorf("배열 점유자 미보고: %q", out2)
	}
}

// DoctorChecks — includes "port" (appended after the existing 5).
func TestDoctorChecksIncludesPort(t *testing.T) {
	got := DoctorChecks()
	if len(got) == 0 || got[len(got)-1] != "port" {
		t.Errorf("DoctorChecks 마지막에 port 없음: %v", got)
	}
}

// prefix — a container whose name contains flint but not as a prefix ('conflint') is not a
// flint container. Even publishing 5434, it is an occupant fail, not ok.
func TestPortFlintSubstringNotPrefix(t *testing.T) {
	if isFlintContainer("conflint-web-1") {
		t.Errorf("isFlintContainer(conflint-web-1) = true, want false(접두 아님)")
	}
	if !isFlintContainer("flint-doltgres-1") {
		t.Errorf("isFlintContainer(flint-doltgres-1) = false, want true")
	}

	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"conflint-web-1","State":"running",` +
			`"Publishers":[{"PublishedPort":5434}]}`), nil
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("conflint 점유 exit=%d, want 1 (출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] != "fail" {
		t.Fatalf("status=%v, want fail(오탐 아님 — 점유자 보고): %q", c["status"], out)
	}
	if !strings.Contains(fmt.Sprint(c["detail"]), "conflint-web-1") {
		t.Errorf("점유자 이름 없음: %v", c["detail"])
	}
}

// timeout — when docker does not answer (context wait), a 2-second cap falls through to skip.
func TestPortDockerHangTimeout(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done() // simulate an indefinite wait where docker never answers
		return nil, ctx.Err()
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("timeout skip가 exit에 영향: exit=%d (출력 %q)", code, out)
	}
	if c := doctorPortCheck(t, out); c["status"] != "skip" {
		t.Errorf("status=%v, want skip(타임아웃은 오류 경로로): %q", c["status"], out)
	}
}
