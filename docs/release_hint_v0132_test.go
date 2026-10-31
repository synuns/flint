// release_hint_v0132_test.go — v0.13.2 심층 리뷰 회귀(N12): make release·
// ci.yml 릴리스 안내는 기존 태그이 아니라 다음 패치 버전(최신 태그 +1)을
// 지시해야 한다. 기존 태그 지시는 태그 충돌로 안내대로 실행해도 항상 실패.
package docs

import (
	"os"
	"strings"
	"testing"
)

// TestReleaseHintUsesNextTag — Makefile release 타깃 안내가 NEXT_TAG
// (git describe 기반 최신 태그 패치 +1 파생)를 쓰는지, 기존 태그 지시
// (git tag v$(VERSION_NU))가 빠졌는지.
func TestReleaseHintUsesNextTag(t *testing.T) {
	b, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	mk := string(b)
	if !strings.Contains(mk, "git tag $(NEXT_TAG)") {
		t.Errorf("release 안내가 NEXT_TAG 미사용: git tag $(NEXT_TAG) 없음")
	}
	if strings.Contains(mk, "git tag v$(VERSION_NU)") {
		t.Errorf("release 안내가 기존 태그(v$(VERSION_NU)) 지시 — 항상 실패")
	}
	if !strings.Contains(mk, `awk -F. '{print $$1"."$$2"."($$3+1)}'`) {
		t.Errorf("NEXT_TAG 파생에 최신 태그 패치 +1 계산 없음")
	}
}

// TestCiReleaseNoticeComputesNext — ci.yml 릴리스 안내도 다음 패치
// 버전을 동적으로 계산한다(정적 vX.Y.Z 플레이스홀더 폐지).
func TestCiReleaseNoticeComputesNext(t *testing.T) {
	b, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	yml := string(b)
	if strings.Contains(yml, "git tag vX.Y.Z") {
		t.Errorf("ci.yml 안내가 정적 플레이스홀더 vX.Y.Z 사용")
	}
	if !strings.Contains(yml, `awk -F. '{print $1"."$2"."($3+1)}'`) {
		t.Errorf("ci.yml 안내에 다음 패치 버전 계산 없음")
	}
}
