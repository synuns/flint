//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func rtRunID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

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

func rtExport(t *testing.T, path, tag string) []rtRow {
	t.Helper()
	out, code := runCLI(t, []string{
		"export", "--format", "jsonl", "--status", "all", "--tag", tag, "--out", path,
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

func splitNonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func ctx() context.Context { return context.Background() }

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

func TestExportImportRoundtripRealDB(t *testing.T) {
	pool := mustDB(t)
	run := rtRunID("e2e-rt")
	ta, tb, tc := run+"-a", run+"-b", run+"-c"
	cleanupTopics(t, pool, run+"-")
	t.Cleanup(func() { cleanupTopics(t, pool, run+"-") })

	kA := writeKnowledge(t,
		`{"topic":"`+ta+`","summary":"왕복 프로브 본문 A — 합성 데모.","confidence":0.9,"tags":["`+run+`"]}`)
	kB := writeKnowledge(t,
		`{"topic":"`+tb+`","summary":"왕복 프로브 본문 B — 합성 데모.","confidence":0.9,"tags":["`+run+`"]}`)
	kC := writeKnowledge(t,
		`{"topic":"`+tc+`","summary":"왕복 프로브 본문 C — 합성 데모(대체 승격).","confidence":0.7,"tags":["`+run+`"]}`)
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
	rows1 := rtExport(t, file1, run)
	if len(rows1) != 3 {
		t.Fatalf("1차 export 행 %d건, want 3(active 2·invalidated 1); rows=%+v", len(rows1), rows1)
	}
	assertLineage(t, rows1, ta, tc)
	byTopic1 := rtByTopic(t, rows1)
	if s := byTopic1[ta].Status; s != "invalidated" {
		t.Fatalf("%s status=%q, want invalidated", ta, s)
	}

	if _, err := pool.Exec(ctx(), `DELETE FROM knowledge WHERE topic LIKE $1`, run+"-%"); err != nil {
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

	file2 := filepath.Join(dir, "rt2.jsonl")
	rows2 := rtExport(t, file2, run)
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
	assertLineage(t, rows2, ta, tc)

	out, code = runCLI(t, []string{"import", "--format", "jsonl", file1}, "")
	if code != 0 {
		t.Fatalf("재import exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["imported"] != float64(0) || m["duplicated"] != float64(3) {
		t.Fatalf("멱등 재import 요약=%v, want imported=0 duplicated=3", m)
	}
}

func TestImportPAMMinimalRealDB(t *testing.T) {
	pool := mustDB(t)
	run := rtRunID("e2e-rt")
	memID := run + "-p1"
	topic := "pam[" + memID + "]"
	if _, err := pool.Exec(ctx(),
		`DELETE FROM knowledge WHERE topic = $1`, topic); err != nil {
		t.Fatalf("PAM 프로브 정리 실패: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM knowledge WHERE topic = $1`, topic)
	})

	pam := `{"schema":"portable-ai-memory","memories":[{` +
		`"id":"` + memID + `","type":"fact","content":"PAM 수입 프로브 — 합성 데모 팩트. (` + run + `)",` +
		`"temporal":{"created_at":"2026-11-14T00:00:00Z"},"status":"active",` +
		`"confidence":{"current":0.8},"tags":["` + run + `"],"provenance":{"platform":"chatgpt"}}],` +
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

	var status, origin string
	if err := pool.QueryRow(ctx(),
		`SELECT status, origin FROM knowledge WHERE topic = $1`, topic).
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
