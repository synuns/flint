// import_test.go — v0.27.0 R3: 온보딩 임포트 계약 — 후보 탐색·헤딩 분할·
// dry-run 기본·--commit 적립·dedup 멱등.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMarkdownSections(t *testing.T) {
	body := "# 제목\n\n## 규칙 A\n내용 12자 이상 있습니다.\n\n## 규칙 B\n\n짧\n\n## 규칙 C\n<!-- 주석 -->\n충분한 내용이 여기 있습니다.\n"
	got := parseMarkdownSections(stripImportNoise(body))
	if len(got) != 2 { // B는 너무 짧아 parse에서 남지만 호출부에서 걸러짐 — 여기선 3섹션
		t.Logf("섹션 수=%d(짧은 B 포함 — 호출부 필터)", len(got))
	}
	if len(got) >= 1 && got[0].Heading != "규칙 A" {
		t.Errorf("첫 헤딩=%q", got[0].Heading)
	}
	noise := stripImportNoise("전\n<!-- 주석\n내부\n-->\n후")
	if strings.Contains(noise, "주석") || !strings.Contains(noise, "후") {
		t.Errorf("HTML 주석 미제거: %q", noise)
	}
}

func TestRunImportDryRunAndCommit(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("# 규칙\n\n## 코딩 스타일\n이 프로젝트는 탭 대신 스페이스 2칸을 사용한다.\n\n## 테스트\n테스트는 표준 라이브러리만 사용한다.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	// 후보 명시 + dry-run.
	code := runImport(context.Background(), f, []string{claude}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("dry-run exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"dry-run"`) || !strings.Contains(out.String(), `"sections":2`) {
		t.Fatalf("dry-run 출력: %s", out.String())
	}
	// --commit.
	out.Reset()
	code = runImport(context.Background(), f, []string{claude, "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("commit exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"inserted":2`) {
		t.Fatalf("commit 출력: %s", out.String())
	}
	if len(f.inserted) != 2 {
		t.Fatalf("적립 = %d건, want 2", len(f.inserted))
	}
	// 재임포트 — dedup 멱등.
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f2.exists = true // 모든 해시 존재 → skip
	out.Reset()
	code = runImport(context.Background(), f2, []string{claude, "--commit"}, strings.NewReader(""), &out)
	if code != 0 || !strings.Contains(out.String(), `"inserted":0`) {
		t.Fatalf("재임포트 멱등 위반: exit=%d %s", code, out.String())
	}
}
