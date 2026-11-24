package doclint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const llmGateScript = "../scripts/check_llm_imports.py"

func runLLMImportGate(t *testing.T, root string, whitelistJSON string) (string, int) {
	t.Helper()
	cmd := exec.Command("python3", docPath(llmGateScript), root)
	if whitelistJSON != "" {
		dir := t.TempDir()
		p := filepath.Join(dir, "whitelist.json")
		if err := os.WriteFile(p, []byte(whitelistJSON), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(os.Environ(), "FLINT_LLM_IMPORT_WHITELIST="+p)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("check_llm_imports 실행 실패: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func writeTempGoFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "demo.go")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLLMImportGateSDKImportDetected(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("SDK import 위반 exit 3 기대, 실제 %d\n%s", code, out)
	}
	for _, want := range []string{"demo.go:", "github.com/sashabaranov/go-openai", "수정처"} {
		if !strings.Contains(out, want) {
			t.Errorf("구조화 실패 출력 누락 %q:\n%s", want, out)
		}
	}
}

func TestLLMImportGateRepoClean(t *testing.T) {
	out, code := runLLMImportGate(t, docPath(".."), "")
	if code != 0 {
		t.Fatalf("현재 저장소는 전면 금지(화이트리스트 0건)에서 exit 0 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateDotImportDetected(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport . \"github.com/sashabaranov/go-openai\"\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("dot import SDK도 위반 — exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "github.com/sashabaranov/go-openai") {
		t.Errorf("dot import 경로 미표기:\n%s", out)
	}
}

func TestLLMImportGateDotImportBlockDetected(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport (\n\t\"fmt\"\n\t. \"github.com/sashabaranov/go-openai\"\n)\n")
	if _, code := runLLMImportGate(t, dir, ""); code != 3 {
		t.Fatalf("import 블록 내 dot import도 위반 — exit 3 기대, 실제 %d", code)
	}
}

func TestLLMImportGateCommentedImportPasses(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport (\n\t\"fmt\"\n\t// deprecated: \"github.com/sashabaranov/go-openai\" 제거 예정\n)\n")
	if out, code := runLLMImportGate(t, dir, ""); code != 0 {
		t.Fatalf("주석 줄의 SDK 경로는 위반 아님 — exit 0 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateNonSDKImportPasses(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport (\n\t\"fmt\"\n\t\"github.com/jackc/pgx/v5\"\n)\n")
	if out, code := runLLMImportGate(t, dir, ""); code != 0 {
		t.Fatalf("비 SDK import는 통과 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateWhitelistAllows(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	wl := `{"github.com/sashabaranov/go-openai":{"reason":"합성 데모 데이터 어댑터","expires":"2099-01-01"}}`
	if out, code := runLLMImportGate(t, dir, wl); code != 0 {
		t.Fatalf("화이트리스트 등재 import는 통과 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateWhitelistPathMismatchFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	wl := `{"github.com/other/sdk":{"reason":"합성 데모 데이터 어댑터","expires":"2099-01-01"}}`
	out, code := runLLMImportGate(t, dir, wl)
	if code != 3 {
		t.Fatalf("경로 불일치 화이트리스트는 면책 없는 위반 — exit 3 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateDisclaimerRequiresReason(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	wl := `{"github.com/sashabaranov/go-openai":{"expires":"2099-01-01"}}`
	out, code := runLLMImportGate(t, dir, wl)
	if code != 3 {
		t.Fatalf("면책 이유 누락 — exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "이유") {
		t.Errorf("면책 이유 필수 안내 누락:\n%s", out)
	}
}

func TestLLMImportGateDisclaimerRequiresExpiry(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	wl := `{"github.com/sashabaranov/go-openai":{"reason":"합성 데모 데이터 어댑터"}}`
	out, code := runLLMImportGate(t, dir, wl)
	if code != 3 {
		t.Fatalf("면책 만료일 누락 — exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "만료일") {
		t.Errorf("만료일 필수 안내 누락:\n%s", out)
	}
}

func TestLLMImportGateDisclaimerExpiredFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	wl := `{"github.com/sashabaranov/go-openai":{"reason":"합성 데모 데이터 어댑터","expires":"2000-01-01"}}`
	out, code := runLLMImportGate(t, dir, wl)
	if code != 3 {
		t.Fatalf("만료일 경과 면책은 위반 — exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "경과") {
		t.Errorf("만료일 경과 안내 누락:\n%s", out)
	}
}

func TestLLMImportGateWaiverAllows(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\n// flint-waiver: 등록표 외부 SDK 어댑터 이관 대기 expires=2099-01-01\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	if out, code := runLLMImportGate(t, dir, ""); code != 0 {
		t.Fatalf("유효한 미만료 면책 행 아래 import는 통과 기대, 실제 %d\n%s", code, out)
	}
}

func TestLLMImportGateWaiverExpiredFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\n// flint-waiver: 등록표 외부 SDK 어댑터 이관 대기 expires=2000-01-01\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("만료된 면책 행은 무효 — 위반 exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "경과") {
		t.Errorf("면책 만료일 경과 안내 누락:\n%s", out)
	}
}

func TestLLMImportGateWaiverMissingReasonFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\n// flint-waiver: expires=2099-01-01\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("면책 이유 누락은 무효 — 위반 exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "이유 누락") {
		t.Errorf("면책 이유 누락 안내 미표기:\n%s", out)
	}
}

func TestLLMImportGateWaiverMissingExpiryFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\n// flint-waiver: 등록표 외부 SDK 어댑터 이관 대기\nimport _ \"github.com/sashabaranov/go-openai\"\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("면책 만료일 누락은 무효 — 위반 exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "만료일 필수") {
		t.Errorf("면책 만료일 필수 안내 미표기:\n%s", out)
	}
}

func TestLLMImportGateWaiverNotAdjacentFails(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\n// flint-waiver: 등록표 외부 SDK 어댑터 이관 대기 expires=2099-01-01\nimport (\n\t\"fmt\"\n\t\"github.com/sashabaranov/go-openai\"\n)\n")
	if _, code := runLLMImportGate(t, dir, ""); code != 3 {
		t.Fatalf("면책 행은 바로 아래 1행에만 적용 — 떨어진 import는 위반 exit 3 기대, 실제 %d", code)
	}
}

func TestLLMImportGateBlockCommentParenNotBlockEnd(t *testing.T) {
	dir := t.TempDir()
	writeTempGoFile(t, dir, "package demo\n\nimport (\n\t\"fmt\"\n\t/* a note that mentions\n)\nand continues on later lines\n*/\n\t\"github.com/sashabaranov/go-openai\"\n)\n")
	out, code := runLLMImportGate(t, dir, "")
	if code != 3 {
		t.Fatalf("블록 주석 내 ')'는 import 블록 종결이 아님 — 주석 뒤 SDK import도 탐지, exit 3 기대, 실제 %d\n%s", code, out)
	}
	if !strings.Contains(out, "github.com/sashabaranov/go-openai") {
		t.Errorf("주석 뒤 SDK import 경로 미표기:\n%s", out)
	}
}

func TestCiRunsVetLint(t *testing.T) {
	b, err := os.ReadFile(docPath("../Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "ci:") {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatal("Makefile ci 타깃 부재")
	}
	var block []string
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "\t") {
			block = append(block, l)
			continue
		}
		break
	}
	recipe := strings.Join(block, "\n")
	if !strings.Contains(recipe, "go vet ./...") {
		t.Error("ci 타깃에 go vet ./... 행 누락 — vet 상시화(M2 검사 3)")
	}
	if !strings.Contains(recipe, "$(MAKE) lint") {
		t.Error("ci 타깃에 $(MAKE) lint 행 누락 — lint(staticcheck) 상시화(M2 검사 3)")
	}
	if !strings.Contains(recipe, "check_llm_imports.py") {
		t.Error("ci 타깃에 check_llm_imports.py 행 누락 — T2 보호 린트 상시화(M2 검사 2)")
	}
}
