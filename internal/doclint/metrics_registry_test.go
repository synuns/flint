package doclint

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

func registryKindTokens() map[string]bool {
	tokens := map[string]bool{}
	for _, g := range db.MeteringKindRegistry {
		for _, k := range g.Kinds {
			tokens[g.Name+":"+k] = true
		}
	}
	return tokens
}

func masterListSection(t *testing.T, doc string) string {
	t.Helper()
	const head = "## 7. Raw data source master list"
	start := strings.Index(doc, head)
	if start < 0 {
		t.Fatalf("METRICS.md에 %q 섹션 없음", head)
	}
	rest := doc[start+len(head):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func section4_9(t *testing.T, doc string) string {
	t.Helper()
	start := strings.Index(doc, "## 4.9")
	if start < 0 {
		t.Fatal("METRICS.md에 §4.9 섹션 없음")
	}
	rest := doc[start:]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestMetricsRegistrySync(t *testing.T) {
	doc := read(t, "METRICS.md")
	sec := masterListSection(t, doc)

	reg := registryKindTokens()
	for tok := range reg {
		if !strings.Contains(sec, "`"+tok+"`") {
			t.Errorf("METRICS.md 총목록에 등록표 키 `%s` 행 없음 — 누락", tok)
		}
	}

	tokRe := regexp.MustCompile("`([a-z]+:[a-z_]+)`")
	for _, m := range tokRe.FindAllStringSubmatch(sec, -1) {
		tok := m[1]
		if !reg[tok] {
			t.Errorf("METRICS.md 총목록에 등록표에 없는 키 `%s` 존재 — 과잉", tok)
		}
	}
}

func TestStaleFiveKeysFixed(t *testing.T) {
	doc := read(t, "METRICS.md")
	sec := section4_9(t, doc)

	stale := []string{
		"correction_preference", "correction_fact",
		"merge_audit_pairs", "recall_audit_listed", "recall_audit_disposed",
	}
	for _, k := range stale {
		if !strings.Contains(sec, k) {
			t.Errorf("METRICS.md §4.9 A5에 스테일 키 %s 미반영", k)
		}
	}

	total := 0
	counts := map[string]int{}
	for _, g := range db.MeteringKindRegistry {
		counts[g.Name] = len(g.Kinds)
		total += len(g.Kinds)
	}
	for _, want := range []string{
		fmt.Sprintf("%d keys", total),
		fmt.Sprintf("usage %d", counts["usage"]),
		fmt.Sprintf("curation %d", counts["curation"]),
		fmt.Sprintf("bytes %d", counts["bytes"]),
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("METRICS.md §4.9 A5 키 수 기술이 등록표와 불일치 — %q 없음", want)
		}
	}
	if strings.Contains(sec, "curation 11") {
		t.Error("METRICS.md §4.9 A5에 스테일 개수(curation 11) 잔존")
	}
}

func TestRetentionColumn(t *testing.T) {
	doc := read(t, "METRICS.md")
	sec := masterListSection(t, doc)

	found := false
	for _, line := range strings.Split(sec, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "kind") && strings.Contains(lower, "retention") {
			found = true
			break
		}
	}
	if !found {
		t.Error("METRICS.md 총목록 표 헤더에 보존 기간(retention) 열 없음")
	}
}
