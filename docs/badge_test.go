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

// TestReadmeKoCopilotAutoRegister — v0.21.0부터 Copilot도 네이티브 CLI
// 자동 등록 대상(setup.go runSetupMCP의 A급 하니스). KO README의
// "검증된 네이티브 CLI가 없어 수동 설정" 서술은 부정홫하므로 금지하고,
// Copilot이 자동 감지 목록에 열거되어야 한다(EN README 문단과 정합).
func TestReadmeKoCopilotAutoRegister(t *testing.T) {
	b, err := os.ReadFile("../README.ko.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"검증된 네이티브 CLI가 없어", "수동 설정입니다"} {
		if strings.Contains(s, bad) {
			t.Errorf("README.ko.md Copilot 수동 설정 서술 잔존: %q", bad)
		}
	}
	i := strings.Index(s, "설치된 하네스")
	if i < 0 {
		t.Fatal("README.ko.md 자동 감지 문단 없음")
	}
	if !strings.Contains(s[i:i+400], "Copilot") {
		t.Error("README.ko.md 자동 감지 목록에 Copilot 없음")
	}
}

// TestReadmeKoGoInstallSection — EN README의 "Install via go install" 절의
// KO 대응 절: 모듈 경로·빌드 정보 버전·flint update 안내까지 번역 정합.
func TestReadmeKoGoInstallSection(t *testing.T) {
	b, err := os.ReadFile("../README.ko.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"go install github.com/synuns/flint/cmd/flint@latest",
		"모듈 경로",
		"빌드 정보",
		"`flint update`",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("README.ko.md go install 절 누락: %q", want)
		}
	}
}

// TestReadmeKoHarnessBadgeNote — harnesses-8 배지 직후, 8종이 A급 자동
// 등록 하니스 기준이며 전체 목록은 어댑터 표에 있다는 1줄 주석 존재.
func TestReadmeKoHarnessBadgeNote(t *testing.T) {
	b, err := os.ReadFile("../README.ko.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	badge := strings.Index(s, "harnesses-8")
	note := strings.Index(s, "A급")
	quick := strings.Index(s, "## 빠른 시작")
	if badge < 0 || note < 0 || quick < 0 {
		t.Fatal("README.ko.md 배지/주석/빠른 시작 앵커 없음")
	}
	if !(badge < note && note < quick) {
		t.Errorf("배지 주석 위치 오류: badge=%d note=%d quick=%d", badge, note, quick)
	}
	if !strings.Contains(s[note:note+200], "어댑터 표") {
		t.Error("배지 주석에 어댑터 표 안내 없음")
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
