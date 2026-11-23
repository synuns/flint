package db

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSchemaGatesContiguous(t *testing.T) {
	gates := SchemaGates()
	if len(gates) == 0 {
		t.Fatal("SchemaGates() 비어 있음")
	}
	if want := LatestSchemaVersion - 1; len(gates) != want {
		t.Fatalf("게이트 %d건, want %d건(v2..v%d)", len(gates), want, LatestSchemaVersion)
	}
	for i, g := range gates {
		if g.Version != 2+i {
			t.Fatalf("gates[%d].Version = %d, want %d(연속 위반 — 누락·중복 또는 순서 오류)",
				i, g.Version, 2+i)
		}
		if g.Ensure == nil {
			t.Fatalf("gates[%d].Ensure nil", i)
		}
	}
	if last := gates[len(gates)-1].Version; last != LatestSchemaVersion {
		t.Fatalf("마지막 게이트 버전 %d != LatestSchemaVersion %d", last, LatestSchemaVersion)
	}
}

func ensureSchemaDefs(t *testing.T, dir string) map[int]string {
	t.Helper()
	defs := map[int]string{}
	re := regexp.MustCompile(`(?m)^func \(r \*Repo\) EnsureSchemaV(\d+)\(`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("디렉터리 읽기 %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("읽기 %s: %v", name, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			v, cerr := strconv.Atoi(m[1])
			if cerr != nil {
				t.Fatalf("버전 파싱 %q(%s): %v", m[1], name, cerr)
			}
			if prev, dup := defs[v]; dup {
				t.Fatalf("EnsureSchemaV%d 중복 정의: %s·%s", v, prev, name)
			}
			defs[v] = name
		}
	}
	return defs
}

func TestSchemaGatesCoverAdmin(t *testing.T) {
	defined := ensureSchemaDefs(t, ".")
	if len(defined) == 0 {
		t.Fatal("lib/db 비테스트 소스에 EnsureSchemaV* 정의 없음 — 패턴 확인 필요")
	}
	gates := SchemaGates()
	if len(gates) != len(defined) {
		t.Fatalf("등록표 %d건 vs 정의 %d건(%v) — 양방향 정합 필요",
			len(gates), len(defined), defined)
	}
	for _, g := range gates {
		if _, ok := defined[g.Version]; !ok {
			t.Fatalf("등록표 v%d는 정의 없음", g.Version)
		}
	}
}

func TestSchemaGateDefsSpanAllFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"admin.go":      "package db\n\nfunc (r *Repo) EnsureSchemaV2() {}\nfunc (r *Repo) EnsureSchemaV3() {}\n",
		"gates_new.go":  "package db\n\n// 신규 게이트 — 다른 파일 정의(구 가드 사각).\nfunc (r *Repo) EnsureSchemaV5() {}\n",
		"noise_test.go": "package db\n\n// _test.go는 대상 아님.\nfunc (r *Repo) EnsureSchemaV9() {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	defs := ensureSchemaDefs(t, root)
	want := map[int]string{2: "admin.go", 3: "admin.go", 5: "gates_new.go"}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("정의 수집 = %v, want %v(admin.go 밖 정의·_test 제외)", defs, want)
	}
}

func cliGateAudit(t *testing.T, dir string) (bool, []string) {
	t.Helper()
	derivation := regexp.MustCompile(`for\s+_,\s*\w+\s*:?=\s*range\s+db\.SchemaGates\(\)`)
	direct := regexp.MustCompile(`\.EnsureSchemaV\d+\(`)
	found := false
	var calls []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if derivation.MatchString(src) {
			found = true
		}
		for _, m := range direct.FindAllString(src, -1) {
			calls = append(calls, fmt.Sprintf("%s: %s", path, m))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("순회 %s: %v", dir, err)
	}
	return found, calls
}

func TestEnsureSchemasUsesGates(t *testing.T) {
	found, calls := cliGateAudit(t, "../cli")
	if !found {
		t.Fatal("lib/cli 어디에도 db.SchemaGates() range 파생 없음 — 순차 호출 등록표 전환 필요")
	}
	if len(calls) > 0 {
		t.Fatalf("직접 게이트 호출 잔여: %v — 등록표 파생으로 수렴 필요", calls)
	}
}

func TestCliGateAuditSpansAllFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"root.go": "package cli\n\nfunc ensureSchemas() error {\n	for _, g := range db.SchemaGates() {\n		_ = g\n	}\n	return nil\n}\n",

		"probe_call.go": "package cli\n\nfunc legacy(ctx context.Context, repo *db.Repo) error { return repo.EnsureSchemaV3(ctx) }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	found, calls := cliGateAudit(t, root)
	if !found {
		t.Fatal("range 파생 미탐지")
	}
	if len(calls) != 1 || !strings.Contains(calls[0], "EnsureSchemaV3") {
		t.Fatalf("root.go 밖 직접 호출 미탐지(파일 한정 사각 잔존): %v", calls)
	}
}
