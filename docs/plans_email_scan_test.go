// plans_email_scan_test.go — docs/plans/*.md에 이메일 주소 리터럴이
// 없는지 검증한다(개인 데이터 방어 계약 — check_personal과 같은 기준을
// go test 게이트에서도 잡는다).
package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// TestPlanFilesContainNoEmails — 플랜/스펙 문서는 커밋되는 공개 산출물이므로
// 이메일 주소(트레일러 주소 포함)가 문자열로 등장하면 안 된다.
func TestPlanFilesContainNoEmails(t *testing.T) {
	files, err := filepath.Glob("plans/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("plans/*.md 수집 실패: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", f, err)
		}
		for _, m := range emailRe.FindAllString(string(b), -1) {
			if strings.HasSuffix(m, ".git") { // 예시 리모트 경로는 허용
				continue
			}
			t.Errorf("%s에 이메일 주소 리터럴 존재: %q — 치환/제거 필요", f, m)
		}
	}
}
