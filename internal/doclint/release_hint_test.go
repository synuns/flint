package doclint

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseHintUsesNextTag(t *testing.T) {
	b, err := os.ReadFile(docPath("../Makefile"))
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
