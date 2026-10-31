package docs

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestReadmeBadgeIsLatest(t *testing.T) {
	re := regexp.MustCompile(`release-v[0-9]+\.[0-9]+\.[0-9]+`)
	var seen string
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindString(string(b))
		if m == "" {
			t.Errorf("%s release-v 배지 없음", p)
		}
		if seen != "" && m != seen {
			t.Errorf("EN/KO 배지 불일치: %s vs %s", seen, m)
		}
		seen = m
	}
}

// TestReadmeBadgeMatchesLatestTag — 배지 문자열이 git 최신 태그와 일치
// ("release-"+tag). 태그 부재 환경(일부 CI·shallow clone)은 스킵.
func TestReadmeBadgeMatchesLatestTag(t *testing.T) {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		t.Skipf("최신 태그 조회 불가(태그 부재 환경): %v", err)
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		t.Skip("최신 태그 없음")
	}
	want := "release-" + tag
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s 배지 = 최신 태그 불일치: %q 없음", p, want)
		}
	}
}
