// import_test.go — the onboarding import contract — candidate discovery·
// heading splitting·dry-run default·--commit accrual·dedup idempotence).
// Direct runImport-call tests were replaced with RunImport (contract
// unchanged).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMarkdownSections(t *testing.T) {
	body := "# 제목\n\n## 규칙 A\n내용 12자 이상 있습니다.\n\n## 규칙 B\n\n짧\n\n## 규칙 C\n<!-- 주석 -->\n충분한 내용이 여기 있습니다.\n"
	got := parseMarkdownSections(stripImportNoise(body))
	// Count-contract assertion — 3 sections A·B·C (the short B included —
	// the caller filters).
	if len(got) != 3 {
		t.Fatalf("섹션 수=%d, want 3(A·B·C)", len(got))
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
	// Explicit candidate + dry-run.
	code := RunImport(context.Background(), f, []string{claude}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("dry-run exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"dry-run"`) || !strings.Contains(out.String(), `"sections":2`) {
		t.Fatalf("dry-run 출력: %s", out.String())
	}
	// --commit.
	out.Reset()
	code = RunImport(context.Background(), f, []string{claude, "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("commit exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"inserted":2`) {
		t.Fatalf("commit 출력: %s", out.String())
	}
	if len(f.inserted) != 2 {
		t.Fatalf("적립 = %d건, want 2", len(f.inserted))
	}
	// Re-import — dedup idempotence.
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f2.exists = true // every hash exists → skip
	out.Reset()
	code = RunImport(context.Background(), f2, []string{claude, "--commit"}, strings.NewReader(""), &out)
	if code != 0 || !strings.Contains(out.String(), `"inserted":0`) {
		t.Fatalf("재임포트 멱등 위반: exit=%d %s", code, out.String())
	}
}

// TestParseMarkdownFenceGuard — a "## " inside a fence is not a heading
// — both section splitting·comment removal preserve fences.
func TestParseMarkdownFenceGuard(t *testing.T) {
	body := "## 규칙\n실제 내용 충분합니다.\n\n```markdown\n## 펜스 내부 예시\n```\n"
	secs := parseMarkdownSections(body)
	if len(secs) != 1 || secs[0].Heading != "규칙" {
		t.Fatalf("펜스 내부 헤딩 오판: %d sections %+v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Text, "## 펜스 내부 예시") {
		t.Errorf("펜스 내부 콘텐츠 미보존: %q", secs[0].Text)
	}
}

// TestStripImportNoiseCommentFence — the defect where a ``` line inside
// a comment block opened a fence and swallowed all later sections —
// comment handling takes precedence over fences, and fence detection
// sees only the post-comment-removal lines.
func TestStripImportNoiseCommentFence(t *testing.T) {
	in := "## A\n본문 A 충분합니다.\n\n<!--\n```\n주석 안 펜스\n-->\n\n## B\n본문 B 충분합니다.\n"
	got := stripImportNoise(in)
	if strings.Contains(got, "주석 안 펜스") {
		t.Errorf("주석 본문 누출: %q", got)
	}
	secs := parseMarkdownSections(got)
	if len(secs) != 2 || secs[1].Heading != "B" {
		t.Fatalf("주석 펜스 오염 — 섹션 소실: %d sections %+v", len(secs), secs)
	}
}

// TestStripImportNoiseSameLineClose — the defect where a same-line
// `<!-- comment --> trailing text` left inComment lingering and silently
// deleted the whole rest of the document.
func TestStripImportNoiseSameLineClose(t *testing.T) {
	in := "## A\n<!-- 주석 --> 뒷 텍스트\n본문 계속 충분합니다.\n\n## B\n본문 B 충분합니다.\n"
	got := stripImportNoise(in)
	secs := parseMarkdownSections(got)
	if len(secs) != 2 {
		t.Fatalf("같은 줄 종결 잔류 — 문서 소실: %q", got)
	}
	if !strings.Contains(secs[0].Text, "뒷 텍스트") || !strings.Contains(secs[0].Text, "본문 계속") {
		t.Errorf("주석 뒤 텍스트 미보존: %q", secs[0].Text)
	}
}

// TestStripImportNoiseInline — a mid-line inline comment removes only the
// comment part; the surrounding text is preserved.
func TestStripImportNoiseInline(t *testing.T) {
	got := stripImportNoise("규칙은 이렇다 <!-- 비고: 예외 없음 --> 저렇게 쓴다")
	if strings.Contains(got, "비고") {
		t.Errorf("인라인 주석 잔존: %q", got)
	}
	if !strings.Contains(got, "규칙은 이렇다") || !strings.Contains(got, "저렇게 쓴다") {
		t.Errorf("인라인 앞뒤 미보존: %q", got)
	}
}

// TestParseMarkdownTildeFence — prevents heading misjudgment inside a
// ~~~ fence (CommonMark) — blocking spurious-knowledge ("rule[fake]")
// accrual.
func TestParseMarkdownTildeFence(t *testing.T) {
	body := "## 실제 섹션\n본문 충분합니다.\n\n~~~python\n## 물결 펜스 내부 헤딩\ncode\n~~~\n"
	secs := parseMarkdownSections(body)
	if len(secs) != 1 || secs[0].Heading != "실제 섹션" {
		t.Fatalf("~~~ 펜스 내부 헤딩 오판: %d sections %+v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Text, "물결 펜스 내부 헤딩") {
		t.Errorf("~~~ 펜스 내부 콘텐츠 미보존: %q", secs[0].Text)
	}
}

// TestParseMarkdownUnterminatedFence — a fence unclosed at EOF absorbs
// the remaining lines into the previous section's body (no heading
// misjudgment).
func TestParseMarkdownUnterminatedFence(t *testing.T) {
	body := "## 섹션\n본문 충분합니다.\n\n```\n미닫힘 코드 블록\n## 여기도 헤딩 아님\n"
	secs := parseMarkdownSections(body)
	if len(secs) != 1 {
		t.Fatalf("미닫힌 펜스 처리: %d sections %+v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Text, "미닫힘 코드 블록") {
		t.Errorf("미닫힌 펜스 콘텐츠 미흡수: %q", secs[0].Text)
	}
}

// TestRunImportSameFileTwice — giving the same file twice accrues once
// via in-batch dedup (a skipped report, not a unique-violation
// rollback+exit 0).
func TestRunImportSameFileTwice(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 중복\n동일 내용 섹션 — 최소 길이 충분합니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{p, p, "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"inserted":1`) || !strings.Contains(out.String(), `"skipped":1`) {
		t.Fatalf("배치 내 dedup 출력: %s", out.String())
	}
	if len(f.inserted) != 1 {
		t.Fatalf("배치 내 dedup 적립 = %d건, want 1", len(f.inserted))
	}
}

// TestRunImportPartialUnreadableReported — when readable·unreadable
// files are mixed, unreadable is reported in the output (preventing the
// silent loss of partial failure).
func TestRunImportPartialUnreadableReported(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 규칙\n읽히는 파일의 본문이 충분히 있습니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{p, dir + "/없는파일.md", "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"unreadable":["`) {
		t.Fatalf("혼재 unreadable 미보고: %s", out.String())
	}
	if len(f.inserted) != 1 {
		t.Fatalf("읽힌 파일 적립 = %d건, want 1", len(f.inserted))
	}
}

// TestRunImportOriginNormalized — re-importing CLAUDE.md·./CLAUDE.md
// yields the same origin (path-normalized) and thus the same hash —
// idempotence preserved.
func TestRunImportOriginNormalized(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/CLAUDE.md", []byte("## 규칙\n경로 정규화 검증용 충분한 본문입니다.\n"), 0o644)
	t.Chdir(dir) // relative-path candidate discovery also in the same directory
	f1 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	RunImport(context.Background(), f1, []string{"CLAUDE.md", "--commit"}, strings.NewReader(""), &strings.Builder{})
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f2.exists = true // the same hash as the first accrual → ExistsHash true, idempotent skip
	var out strings.Builder
	code := RunImport(context.Background(), f2, []string{"./CLAUDE.md", "--commit"}, strings.NewReader(""), &out)
	if code != 0 || !strings.Contains(out.String(), `"inserted":0`) {
		t.Fatalf("경로 재임포트 멱등 위반: exit=%d %s", code, out.String())
	}
}

// TestRunImportUnknownFlag — an unrecognized flag is a usage error
// (preventing a mistyped flag from becoming a silent path) — the errLine
// convention·the invalid-argument contract exit 3 (doctor·tuning·diff·
// maintain alignment). The message carries an allowed-flag usage hint —
// the assertion updated to flag fact + hint.
func TestRunImportUnknownFlag(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{"--verbose"}, strings.NewReader(""), &out)
	if code != 3 || !strings.Contains(out.String(), "unknown flag: --verbose") ||
		!strings.Contains(out.String(), "usage: flint import") {
		t.Fatalf("미확인 플래그 = exit %d %s, want 3 unknown flag + usage 힌트", code, out.String())
	}
	// A flag containing quotes still parses as JSON (errLine escaping).
	out.Reset()
	code = RunImport(context.Background(), f, []string{`--x"y`}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("이상 플래그 exit=%d", code)
	}
	if !json.Valid([]byte(strings.TrimRight(out.String(), "\n"))) {
		t.Fatalf("플래그 에러 JSON 파손: %s", out.String())
	}
}

// TestRunImportOriginCleanNotBase — origin is Clean-normalized — the
// same file name in different directories (a/rules.md·b/rules.md) is
// two different rules, both accrued (preventing a silent basename
// collision skip).
func TestRunImportOriginCleanNotBase(t *testing.T) {
	dir := t.TempDir()
	body := "## 코딩 스타일\n동일 제목·본문 — 최소 길이 충분합니다.\n"
	os.MkdirAll(dir+"/a", 0o755)
	os.MkdirAll(dir+"/b", 0o755)
	os.WriteFile(dir+"/a/rules.md", []byte(body), 0o644)
	os.WriteFile(dir+"/b/rules.md", []byte(body), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{dir + "/a/rules.md", dir + "/b/rules.md", "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"inserted":2`) {
		t.Fatalf("basename 충돌 무음 skip: %s", out.String())
	}
}

// TestRunImportDedupErrorAllFail — a total dedup-lookup failure is an
// outage, not 'all duplicates' — exit 1 (no false success).
func TestRunImportDedupErrorAllFail(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 규칙\n조회 장애 상황 검증용 충분한 본문입니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f.existsErr = errors.New("db down")
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{p, "--commit"}, strings.NewReader(""), &out)
	if code != 1 || !strings.Contains(out.String(), `"status":"error"`) {
		t.Fatalf("전량 조회 실패 = exit %d %s, want 1 error", code, out.String())
	}
}

// TestParseMarkdownDanglingTilde — a one-line unterminated ~~~ does not
// swallow the remaining document's headings (the opening line
// neutralized into body content).
func TestParseMarkdownDanglingTilde(t *testing.T) {
	body := "## A\n본문 A 충분합니다.\n\n~~~\n미종결 물결 표시\n\n## B\n본문 B 충분합니다.\n\n## C\n본문 C 충분합니다.\n"
	secs := parseMarkdownSections(body)
	var heads []string
	for _, s := range secs {
		heads = append(heads, s.Heading)
	}
	if len(secs) != 3 {
		t.Fatalf("미종결 ~~~ 삼킴: %d sections %v", len(secs), heads)
	}
}

// withStderrCapture — swaps os.Stderr for a temp file and returns a
// read closure (import warnings go directly to os.Stderr by convention —
// an *os.File type, so captured to a file rather than a buffer).
func withStderrCapture(t *testing.T) func() string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = tmp
	return func() string {
		os.Stderr = old
		tmp.Close()
		b, rerr := os.ReadFile(tmp.Name())
		if rerr != nil {
			t.Fatal(rerr)
		}
		return string(b)
	}
}

// TestImportMigrationExtRouting — without --format, a single positional
// ending in .jsonl/.pam triggers the migration-routing verdict. An
// explicit --format·other extensions·0 or 2 positionals mean no routing
// (the existing rule-conversion path).
func TestImportMigrationExtRouting(t *testing.T) {
	cases := []struct {
		args   []string
		format string
		file   string
		ok     bool
	}{
		{[]string{"dump.jsonl"}, "jsonl", "dump.jsonl", true},
		{[]string{"dump.pam"}, "pam", "dump.pam", true},
		{[]string{"dir/sub/demo.JSONL"}, "jsonl", "dir/sub/demo.JSONL", true},
		{[]string{"dump.json"}, "", "", false},
		{[]string{"rules.md"}, "", "", false},
		{[]string{"--format", "jsonl", "dump.jsonl"}, "", "", false},
		{[]string{"--format=jsonl", "dump.jsonl"}, "", "", false},
		{[]string{"dump.jsonl", "other.jsonl"}, "", "", false},
		{[]string{"--commit"}, "", "", false},
		{nil, "", "", false},
	}
	for _, c := range cases {
		format, file, ok := importMigrationExt(c.args)
		if ok != c.ok || format != c.format || file != c.file {
			t.Fatalf("args %v = (%q,%q,%v), want (%q,%q,%v)", c.args, format, file, ok, c.format, c.file, c.ok)
		}
	}
}

// TestRunImportJsonlExtRoutesToMigration — without --format, a .jsonl
// path enters migration (a one-line stderr notice) — not rule
// conversion.
func TestRunImportJsonlExtRoutesToMigration(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "demo.jsonl")
	line := `{"id":1,"topic":"demo-routing","content":"routing check demo content long enough","kind":"knowledge","status":"active","confidence":0.8,"tags":[],"entities":[],"domain":"main","source":"demo","created_at":"2026-11-16T00:00:00Z","updated_at":"2026-11-16T00:00:00Z","valid_at":"2026-11-16T00:00:00Z"}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	readStderr := withStderrCapture(t)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{p}, strings.NewReader(""), &out)
	errs := readStderr()
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) || !strings.Contains(out.String(), `"imported":1`) {
		t.Fatalf("migration summary expected: %s", out.String())
	}
	if !strings.Contains(errs, "migration") {
		t.Fatalf("routing notice missing on stderr: %q", errs)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("migration insert = %d건, want 1", len(f.inserted))
	}
}

// TestRunImportEmptyHint — an empty rule-conversion result gets an
// additive hint key (the migration --format guidance).
func TestRunImportEmptyHint(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rules.md")
	if err := os.WriteFile(p, []byte("# 헤딩 없는 문서\n본문만 있는 문서입니다.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := RunImport(context.Background(), f, []string{p}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"empty"`) ||
		!strings.Contains(out.String(), `"hint":"--format jsonl|pam for migration"`) {
		t.Fatalf("empty hint missing: %s", out.String())
	}
}

// TestStripImportNoiseDoubleInline — two comment pairs on the same line
// are both removed (preventing a residual second opening marker).
func TestStripImportNoiseDoubleInline(t *testing.T) {
	got := stripImportNoise("설명 <!-- 비고1 --> 예시 <!-- 비고2")
	if strings.Contains(got, "<!--") || strings.Contains(got, "비고") {
		t.Errorf("다중 인라인 주석 잔존: %q", got)
	}
	if !strings.Contains(got, "설명") || !strings.Contains(got, "예시") {
		t.Errorf("본문 미보존: %q", got)
	}
}
