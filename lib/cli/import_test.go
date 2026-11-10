// import_test.go — v0.27.0 R3: 온보딩 임포트 계약 — 후보 탐색·헤딩 분할·
// dry-run 기본·--commit 적립·dedup 멱등.
package cli

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
	// 리뷰 10: 수 계약 단언 — A·B·C 3섹션(짧은 B 포함 — 호출부 필터).
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

// TestParseMarkdownFenceGuard — v0.27.0 리뷰 6: 펜스 내부 "## "는
// 헤딩 아니다 — 섹션 분할·주석 제거 모두 펜스 보존.
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

// TestStripImportNoiseCommentFence — v0.27.3(재감사 발견1): 주석 블록
// 내부의 ``` 라인이 펜스를 열어 이후 섹션 전체를 삼키던 결함 — 주석
// 처리가 펜스보다 우선하고 펜스 판정은 주석 제거 후 라인만 본다.
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

// TestStripImportNoiseSameLineClose — v0.27.3(발견2): 같은 줄
// `<!-- 주석 --> 뒷텍스트`가 inComment를 잔류시켜 이후 문서 전체를
// 무음 삭제하던 결함.
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

// TestStripImportNoiseInline — v0.27.3(발견9): 줄 중간 인라인 주석은
// 주석 부분만 제거되고 앞뒤 텍스트가 보존된다.
func TestStripImportNoiseInline(t *testing.T) {
	got := stripImportNoise("규칙은 이렇다 <!-- 비고: 예외 없음 --> 저렇게 쓴다")
	if strings.Contains(got, "비고") {
		t.Errorf("인라인 주석 잔존: %q", got)
	}
	if !strings.Contains(got, "규칙은 이렇다") || !strings.Contains(got, "저렇게 쓴다") {
		t.Errorf("인라인 앞뒤 미보존: %q", got)
	}
}

// TestParseMarkdownTildeFence — v0.27.3: ~~~ 펜스(CommonMark) 내부
// 헤딩 오판 방지 — 유사 지식("규칙[가짜]") 적립 차단.
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

// TestParseMarkdownUnterminatedFence — EOF에서 닫히지 않은 펜스는
// 잔여 라인을 직전 섹션 본문으로 흡수(헤딩 오판 없이).
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

// TestRunImportSameFileTwice — v0.27.3(발견3): 같은 파일 2회 지정은
// 배치 내 dedup으로 1건 적립(unique 위반 rollback+exit 0이 아니라
// skipped 보고).
func TestRunImportSameFileTwice(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 중복\n동일 내용 섹션 — 최소 길이 충분합니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := runImport(context.Background(), f, []string{p, p, "--commit"}, strings.NewReader(""), &out)
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

// TestRunImportPartialUnreadableReported — v0.27.3(발견5): 읽기 가능·불가
// 혼재 시 unreadable이 출력에 보고된다(부분 유실의 무음 누락 방지).
func TestRunImportPartialUnreadableReported(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 규칙\n읽히는 파일의 본문이 충분히 있습니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := runImport(context.Background(), f, []string{p, dir + "/없는파일.md", "--commit"}, strings.NewReader(""), &out)
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

// TestRunImportOriginNormalized — v0.27.3(발견6): CLAUDE.md·./CLAUDE.md
// 재임포트가 같은 origin(파일명 기준)으로 동일 해시 — 멱등 유지.
func TestRunImportOriginNormalized(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/CLAUDE.md", []byte("## 규칙\n경로 정규화 검증용 충분한 본문입니다.\n"), 0o644)
	t.Chdir(dir) // 상대경로 후보 탐색도 같은 디렉터리에서
	f1 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	runImport(context.Background(), f1, []string{"CLAUDE.md", "--commit"}, strings.NewReader(""), &strings.Builder{})
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f2.exists = true // 첫 적립과 동일 해시 → ExistsHash 참으로 멱등 skip
	var out strings.Builder
	code := runImport(context.Background(), f2, []string{"./CLAUDE.md", "--commit"}, strings.NewReader(""), &out)
	if code != 0 || !strings.Contains(out.String(), `"inserted":0`) {
		t.Fatalf("경로 재임포트 멱등 위반: exit=%d %s", code, out.String())
	}
}

// TestRunImportUnknownFlag — v0.27.3(발견12): 미확인 플래그는 사용법
// 오류(오타 플래그의 무음 경로화 방지) — errLine 관례·v0.34.3부터 무효
// 인자 계약 exit 3(doctor·tuning·diff·maintain 정합).
func TestRunImportUnknownFlag(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := runImport(context.Background(), f, []string{"--verbose"}, strings.NewReader(""), &out)
	if code != 3 || !strings.Contains(out.String(), `"unknown flag: --verbose"`) {
		t.Fatalf("미확인 플래그 = exit %d %s, want 3 unknown flag", code, out.String())
	}
	// 따옴표 포함 플래그도 JSON 파싱 가능(errLine 이스케이프).
	out.Reset()
	code = runImport(context.Background(), f, []string{`--x"y`}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("이상 플래그 exit=%d", code)
	}
	if !json.Valid([]byte(strings.TrimRight(out.String(), "\n"))) {
		t.Fatalf("플래그 에러 JSON 파손: %s", out.String())
	}
}

// TestRunImportOriginCleanNotBase — 리뷰 1(v0.27.3): origin은 Clean
// 정규화 — 디렉터리가 다른 동일 파일명(a/rules.md·b/rules.md)은 서로
// 다른 규칙으로 둘 다 적립(basename 충돌 무음 skip 방지).
func TestRunImportOriginCleanNotBase(t *testing.T) {
	dir := t.TempDir()
	body := "## 코딩 스타일\n동일 제목·본문 — 최소 길이 충분합니다.\n"
	os.MkdirAll(dir+"/a", 0o755)
	os.MkdirAll(dir+"/b", 0o755)
	os.WriteFile(dir+"/a/rules.md", []byte(body), 0o644)
	os.WriteFile(dir+"/b/rules.md", []byte(body), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var out strings.Builder
	code := runImport(context.Background(), f, []string{dir + "/a/rules.md", dir + "/b/rules.md", "--commit"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"inserted":2`) {
		t.Fatalf("basename 충돌 무음 skip: %s", out.String())
	}
}

// TestRunImportDedupErrorAllFail — 리뷰 3(v0.27.3): dedup 조회 전량
// 실패는 '전부 중복'이 아닌 장애 — exit 1(거짓 성공 금지).
func TestRunImportDedupErrorAllFail(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/rules.md"
	os.WriteFile(p, []byte("## 규칙\n조회 장애 상황 검증용 충분한 본문입니다.\n"), 0o644)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	f.existsErr = errors.New("db down")
	var out strings.Builder
	code := runImport(context.Background(), f, []string{p, "--commit"}, strings.NewReader(""), &out)
	if code != 1 || !strings.Contains(out.String(), `"status":"error"`) {
		t.Fatalf("전량 조회 실패 = exit %d %s, want 1 error", code, out.String())
	}
}

// TestParseMarkdownDanglingTilde — 리뷰 2(v0.27.3): 미종결 ~~~ 한 줄이
// 나머지 문서 헤딩을 삼키지 않는다(개시 라인 본문 중립화).
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

// TestStripImportNoiseDoubleInline — 리뷰 4(v0.27.3): 같은 줄 주석 2쌍은
// 전부 제거(두 번째 개시 마커 잔존 방지).
func TestStripImportNoiseDoubleInline(t *testing.T) {
	got := stripImportNoise("설명 <!-- 비고1 --> 예시 <!-- 비고2")
	if strings.Contains(got, "<!--") || strings.Contains(got, "비고") {
		t.Errorf("다중 인라인 주석 잔존: %q", got)
	}
	if !strings.Contains(got, "설명") || !strings.Contains(got, "예시") {
		t.Errorf("본문 미보존: %q", got)
	}
}
