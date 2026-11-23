package doclint

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func latestTag(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		t.Skipf("최신 태그 조회 불가(태그 부재 환경): %v", err)
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		t.Skip("최신 태그 없음")
	}
	return tag
}

var badgeRe = regexp.MustCompile(`release-v[0-9]+\.[0-9]+\.[0-9]+`)

func readmeReleaseBadge(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(docPath(p))
	if err != nil {
		t.Fatal(err)
	}
	return badgeRe.FindString(string(b))
}

func plannedBadgeVersions(tag string) []string {
	var maj, min, pat int
	if _, err := fmt.Sscanf(tag, "v%d.%d.%d", &maj, &min, &pat); err != nil {
		return []string{tag}
	}
	return []string{
		tag,
		fmt.Sprintf("v%d.%d.%d", maj, min, pat+1),
		fmt.Sprintf("v%d.%d.0", maj, min+1),
		fmt.Sprintf("v%d.0.0", maj+1),
	}
}

func TestPlannedBadgeVersions(t *testing.T) {
	got := plannedBadgeVersions("v0.13.1")
	want := []string{"v0.13.1", "v0.13.2", "v0.14.0", "v1.0.0"}
	if len(got) != len(want) {
		t.Fatalf("후보 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("후보[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if got := plannedBadgeVersions("dev"); len(got) != 1 || got[0] != "dev" {
		t.Errorf("파싱 불가 태그 후보 = %v, want [dev]", got)
	}
}

var badgeVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func versionFileBadge() (string, bool) {
	b, err := os.ReadFile(docPath("../VERSION"))
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(b))
	if !badgeVersionRe.MatchString(v) {
		return "", false
	}
	return "v" + v, true
}

func allowedBadges(tag string) []string {
	allowed := plannedBadgeVersions(tag)
	if v, ok := versionFileBadge(); ok {
		seen := false
		for _, c := range allowed {
			if c == v {
				seen = true
				break
			}
		}
		if !seen {
			allowed = append(allowed, v)
		}
	}
	return allowed
}

func TestVersionFileBadge(t *testing.T) {
	v, ok := versionFileBadge()
	if !ok {
		t.Fatal("VERSION 파일 후보 파싱 실패 — x.y.z 형식이어야 한다")
	}
	if !badgeRe.MatchString("release-" + v) {
		t.Errorf("후보 %q가 배지 형식과 불일치", v)
	}
}

func TestReadmeBadgeIsPlannedVersion(t *testing.T) {
	tag := latestTag(t)
	if tag == "" {
		t.Skip("최신 태그 없음")
	}
	allowed := allowedBadges(tag)
	var seen string
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		badge := readmeReleaseBadge(t, p)
		ok := false
		for _, v := range allowed {
			if badge == "release-"+v {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%s 배지 %q는 최신 태그(%s)·다음 예정 버전(%s) 어느 쪽도 아님", p, badge, tag, strings.Join(allowed[1:], "|"))
		}
		if seen != "" && badge != seen {
			t.Errorf("EN/KO 배지 불일치: %s vs %s", seen, badge)
		}
		seen = badge
	}
}

var dynamicCIBadgeRe = regexp.MustCompile(`https://github\.com/synuns/flint/actions/workflows/ci\.yml/badge\.svg`)

func TestReadmeDynamicCIBadge(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(docPath(p))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !dynamicCIBadgeRe.MatchString(s) {
			t.Errorf("%s 동적 CI 배지(img src=actions/workflows/ci.yml/badge.svg) 없음", p)
		}
		if !strings.Contains(s, "https://github.com/synuns/flint/actions/workflows/ci.yml\"") {
			t.Errorf("%s CI 배지가 워크플로 페이지로 링크되지 않음", p)
		}
	}
}
