// doctor_check_registry_test.go — verifies that the doctor check registry
// is the single source of the full run chain, the --check allow list, and --list-checks.
// The 14 harness-* kinds and skill-file rely on dynamic detection in a separate chain, outside the registry (explicitly excluded).
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// doctorRegistryNames — the whole registry's name order (core 6 + tail 11 = 17).
func doctorRegistryNames() []string {
	specs := doctorChecks()
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	return names
}

// doctorRegistryChainedNames — only the names of a given chain ("core"|"tail") from the registry.
func doctorRegistryChainedNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

// registry — the name set equals the full run chain's assembled set (core 6 + tail 11).
// The harness and skill families are dynamic-detection (file-existence) separate chains
// whose output varies by environment, so they are excluded before comparing.
func TestDoctorRegistryCoversChain(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir()) // skill source undetected — skill-file omission guaranteed
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
			continue // a separate chain outside the registry (dynamic detection)
		}
		chain[c.Name] = true
	}
	specs := doctorChecks()
	if len(specs) != 20 {
		t.Errorf("등록표 %d종, want 20종(코어6+tail14)", len(specs))
	}
	if got := doctorRegistryChainedNames("core"); len(got) != 6 {
		t.Errorf("코어 체인 %v, want 6종", got)
	}
	if got := doctorRegistryChainedNames("tail"); len(got) != 14 {
		t.Errorf("tail 체인 %v, want 14종", got)
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

// --list-checks — outputs all 17 registry kinds (preventing the regression of listing only the old 6).
// The core 6 keep the existing DoctorChecks order (hook-file... port) at the front.
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
	// the old-6 regression — the DoctorChecks order stays at the front as-is.
	core := DoctorChecks()
	for i, name := range core {
		if m.Checks[i] != name {
			t.Errorf("선두 6종[%d]=%q, want %q(코어 순서 회귀)", i, m.Checks[i], name)
		}
	}
}

// onStoreNil — the registry attributes match the actual st==nil behavior (core 4
// fail, tail warn). Checks not using st (hook-file, port, update) are codified as
// attribute "" and excluded from the verdict.
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
		c := runCheckSpec(context.Background(), nil, doctorThresholds(context.Background(), nil), s)
		if c.Status != s.OnStoreNil {
			t.Errorf("%s: st==nil → %q, 등록표 속성 %q와 불일치", s.Name, c.Status, s.OnStoreNil)
		}
	}
	if fail != 4 {
		t.Errorf("onStoreNil fail %d종, want 4종(db·schema·integrity·last-seen)", fail)
	}
}
