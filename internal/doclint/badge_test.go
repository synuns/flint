package doclint

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
		b, err := os.ReadFile(docPath(p))
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

func TestReadmeKoCopilotAutoRegister(t *testing.T) {
	b, err := os.ReadFile(docPath("../README.ko.md"))
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

func TestReadmeKoGoInstallSection(t *testing.T) {
	b, err := os.ReadFile(docPath("../README.ko.md"))
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

func TestReadmeKoHarnessBadgeNote(t *testing.T) {
	b, err := os.ReadFile(docPath("../README.ko.md"))
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

func TestReadmeBadgeMatchesLatestTag(t *testing.T) {
	tag := latestTag(t)
	allowed := allowedBadges(tag)
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(docPath(p))
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
