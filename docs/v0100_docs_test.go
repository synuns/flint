package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0100(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeBadgeIsV0100(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0100(t, p), "release-v0.12.0") {
			t.Errorf("%s 배지가 v0.10.0 아님", p)
		}
	}
}

func TestReadmeMentionsSuggest(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStrV0100(t, p)
		if !strings.Contains(s, "suggestions") {
			t.Errorf("%s read 제안 미언급", p)
		}
		if !strings.Contains(s, "sha256") {
			t.Errorf("%s 백업 무결성 미언급", p)
		}
	}
}

// planV0100Section — PLAN.md에서 v0.10.0 마일스톤 섹션(### v0.10.0부터
// 다음 ## 헤더 전)까지를 잘라 반환한다.
func planV0100Section(t *testing.T) string {
	t.Helper()
	s := docStrV0100(t, "../docs/PLAN.md")
	i := strings.Index(s, "### v0.10.0")
	if i < 0 {
		t.Fatal("PLAN.md에 v0.10.0 마일스톤 없음")
	}
	s = s[i:]
	if j := strings.Index(s, "\n## "); j >= 0 {
		s = s[:j]
	}
	return s
}

// TestPlanV0100MilestoneNumbering — 마일스톤 R번호가 스펙(§3 R1–R5)·
// 구현 커밋(T2:R3·T3:R4·T4:R5)과 일치하는지. 압축·이동 번호 회귀 방지.
func TestPlanV0100MilestoneNumbering(t *testing.T) {
	sec := planV0100Section(t)
	for _, want := range []string{
		"R1 did-you-mean",
		"R2 NFKC",
		"R3 자동 백업 사이드카",
		"R4 doctor backup-age",
		"R5 문서 동기화",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("v0.10.0 마일스톤에 %q 없음(스펙 R1–R5 번호와 불일치)", want)
		}
	}
	if strings.Contains(sec, "R4 문서 동기화") {
		t.Errorf("v0.10.0 마일스톤이 이동 번호(R4 문서 동기화)로 회귀함")
	}
}

// TestReadmeBackupLineAttribution — sha256 사이드카 신규 적용 대상은
// `flint done` 자동 백업(R3). `flint backup`에 오귀속한 문구 회귀 방지.
func TestReadmeBackupLineAttribution(t *testing.T) {
	for _, bad := range []string{
		"`flint backup`은 자동 백업 sha256",
		"`flint backup` verifies dump integrity via an automatic",
	} {
		for _, p := range []string{"../README.md", "../README.ko.md"} {
			if strings.Contains(docStrV0100(t, p), bad) {
				t.Errorf("%s 백업 문구 오귀속(%q) — done 자동 백업이 아닌 flint backup에 귀속", p, bad)
			}
		}
	}
}
