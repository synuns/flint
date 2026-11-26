package ops

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var _ dumpStore = (*dumpDelegFake)(nil)
var _ MaintStore = (*fakeMaintStore)(nil)
var _ workStateStore = (*fakeMaintStore)(nil)
var _ ConflictStore = (*fakeMaintStore)(nil)
var _ conflictStore = (*fakeMaintStore)(nil)
var _ metaDeleteStore = (*fakeStore)(nil)
var _ ReaffirmStore = (*reaffirmFakeStore)(nil)
var _ RecheckConditionsStore = (*recheckCondStore)(nil)
var _ RecheckSourceClearStore = (*recheckClearStore)(nil)

var fakeParityExcluded = map[string]bool{"SessionStartRecorder": true}

func TestFakeParityOps(t *testing.T) {

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

	discovered := map[string]bool{}
	for iface := range defined {
		if fakeParityExcluded[iface] {
			continue
		}
		if regexp.MustCompile(`\.\(` + iface + `\)`).MatchString(allSrc) {
			discovered[iface] = true
		}
	}

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

func fakeParityKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
