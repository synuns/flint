// badge_v0132_test.go — v0.13.2 심층 리뷰 회귀(N2): 배지 가드는 최신
// 태그와 함께 다음 예정 버전(최신 태그의 +패치/+마이너)까지 허용한다 —
// 릴리스 준비 커밋→태그 사이 창의 일시 레드 해소, 낡은 배지·오타 가드 유지.
package docs

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// latestTag — git 최신 태그(없으면 스킵).
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

// badgeRe — README release 배지 추출용.
var badgeRe = regexp.MustCompile(`release-v[0-9]+\.[0-9]+\.[0-9]+`)

// readmeReleaseBadge — README 1개의 release 배지 문자열.
func readmeReleaseBadge(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return badgeRe.FindString(string(b))
}

// plannedBadgeVersions — 최신 태그 기준 허용 배지 버전 후보:
// 최신 태그 자신 + 다음 패치 + 다음 마이너. 파싱 불가 태그는 자신만.
func plannedBadgeVersions(tag string) []string {
	var maj, min, pat int
	if _, err := fmt.Sscanf(tag, "v%d.%d.%d", &maj, &min, &pat); err != nil {
		return []string{tag}
	}
	return []string{
		tag,
		fmt.Sprintf("v%d.%d.%d", maj, min, pat+1),
		fmt.Sprintf("v%d.%d.0", maj, min+1),
	}
}

// TestPlannedBadgeVersions — 후보 산출: v0.13.1 → v0.13.1·v0.13.2·v0.14.0.
// 파싱 불가 태그는 자기 자신만 허용(과대 허용 방지).
func TestPlannedBadgeVersions(t *testing.T) {
	got := plannedBadgeVersions("v0.13.1")
	want := []string{"v0.13.1", "v0.13.2", "v0.14.0"}
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

// TestReadmeBadgeIsPlannedVersion — README(EN/KO) 배지가 예정 버전
// 후보 안에 있는지 정규식으로 추출해 확인(양쪽 동일 버전).
func TestReadmeBadgeIsPlannedVersion(t *testing.T) {
	tag := latestTag(t)
	if tag == "" {
		t.Skip("최신 태그 없음")
	}
	allowed := plannedBadgeVersions(tag)
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
