package docs

import (
	"os"
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

// TestReadmeBadgeMatchesLatestTag — 배지 문자열이 git 최신 태그 또는
// 다음 예정 버전(최신 태그의 +패치/+마이너, plannedBadgeVersions)과
// 일치("release-"+버전). 태그 부재 환경(일부 CI·shallow clone)은 스킵.
// v0.13.2 N2 갱신: 릴리스 준비 커밋→태그 사이 창의 일시 레드 해소.
func TestReadmeBadgeMatchesLatestTag(t *testing.T) {
	tag := latestTag(t)
	allowed := plannedBadgeVersions(tag)
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		ok := false
		for _, v := range allowed {
			if strings.Contains(string(b), "release-"+v) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%s 배지 = 최신 태그·예정 버전 불일치: %q 없음", p, "release-"+tag)
		}
	}
}
