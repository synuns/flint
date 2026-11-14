//go:build e2e

// export_import_test.go — v1.3.1 B1(R19): export/import 실DB 왕복 e2e.
// 시나리오: 합성 seed(active 2·invalidated 1 계보 포함) → export jsonl
// → 테이블 정리(fresh 상태) → import jsonl 재이식 → 재export 동일성
// (행 수·토픽·본문·계보 superseded_by 연결). 후속 PAM 최소 문서 1건
// 수입(pending 적립·source 원천 기록) 확인. 기존 e2e 헬퍼(mustDB·
// runCLI·decode·writeKnowledge) 재사용 — DB 미기동 시 스킵(e2e_test.go 관례).
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rtRow — jsonl export 행 파싱(snake_case 키 — FormatJSONL 계약).
type rtRow struct {
	ID           int      `json:"id"`
	Topic        string   `json:"topic"`
	Content      string   `json:"content"`
	Status       string   `json:"status"`
	Source       string   `json:"source"`
	ContentHash  string   `json:"content_hash"`
	SupersededBy *int     `json:"superseded_by"`
	Tags         []string `json:"tags"`
}

// rtExport — `flint export --format jsonl --status all --tag e2e-rt --out`
// 실행 후 파일을 행으로 파싱(요약은 stderr — exit로 판정).
func rtExport(t *testing.T, path string) []rtRow {
	t.Helper()
	out, code := runCLI(t, []string{
		"export", "--format", "jsonl", "--status", "all", "--tag", "e2e-rt", "--out", path,
	}, "")
	if code != 0 {
		t.Fatalf("export exit=%d, want 0; out=%s", code, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("export 파일 읽기: %v", err)
	}
	var rows []rtRow
	for _, line := range splitNonEmpty(string(b)) {
		var r rtRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("jsonl 행 파싱: %v: %s", err, line)
		}
		rows = append(rows, r)
	}
	return rows
}

// splitNonEmpty — 개행 구분 비빈 줄.
func splitNonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ctx — 테스트 공용 컨텍스트(기존 e2e 관례: context.Background() 직접).
func ctx() context.Context { return context.Background() }

// rtByTopic — 행을 topic 기준 색인(왕복 비교 원천 — id는 재이식 후
// 재할당되므로 토픽이 안정 식별자).
func rtByTopic(t *testing.T, rows []rtRow) map[string]rtRow {
	t.Helper()
	m := map[string]rtRow{}
	for _, r := range rows {
		if _, dup := m[r.Topic]; dup {
			t.Fatalf("topic %q 중복 행", r.Topic)
		}
		m[r.Topic] = r
	}
	return m
}

// assertLineage — 계보 동일성: invalidated 행의 superseded_by가 같은
// 파일 안 실재 행 id를 가리키고, 그 행의 topic이 replacing과 일치.
func assertLineage(t *testing.T, rows []rtRow, invalidated, replacing string) {
	t.Helper()
	byTopic := rtByTopic(t, rows)
	old := byTopic[invalidated]
	new_ := byTopic[replacing]
	if old.SupersededBy == nil {
		t.Fatalf("%s superseded_by 없음(계보 유실): %+v", invalidated, old)
	}
	if *old.SupersededBy != new_.ID {
		t.Fatalf("%s superseded_by=%d, want %d(%s의 id — 계보 미보존)",
			invalidated, *old.SupersededBy, new_.ID, replacing)
	}
	// id 참조 무결성: 대상 id가 파일 안 행으로 실재.
	found := false
	for _, r := range rows {
		if r.ID == *old.SupersededBy {
			found = true
		}
	}
	if !found {
		t.Fatalf("superseded_by=%d가 재export 행 중 미존재", *old.SupersededBy)
	}
}

// TestExportImportRoundtripRealDB — B1 왕복 실증(jsonl 재이식·재export
// 동일성). seed는 합성 데모데이터(개인 데이터 금지 — 저장소 원칙).
func TestExportImportRoundtripRealDB(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-rt-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-rt-") })

	// seed: active 2(kA·kB) + pending 1(kC) → approve --supersede로 kA를
	// kC가 대체(invalidated 1·계보 포함). 결과 3행: active 2·invalidated 1.
	kA := writeKnowledge(t,
		`{"topic":"e2e-rt-a","summary":"왕복 프로브 본문 A — 합성 데모.","confidence":0.9,"tags":["e2e-rt"]}`)
	kB := writeKnowledge(t,
		`{"topic":"e2e-rt-b","summary":"왕복 프로브 본문 B — 합성 데모.","confidence":0.9,"tags":["e2e-rt"]}`)
	kC := writeKnowledge(t,
		`{"topic":"e2e-rt-c","summary":"왕복 프로브 본문 C — 합성 데모(대체 승격).","confidence":0.7,"tags":["e2e-rt"]}`)
	if kB["state"] != "active" || kC["state"] != "pending" {
		t.Fatalf("seed state kB=%v kC=%v, want active/pending", kB["state"], kC["state"])
	}
	idA, idC := int(kA["id"].(float64)), int(kC["id"].(float64))
	out, code := runCLI(t, []string{"approve", "--id", strconv.Itoa(idC), "--supersede", strconv.Itoa(idA)}, "")
	if code != 0 {
		t.Fatalf("approve --supersede exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["superseded"] != float64(idA) {
		t.Fatalf("approve superseded=%v, want %d; out=%s", m["superseded"], idA, out)
	}

	dir := t.TempDir()
	file1 := filepath.Join(dir, "rt1.jsonl")
	rows1 := rtExport(t, file1)
	if len(rows1) != 3 {
		t.Fatalf("1차 export 행 %d건, want 3(active 2·invalidated 1); rows=%+v", len(rows1), rows1)
	}
	assertLineage(t, rows1, "e2e-rt-a", "e2e-rt-c")
	byTopic1 := rtByTopic(t, rows1)
	if s := byTopic1["e2e-rt-a"].Status; s != "invalidated" {
		t.Fatalf("e2e-rt-a status=%q, want invalidated", s)
	}

	// fresh 상태 — 시드 행 정리 후 재이식(왕복 재현).
	if _, err := pool.Exec(ctx(), `DELETE FROM knowledge WHERE topic LIKE 'e2e-rt-%'`); err != nil {
		t.Fatalf("시드 정리 실패: %v", err)
	}
	out, code = runCLI(t, []string{"import", "--format", "jsonl", file1}, "")
	if code != 0 {
		t.Fatalf("import exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "ok" || m["imported"] != float64(3) {
		t.Fatalf("import 요약=%v, want status=ok imported=3", m)
	}
	if sup, _ := m["superseded"].(float64); sup < 1 {
		t.Fatalf("import superseded=%v, want >=1(계보 복원 보고)", m["superseded"])
	}

	// 재export 동일성 — 행 수·토픽·본문·상태·계보.
	file2 := filepath.Join(dir, "rt2.jsonl")
	rows2 := rtExport(t, file2)
	if len(rows2) != len(rows1) {
		t.Fatalf("재export 행 %d건, want %d건", len(rows2), len(rows1))
	}
	byTopic2 := rtByTopic(t, rows2)
	for topic, r1 := range byTopic1 {
		r2, ok := byTopic2[topic]
		if !ok {
			t.Fatalf("재export에 topic %q 유실(왕복 불완전)", topic)
		}
		if r2.Content != r1.Content || r2.Status != r1.Status || r2.Source != r1.Source {
			t.Fatalf("topic %q 왕복 필드 변형: content=%q→%q status=%q→%q source=%q→%q",
				topic, r1.Content, r2.Content, r1.Status, r2.Status, r1.Source, r2.Source)
		}
	}
	assertLineage(t, rows2, "e2e-rt-a", "e2e-rt-c")

	// 멱등 — 같은 파일 재수입은 전부 duplicate(content_hash 스킵).
	out, code = runCLI(t, []string{"import", "--format", "jsonl", file1}, "")
	if code != 0 {
		t.Fatalf("재import exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["imported"] != float64(0) || m["duplicated"] != float64(3) {
		t.Fatalf("멱등 재import 요약=%v, want imported=0 duplicated=3", m)
	}
}

// TestImportPAMMinimalRealDB — B1 후속: PAM v1.0 최소 문서 1건 수입 —
// pending 적립(승격 모델)·source "import:pam:<platform>" 원천 기록.
func TestImportPAMMinimalRealDB(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-rt-") // PAM topic 파생("pam[e2e-rt-…]")도 커버
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-rt-") })

	pam := `{"schema":"portable-ai-memory","memories":[{` +
		`"id":"e2e-rt-p1","type":"fact","content":"PAM 수입 프로브 — 합성 데모 팩트.",` +
		`"temporal":{"created_at":"2026-11-14T00:00:00Z"},"status":"active",` +
		`"confidence":{"current":0.8},"tags":["e2e-rt"],"provenance":{"platform":"chatgpt"}}],` +
		`"relations":[]}`
	pamFile := filepath.Join(t.TempDir(), "memory-store.json")
	if err := os.WriteFile(pamFile, []byte(pam), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, []string{"import", "--format", "pam", pamFile}, "")
	if code != 0 {
		t.Fatalf("import --format pam exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" || m["imported"] != float64(1) {
		t.Fatalf("PAM import 요약=%v, want status=ok imported=1", m)
	}

	// pending 적립 + 원천 기록 실측(열은 origin — ExportRow.Source 대응).
	var status, origin string
	if err := pool.QueryRow(ctx(),
		`SELECT status, origin FROM knowledge WHERE topic = 'pam[e2e-rt-p1]'`).
		Scan(&status, &origin); err != nil {
		t.Fatalf("PAM 적립 행 조회 실패: %v", err)
	}
	if status != "pending" {
		t.Fatalf("PAM 적립 status=%q, want pending(승격 모델)", status)
	}
	if origin != "import:pam:chatgpt" {
		t.Fatalf("PAM origin=%q, want import:pam:chatgpt", origin)
	}
}
