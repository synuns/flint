package doclint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

func TestPlanFilesContainNoEmails(t *testing.T) {
	var files []string
	err := filepath.WalkDir("../../docs/plans", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("docs/plans/*.md 수집 실패: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", f, err)
		}
		for _, m := range emailRe.FindAllString(string(b), -1) {
			if strings.HasSuffix(m, ".git") {
				continue
			}
			t.Errorf("%s에 이메일 주소 리터럴 존재: %q — 치환/제거 필요", f, m)
		}
	}
}
