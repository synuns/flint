package docs

import (
	"os"
	"strings"
	"testing"
)

func TestMakefileDerivesVersion(t *testing.T) {
	b, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "git describe --tags") {
		t.Error("Makefile VERSION 태그 파생 누락")
	}
}

// TestMakefileReleaseTagNoDoubleV — 파생 VERSION(v0.9.2 등 v 접두 포함)이
// release 안내에서 v 접두 중복(git tag vv…)으로 이어지지 않는지. 안내는
// VERSION_NU(patsubst v%,%)를 거쳐 조립돼야 한다.
func TestMakefileReleaseTagNoDoubleV(t *testing.T) {
	b, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "$(patsubst v%,%,$(VERSION))") {
		t.Error("VERSION v 접두 제거(patsubst) 누락 — release 안내 git tag vv… 중복 위험")
	}
	if strings.Contains(s, "git tag v$(VERSION)") {
		t.Error("release 안내가 v$(VERSION)을 그대로 사용 — 파생 태그명에서 v 접두 중복")
	}
}
