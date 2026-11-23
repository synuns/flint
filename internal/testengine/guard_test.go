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
