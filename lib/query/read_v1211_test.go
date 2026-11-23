package query

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatVaultRowsPipeStrippedAndTitleTruncated(t *testing.T) {
	title := strings.Repeat("제", 250)
	rows := [][]any{{title, "a|b", "", "no|te.md"}}

	txt := FormatVaultRows(rows, false)
	line := strings.TrimRight(txt, "\n")
	parts := strings.Split(line, "|")
	if len(parts) != 4 {
		t.Fatalf("텍스트 라인 필드 수=%d, want 4 (파이프 이스케이프): %q", len(parts), line)
	}
	if parts[0] != "vault" {
		t.Fatalf("접두=%q, want vault", parts[0])
	}
	if parts[1] != "no te.md" {
		t.Fatalf("path 셀=%q, want 'no te.md' (| 제거)", parts[1])
	}
	if got := utf8.RuneCountInString(parts[2]); got != 200 {
		t.Fatalf("title 셀 rune 수=%d, want 200", got)
	}
	if parts[3] != "a b" {
		t.Fatalf("summary 셀=%q, want 'a b' (| 제거)", parts[3])
	}

	j := FormatVaultRows(rows, true)
	var v VaultLine
	if err := json.Unmarshal([]byte(strings.TrimRight(j, "\n")), &v); err != nil {
		t.Fatalf("json 파싱: %v — %q", err, j)
	}
	if v.Path != "no|te.md" || v.Summary != "a|b" {
		t.Fatalf("json 모드 셀 변형 금지: %+v", v)
	}
	if got := utf8.RuneCountInString(v.Title); got != 200 {
		t.Fatalf("json title rune 수=%d, want 200", got)
	}
}
