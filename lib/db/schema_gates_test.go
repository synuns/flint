// schema_gates_test.go — T1(v0.41.0) 승격 게이트 등록표 가드.
// 순수 테스트(DB 불필요): 등록표 연속성·admin.go 게이트 전량 등록·
// cli ensureSchemas의 등록표 파생을 단언한다.
package db

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestSchemaGatesContiguous — Review Focus: 등록표 버전은
// 2..LatestSchemaVersion 연속·중복 없음. 게이트가 빠지거나 순서가
// 흐트러지면 연속 단언이 즉시 깨진다(v0.28.1류 무경고 스킵 방지).
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

// TestSchemaGatesCoverAdmin — admin.go에 정의된 EnsureSchemaV* 전부가
// 등록표에 등록돼야 한다(정의만 추가하고 등록을 잊으면 호출 누락).
func TestSchemaGatesCoverAdmin(t *testing.T) {
	b, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[int]bool{}
	re := regexp.MustCompile(`(?m)^func \(r \*Repo\) EnsureSchemaV(\d+)\(`)
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		v, cerr := strconv.Atoi(m[1])
		if cerr != nil {
			t.Fatalf("버전 파싱 %q: %v", m[1], cerr)
		}
		defined[v] = true
	}
	if len(defined) == 0 {
		t.Fatal("admin.go에 EnsureSchemaV* 정의 없음 — 패턴 확인 필요")
	}
	gates := SchemaGates()
	if len(gates) != len(defined) {
		t.Fatalf("등록표 %d건 vs admin.go 정의 %d건(%v) — 양방향 정합 필요",
			len(gates), len(defined), defined)
	}
	for _, g := range gates {
		if !defined[g.Version] {
			t.Fatalf("등록표 v%d는 admin.go에 정의 없음", g.Version)
		}
	}
}

// TestEnsureSchemasUsesGates — Review Focus: cli/root.go ensureSchemas는
// 등록표(db.SchemaGates) 순차 파생이어야 하고 게이트 메서드를 직접
// 호출해서는 안 된다(수동 호출 목록 잔여 = 등록표 우회 회귀).
func TestEnsureSchemasUsesGates(t *testing.T) {
	b, err := os.ReadFile("../cli/root.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !regexp.MustCompile(`for\s+_,\s*\w+\s*:?=\s*range\s+db\.SchemaGates\(\)`).MatchString(src) {
		t.Fatal("ensureSchemas가 db.SchemaGates() range 파생이 아님 — 순차 호출 등록표 전환 필요")
	}
	if re := regexp.MustCompile(`repo\.EnsureSchemaV\d+\(`); re.MatchString(src) {
		t.Fatalf("직접 게이트 호출 잔여: %v — 등록표 파생으로 수렴 필요", re.FindAllString(src, -1))
	}
}
