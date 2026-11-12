// fake_parity_test.go — v0.43.0 T2: ops capability seam 5종 fake 동질 가드.
// 패키지의 runtime 타입 단언 seam(st.(capability))이 소비되는 유닛
// 테스트에서 열화 폴백 분기가 아니라 위임 경로만 검증하게 한다 —
// 모든 capability에 대해 컴파일 시점 단언(var _ X = (*fake)(nil))을
// 두고, TestFakeParityOps가 발견(소스의 단언 seam 사용)과 단언 목록의
// 동질을 검증한다(2026-11-11 리뷰 병인 ③).
package ops

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 컴파일 시점 fake 단언 — ops capability 5종 전수(TestFakeParityOps가
// 발견 목록과의 동질을 검증). fakeMaintStore은 fakeStore 임베딩으로
// MaintStore·ConflictStore·conflictStore(구조적 동일)를 모두 충족하고,
// dumpStore는 위임 경로 fake(supply_test.go), metaDeleteStore는 패키지
// 공통 fakeStore의 canned 구현(doctor_test.go)으로 충족한다.
var _ dumpStore = (*dumpDelegFake)(nil)
var _ MaintStore = (*fakeMaintStore)(nil)
var _ ConflictStore = (*fakeMaintStore)(nil)
var _ conflictStore = (*fakeMaintStore)(nil)
var _ metaDeleteStore = (*fakeStore)(nil)

// fakeParityExcluded — 발견 목록에서 제외하는 선택 훅 seam.
// SessionStartRecorder는 미구현 fake가 조용히 건너뛰는 설계(세션 지표만
// 0 강등 — sessionhook.go §13)로 열화 폴백 병인 ③ 대상이 아니다.
var fakeParityExcluded = map[string]bool{"SessionStartRecorder": true}

// TestFakeParityOps — Review Focus: 발견 목록(비테스트 소스에서
// `.(X)` runtime 단언으로 소비되는 인터페이스)과 이 파일의 var _ 단언
// 목록이 동질해야 한다. 새 capability seam이 추가되면 단언 없이는
// 이 테스트가 실패한다(컴파일 단언 전수 강제).
func TestFakeParityOps(t *testing.T) {
	// ── 발견: 비테스트 소스의 인터페이스 정의 중 타입 단언으로 쓰이는 것 ──
	ifaceRe := regexp.MustCompile(`(?m)^type (\w+) interface\b`)
	discovered := map[string]bool{}
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
		for _, m := range ifaceRe.FindAllStringSubmatch(src, -1) {
			iface := m[1]
			if fakeParityExcluded[iface] {
				continue
			}
			if regexp.MustCompile(`\.\(` + iface + `\)`).MatchString(src) {
				discovered[iface] = true
			}
		}
	}

	// ── 단언: 이 파일의 var _ 목록 ──
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

// fakeParityKeys — 맵 키 정렬(비교 안정화).
func fakeParityKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
