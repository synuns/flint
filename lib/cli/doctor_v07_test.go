// doctor_v07_test.go — T75: port 검사. composePsFn 시임으로
// JSONL·배열 양쪽 파싱·flint 실행/정지·타 컨테이너 점유·docker 부재 skip를
// 검증한다. 기존 검사 5종은 doctor_test.go 담당.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// withComposePs — composePsFn 시임 교체(테스트 후 원복).
func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := composePsFn
	composePsFn = fn
	t.Cleanup(func() { composePsFn = old })
}

// doctorPortCheck — portCheck 단독 출력(단일 객체)과 runDoctor --check port
// 출력({checks:[...]}) 양쪽을 파싱해 port 검사 행 반환.
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

// pass — flint 컨테이너 실행 중 + 5433 매핑(JSONL 연속 객체) → ok·exit 0.
// portCheck 자체는 한 줄 JSON을 out에 쓰고 0 반환.
func TestPortPass(t *testing.T) {
	line := `{"Name":"flint-doltgres-1","State":"running",` +
		`"Publishers":[{"URL":"","TargetPort":5432,"PublishedPort":5433,"Protocol":"tcp"}]}`
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(line + "\n" + `{"Name":"flint-cache-1","State":"running","Publishers":[]}` + "\n"), nil
	})

	var buf bytes.Buffer
	if code := portCheck(context.Background(), &buf); code != 0 {
		t.Fatalf("portCheck exit=%d, want 0 (출력 %q)", code, buf.String())
	}
	c := doctorPortCheck(t, buf.String())
	if c["status"] != "ok" {
		t.Errorf("status=%v, want ok: %q", c["status"], buf.String())
	}

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("runDoctor exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("runDoctor status=%v, want ok: %q", pc["status"], out)
	}
}

// foreign — 타 컨테이너(branding)가 5433 점유 → fail + 점유자 이름 +
// 포트 이동 fix. exit 1.
func TestPortForeign(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"branding-web-1","State":"running",` +
			`"Publishers":[{"TargetPort":5433,"PublishedPort":5433,"Protocol":"tcp"}]}`), nil
	})

	var buf bytes.Buffer
	if code := portCheck(context.Background(), &buf); code != 1 {
		t.Fatalf("portCheck exit=%d, want 1 (출력 %q)", code, buf.String())
	}
	c := doctorPortCheck(t, buf.String())
	if c["status"] != "fail" {
		t.Fatalf("status=%v, want fail: %q", c["status"], buf.String())
	}
	if !strings.Contains(fmt.Sprint(c["detail"]), "branding-web-1") {
		t.Errorf("점유자 이름 없음: %v", c["detail"])
	}
	fix, _ := c["fix"].(string)
	if !strings.Contains(fix, "FLINT_DSN") || !strings.Contains(fix, "compose") {
		t.Errorf("포트 이동 fix 문구 부족: %q", fix)
	}

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("runDoctor exit=%d, want 1 (출력 %q)", code, out)
	}
}

// docker missing — composePsFn 오류(docker 부재 포함) → skip·exit 영향 없음.
func TestPortDockerMissing(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("exec: docker: executable file not found")
	})

	var buf bytes.Buffer
	if code := portCheck(context.Background(), &buf); code != 0 {
		t.Fatalf("portCheck exit=%d, want 0 (출력 %q)", code, buf.String())
	}
	c := doctorPortCheck(t, buf.String())
	if c["status"] != "skip" {
		t.Errorf("status=%v, want skip: %q", c["status"], buf.String())
	}

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("skip가 exit에 영향: exit=%d (출력 %q)", code, out)
	}
	if strings.Contains(out, `"fail"`) {
		t.Errorf("skip인데 fail 존재: %q", out)
	}
}

// stopped — flint 컨테이너 있으나 정지(Exited, Publishers null) → fail +
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

// flint 없음 — 어떤 컨테이너도 flint가 아니면 같은 up -d fail.
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

// 배열 형식 — 구버전 docker compose ps --format json 배열 출력도 통과.
// JSONL pass·배열 pass 양쪽 계약.
func TestPortJSONArray(t *testing.T) {
	arr := `[{"Name":"flint-doltgres-1","State":"running",` +
		`"Publishers":[{"PublishedPort":5433}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(arr), nil })
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("배열 pass exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("배열 status=%v, want ok: %q", pc["status"], out)
	}

	// 배열 + 타 컨테이너 점유도 점유자 보고.
	foreign := `[{"Name":"flint-doltgres-1","State":"running","Publishers":[]},` +
		`{"Name":"branding-1","State":"running","Publishers":[{"PublishedPort":5433}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(foreign), nil })
	out2, code2 := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code2 != 1 {
		t.Fatalf("배열 점유 exit=%d, want 1 (출력 %q)", code2, out2)
	}
	if !strings.Contains(out2, "branding-1") {
		t.Errorf("배열 점유자 미보고: %q", out2)
	}
}

// DoctorChecks — "port" 포함(기존 5종 뒤 추가).
func TestDoctorChecksIncludesPort(t *testing.T) {
	got := DoctorChecks()
	if len(got) == 0 || got[len(got)-1] != "port" {
		t.Errorf("DoctorChecks 마지막에 port 없음: %v", got)
	}
}
