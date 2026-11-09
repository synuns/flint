// import.go — v0.27.0 R3(온보딩 임포트): 기존 규칙 파일(CLAUDE.md·
// AGENTS.md 등)을 pending 지식으로 변환 — 조사 설계 승계(헤딩 단위 분할·
// confidence 0.5 → approve 경로·content_hash dedup). dry-run 기본·
// --commit 확정(maintain --expire-pending 관례 승계).
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/synuns/flint/lib/store"
)

func init() { Register("import", runImport) }

// importCandidates — 발견 순위(조사 확정: Claude Code /import 준용).
var importCandidates = []string{
	"CLAUDE.md", "AGENTS.md", ".cursorrules", "GEMINI.md",
	".github/copilot-instructions.md",
}

// importSection — 헤딩 단위 분할 결과 1건.
type importSection struct {
	Heading, Text string
}

// parseMarkdownSections — 헤딩 단위 분할(## 기준 — 표준 라이브러리만).
func parseMarkdownSections(body string) []importSection {
	var out []importSection
	var cur importSection
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
				out = append(out, cur)
			}
			cur = importSection{Heading: strings.TrimSpace(line[3:])}
			continue
		}
		cur.Text += line + "\n"
	}
	if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
		out = append(out, cur)
	}
	return out
}

// runImport — `flint import [path] [--commit]`. 인자 없음 = 현재
// 디렉터리 후보 탐색. dry-run 요약 기본·--commit 적립 확정.
func runImport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	commit := false
	var pos []string
	for _, a := range args {
		if a == "--commit" {
			commit = true
		} else {
			pos = append(pos, a)
		}
	}
	var files []string
	if len(pos) > 0 {
		files = pos
	} else {
		for _, c := range importCandidates {
			if _, err := os.Stat(c); err == nil {
				files = append(files, c)
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, `{"status":"none","detail":"임포트 후보 파일 없음(CLAUDE.md·AGENTS.md 등)"}`)
		return 0
	}
	type item struct{ File, Heading, Summary string }
	var items []item
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, sec := range parseMarkdownSections(stripImportNoise(string(b))) {
			summary := strings.TrimSpace(sec.Text)
			if len([]rune(summary)) < 12 {
				continue
			}
			items = append(items, item{f, sec.Heading, truncateRunesImport(summary, 500)})
		}
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, `{"status":"empty","detail":"변환 가능 섹션 없음(## 헤딩 필요)"}`)
		return 0
	}
	if !commit {
		fmt.Fprintf(stdout, `{"status":"dry-run","files":%d,"sections":%d,"next":"--commit 적립 후 flint approve로 승격"}`+"\n", len(files), len(items))
		return 0
	}
	inserted := 0
	for _, it := range items {
		rec := map[string]any{
			"kind":       "knowledge",
			"topic":      truncateRunesImport("규칙["+it.Heading+"]", 80),
			"summary":    store.SanitizeText(it.Summary),
			"tags":       ":process:",
			"domain":     "main",
			"confidence": 0.5,
			"status":     "pending",
			"origin":     "import:" + it.File,
		}
		rec["content_hash"] = store.HashCanonical(rec)
		if h, _ := rec["content_hash"].(string); h != "" {
			if exists, err := st.ExistsHash(ctx, h); err == nil && exists {
				continue // dedup — 재임포트 멱등
			}
		}
		if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err == nil {
			inserted++
		}
	}
	fmt.Fprintf(stdout, `{"status":"imported","inserted":%d,"skipped":%d}`+"\n", inserted, len(items)-inserted)
	return 0
}

// stripImportNoise — HTML 주석 제거(코드 펜스 내부는 문자열로만 취급).
func stripImportNoise(s string) string {
	var b strings.Builder
	inComment := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "<!--") {
			inComment = true
		}
		if !inComment {
			b.WriteString(line + "\n")
		}
		if strings.HasSuffix(t, "-->") {
			inComment = false
		}
	}
	return b.String()
}

// truncateRunesImport — rune 상한 절단.
func truncateRunesImport(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
