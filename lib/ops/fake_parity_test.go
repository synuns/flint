// fake_parity_test.go — a fake-parity guard for the 5 ops capability seams.
// The package's runtime type-assertion seams (st.(capability)) let the unit tests that
// consume them verify only the delegation path, not the degraded fallback — a
// compile-time assertion (var _ X = (*fake)(nil)) exists for every capability, and
// TestFakeParityOps verifies the homogeneity between the discovered set (assertion-seam
// usages in the source) and the assertion list.
package ops

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// compile-time fake assertions — all 5 ops capabilities (TestFakeParityOps verifies
// homogeneity with the discovered list). fakeMaintStore, via the fakeStore embedding,
// satisfies MaintStore, ConflictStore, and conflictStore (structurally identical);
// dumpStore is satisfied by the delegation-path fake (supply_test.go), and
// metaDeleteStore by the package-common fakeStore's canned implementation (doctor_test.go).
var _ dumpStore = (*dumpDelegFake)(nil)
var _ MaintStore = (*fakeMaintStore)(nil)
var _ ConflictStore = (*fakeMaintStore)(nil)
var _ conflictStore = (*fakeMaintStore)(nil)
var _ metaDeleteStore = (*fakeStore)(nil)

// fakeParityExcluded — the optional hook seams excluded from the discovered list.
// SessionStartRecorder is designed so an unimplemented fake silently skips (only the
// session metrics demote to 0 — sessionhook.go §13); it is not a degraded-fallback target.
var fakeParityExcluded = map[string]bool{"SessionStartRecorder": true}

// TestFakeParityOps — the discovered list (interfaces consumed via runtime
// `.(X)` assertions in non-test sources) and this file's var _ assertion list must be
// homogeneous. When a new capability seam is added, this test fails without an
// assertion (forcing compile-time assertions for all).
func TestFakeParityOps(t *testing.T) {
	// ── discovery: among the interface definitions in non-test sources, those used in type assertions ──
	ifaceRe := regexp.MustCompile(`(?m)^type (\w+) interface\b`)
	allSrc, defined := "", map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ops 디렉터리 조회: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s 읽기: %v", name, err)
		}
		src := string(b)
		allSrc += src + "\n"
		for _, m := range ifaceRe.FindAllStringSubmatch(src, -1) {
			defined[m[1]] = true
		}
	}
	// assertion usages (`.(X)`) are searched across all sources regardless of the
	// definition file — checking only the definition file would let a new seam consumed
	// elsewhere slip through (demonstrated with a virtual-interface probe).
	discovered := map[string]bool{}
	for iface := range defined {
		if fakeParityExcluded[iface] {
			continue
		}
		if regexp.MustCompile(`\.\(` + iface + `\)`).MatchString(allSrc) {
			discovered[iface] = true
		}
	}

	// ── assertions: this file's var _ list ──
	self, err := os.ReadFile("fake_parity_test.go")
	if err != nil {
		t.Fatalf("자기 소스 읽기: %v", err)
	}
	assertRe := regexp.MustCompile(`(?m)^var _ (\w+) = `)
	asserted := map[string]bool{}
	for _, m := range assertRe.FindAllStringSubmatch(string(self), -1) {
		asserted[m[1]] = true
	}

	dList, aList := fakeParityKeys(discovered), fakeParityKeys(asserted)
	if strings.Join(dList, ",") != strings.Join(aList, ",") {
		t.Errorf("capability 발견/단언 불일치\n발견: %v\n단언: %v", dList, aList)
	}
	if len(aList) == 0 {
		t.Fatal("단언 목록 비어 있음 — var _ 단언 전수 필요")
	}
}

// fakeParityKeys — sorted map keys (stabilizing the comparison).
func fakeParityKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
