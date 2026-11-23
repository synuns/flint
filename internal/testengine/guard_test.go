package testengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countForbiddenImports(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "internal/testengine") {
			count++
		}
		return nil
	})
	return count, err
}

func TestNoProductionImportOfTestengine(t *testing.T) {
	for _, root := range []string{"../../lib", "../../cmd"} {
		count, err := countForbiddenImports(root)
		if err != nil {
			t.Fatalf("%s 순회 실패: %v", root, err)
		}
		if count != 0 {
			t.Fatalf("%s 내 internal/testengine 참조 %d건 — 프로덕션 패키지의 testengine import 금지", root, count)
		}
	}
}

func TestGuardDetectsForbiddenImport(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "lib", "sample")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package sample\n\nimport \"github.com/synuns/flint/internal/testengine\"\n"
	if err := os.WriteFile(filepath.Join(sub, "x.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	count, err := countForbiddenImports(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("위반 탐지 실험: %d != 1", count)
	}
}

var flintDsnReadPatterns = []string{
	`os.Getenv("FLINT_DSN")`,
	`os.LookupEnv("FLINT_DSN")`,
}

func directFlintDSNReads(root string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, pattern := range flintDsnReadPatterns {
			if strings.Contains(string(data), pattern) {
				hits = append(hits, path+": "+pattern)
			}
		}
		return nil
	})
	return hits, err
}

func TestNoDirectFLINTDSNReadInTests(t *testing.T) {
	for _, root := range []string{"../../lib", "../../data", "../../tests"} {
		hits, err := directFlintDSNReads(root)
		if err != nil {
			t.Fatalf("%s 순회 실패: %v", root, err)
		}
		if len(hits) != 0 {
			t.Fatalf("%s 내 FLINT_DSN 직접 읽기 %d건 — testengine.ResolveDSN 경유 필수:\n%s",
				root, len(hits), strings.Join(hits, "\n"))
		}
	}
}

func TestGuardDetectsDirectDSNRead(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "lib", "sample")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package sample\n\nimport \"os\"\n\nvar d = os.Getenv(\"FLINT_DSN\")\n"
	if err := os.WriteFile(filepath.Join(sub, "x_test.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := directFlintDSNReads(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("직접 읽기 탐지 실험: %d != 1", len(hits))
	}
}

func makefileVarDefault(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("Makefile 읽기: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, name+" ?=")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

func TestMakefileTestDSNDefaultIsDedicatedEngine(t *testing.T) {
	got := makefileVarDefault(t, "FLINT_TEST_DSN")
	if got != dedicatedEngineDSN {
		t.Fatalf("Makefile FLINT_TEST_DSN 기본값=%q, want 전용 엔진 DSN %q — 운영 포트(5434) 기본 주입은 테스트 격리를 무력화한다", got, dedicatedEngineDSN)
	}
}

func imageLine(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s 읽기: %v", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "image:")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

func TestComposeTestImageMatchesCIDigest(t *testing.T) {
	composeImg := imageLine(t, filepath.Join("..", "..", "docker-compose.test.yml"))
	ciImg := imageLine(t, filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if !strings.Contains(composeImg, "@sha256:") {
		t.Fatalf("전용 엔진 이미지가 다이제스트 고정 아님: %q — 가변 태그는 CI와 다른 엔진을 띄운다", composeImg)
	}
	if composeImg != ciImg {
		t.Fatalf("이미지 불일치 — compose=%q ci.yml=%q (전용 엔진은 CI와 같은 다이제스트여야 한다)", composeImg, ciImg)
	}
}

func realDBRequiredGuard(t *testing.T, path, funcName string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s 읽기: %v", path, err)
	}
	src := string(b)
	i := strings.Index(src, "func "+funcName+"(")
	if i < 0 {
		t.Fatalf("%s에 %s 부재", path, funcName)
	}
	region := src[i:]
	if j := strings.Index(region, "\nfunc "); j >= 0 {
		region = region[:j]
	}
	fatalAt := strings.Index(region, `FLINT_REALDB_REQUIRED") == "1"`)
	skipAt := strings.Index(region, "t.Skipf(")
	if fatalAt < 0 || skipAt < 0 || fatalAt > skipAt {
		t.Fatalf("%s %s: FLINT_REALDB_REQUIRED=1 t.Fatalf 분기가 접속 실패 skip보다 앞에 없다 — skip은 CI 무결론을 감춘다", path, funcName)
	}
}

func TestRealDBRequiredFatalBeforeSkip(t *testing.T) {
	realDBRequiredGuard(t, filepath.Join("..", "..", "tests", "e2e", "e2e_test.go"), "mustDB")
	realDBRequiredGuard(t, filepath.Join("..", "..", "tests", "regression", "active_knowledge_v06_test.go"), "regViewDB")
}
