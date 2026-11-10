// doctor_check_registry_test.go — v0.34.3(R11): doctor 체크 등록표가
// 전체 실행 체인·--check 허용 목록·--list-checks의 단일 소스인지 검증.
// harness-* 14종·skill-file은 동적 감지 별도 체인이라 등록표 밖(제외 명시).
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// doctorRegistryNames — 등록표 전체 이름 순서(코어6+tail10 = 16종).
func doctorRegistryNames() []string {
	specs := doctorChecks()
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	return names
}

// doctorRegistryChainedNames — 등록표에서 특정 체인("core"|"tail") 이름만.
func doctorRegistryChainedNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

// 등록표 — 이름 집합이 전체 실행 체인 조립 집합과 같다(코어6+tail10).
// 하네스·스킬 계열은 동적 감지(파일 존재) 별도 체인이라 출력이 환경에
// 따라 변하므로 비교 전 제외한다.
func TestDoctorRegistryCoversChain(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir()) // 스킬 소스 미탐지 — skill-file 생략 확정
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	var m struct {
		Status string `json:"status"`
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님(exit %d): %s", code, out)
	}
	chain := map[string]bool{}
	for _, c := range m.Checks {
		if c.Name == "skill-file" || strings.HasPrefix(c.Name, "harness-") {
			continue // 등록표 밖 별도 체인(동적 감지)
		}
		chain[c.Name] = true
	}
	specs := doctorChecks()
	if len(specs) != 16 {
		t.Errorf("등록표 %d종, want 16종(코어6+tail10)", len(specs))
	}
	if got := doctorRegistryChainedNames("core"); len(got) != 6 {
		t.Errorf("코어 체인 %v, want 6종", got)
	}
	if got := doctorRegistryChainedNames("tail"); len(got) != 10 {
		t.Errorf("tail 체인 %v, want 10종", got)
	}
	for _, name := range doctorRegistryNames() {
		if !chain[name] {
			t.Errorf("전체 체인에 등록표 항목 %s 부재 — 등록표와 실행 체인 불일치", name)
		}
		delete(chain, name)
	}
	for name := range chain {
		t.Errorf("전체 체인의 %s가 등록표에 없음(하니스·스킬 밖 사유 불명)", name)
	}
}

// --list-checks — 등록표 16종 전량 출력(구 6종만 내던 회귀 방지).
// 코어 6종은 기존 DoctorChecks 순서(hook-file…port)를 선두에 유지한다.
func TestDoctorListChecksShowsAll(t *testing.T) {
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f, "--list-checks")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	var m struct {
		Checks []string `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	want := doctorRegistryNames()
	if len(m.Checks) != len(want) {
		t.Fatalf("checks %d종, want %d종: %v", len(m.Checks), len(want), m.Checks)
	}
	for i, name := range want {
		if m.Checks[i] != name {
			t.Errorf("checks[%d]=%q, want %q(등록표 순서)", i, m.Checks[i], name)
		}
	}
	// 구 6종 회귀 — DoctorChecks 순서가 선두에 그대로 남는다.
	core := DoctorChecks()
	for i, name := range core {
		if m.Checks[i] != name {
			t.Errorf("선두 6종[%d]=%q, want %q(코어 순서 회귀)", i, m.Checks[i], name)
		}
	}
}

// onStoreNil — 등록표 속성이 실제 st==nil 행동(코어 4종 fail·tail warn)
// 과 동일하다. st를 쓰지 않는 검사(hook-file·port·update)는 속성 ""로
// 명문화해 판정 대상에서 제외한다.
func TestDoctorStoreNilSeverity(t *testing.T) {
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	fail, warn := 0, 0
	for _, s := range doctorChecks() {
		switch s.OnStoreNil {
		case "":
			continue
		case "fail":
			fail++
		case "warn":
			warn++
		default:
			t.Errorf("%s: onStoreNil %q — fail|warn|빈값만 허용", s.Name, s.OnStoreNil)
		}
		c := s.Fn(context.Background(), nil)
		if c.Status != s.OnStoreNil {
			t.Errorf("%s: st==nil → %q, 등록표 속성 %q와 불일치", s.Name, c.Status, s.OnStoreNil)
		}
	}
	if fail != 4 {
		t.Errorf("onStoreNil fail %d종, want 4종(db·schema·integrity·last-seen)", fail)
	}
}
