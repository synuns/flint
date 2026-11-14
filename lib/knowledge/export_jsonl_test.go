// export_jsonl_test.go — v1.3.0 T3: FormatJSONL 계약 테스트.
// 행당 1 JSON 객체·개행 구분·snake_case 필드·완전 스냅샷 보존.
package knowledge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func jsonlFixtureRows() []ExportRow {
	sup := 42
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)
	return []ExportRow{
		{
			ID: 7, Topic: "demo-topic", Content: "첫 번째 데모 지식 행",
			Kind: "lesson", Status: "knowledge", Confidence: 0.75,
			Tags:     []string{"go", "export"},
			Entities: []string{"flint", "jsonl"},
			Domain:   "demo", Source: "demo-seed",
			ContentHash: "abc123", CreatedAt: t0, UpdatedAt: t1,
			SupersededBy: &sup,
		},
		{
			ID: 8, Topic: "demo-topic-2", Content: "줄바꿈 없는 두 번째 행",
			Kind: "note", Status: "pending", Confidence: 0.5,
			Tags: nil, Entities: []string{},
			Domain: "", Source: "",
			ContentHash: "def456", CreatedAt: t1, UpdatedAt: t1,
		},
	}
}

// TestFormatJSONLTwoRowsTwoLines — 2행 입력은 정확히 2개의
// 개행 종결 라인을 낸다.
func TestFormatJSONLTwoRowsTwoLines(t *testing.T) {
	out, err := FormatJSONL(jsonlFixtureRows())
	if err != nil {
		t.Fatalf("FormatJSONL error: %v", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("라인 수 = %d, want 2", len(lines))
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Fatalf("출력이 개행으로 종결되지 않음: %q", out)
	}
	for i, ln := range lines {
		if strings.Contains(ln, "\n") {
			t.Fatalf("라인 %d에 개행 포함: %q", i, ln)
		}
		if !json.Valid([]byte(ln)) {
			t.Fatalf("라인 %d가 유효 JSON 아님: %q", i, ln)
		}
	}
}

// TestFormatJSONLRoundTripPreservesAllFields — json.Unmarshal 왕복으로
// 전 필드(완전 스냅샷)가 보존됨을 검증한다.
func TestFormatJSONLRoundTripPreservesAllFields(t *testing.T) {
	rows := jsonlFixtureRows()
	out, err := FormatJSONL(rows)
	if err != nil {
		t.Fatalf("FormatJSONL error: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(rows) {
		t.Fatalf("라인 수 불일치: %d vs %d", len(lines), len(rows))
	}
	for i, ln := range lines {
		var got exportJSONLRow
		if err := json.Unmarshal([]byte(ln), &got); err != nil {
			t.Fatalf("라인 %d Unmarshal 실패: %v", i, err)
		}
		want := toExportJSONLRow(rows[i])
		if got.ID != want.ID {
			t.Errorf("행 %d id = %d, want %d", i, got.ID, want.ID)
		}
		if got.Topic != want.Topic {
			t.Errorf("행 %d topic = %q, want %q", i, got.Topic, want.Topic)
		}
		if got.Content != want.Content {
			t.Errorf("행 %d content 불일치", i)
		}
		if got.Kind != want.Kind || got.Status != want.Status {
			t.Errorf("행 %d kind/status 불일치: %q/%q", i, got.Kind, got.Status)
		}
		if got.Confidence != want.Confidence {
			t.Errorf("행 %d confidence = %v, want %v", i, got.Confidence, want.Confidence)
		}
		if len(got.Tags) != len(want.Tags) {
			t.Errorf("행 %d tags 길이 = %d, want %d", i, len(got.Tags), len(want.Tags))
		}
		if len(got.Entities) != len(want.Entities) {
			t.Errorf("행 %d entities 길이 = %d, want %d", i, len(got.Entities), len(want.Entities))
		}
		if got.Domain != want.Domain || got.Source != want.Source {
			t.Errorf("행 %d domain/source 불일치", i)
		}
		if got.ContentHash != want.ContentHash {
			t.Errorf("행 %d content_hash 불일치", i)
		}
		if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
			t.Errorf("행 %d 타임스탬프 불일치", i)
		}
		if (got.SupersededBy == nil) != (want.SupersededBy == nil) {
			t.Fatalf("행 %d superseded_by nil 불일치", i)
		}
		if got.SupersededBy != nil && *got.SupersededBy != *want.SupersededBy {
			t.Errorf("행 %d superseded_by = %d, want %d", i, *got.SupersededBy, *want.SupersededBy)
		}
	}
}

// TestFormatJSONLSnakeCaseKeys — 직렬화 키가 snake_case 계약
// (id·…·content_hash·superseded_by)을 정확히 따른다.
func TestFormatJSONLSnakeCaseKeys(t *testing.T) {
	out, err := FormatJSONL(jsonlFixtureRows()[:1])
	if err != nil {
		t.Fatalf("FormatJSONL error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &m); err != nil {
		t.Fatalf("Unmarshal 실패: %v", err)
	}
	want := []string{
		"id", "topic", "content", "kind", "status", "confidence",
		"tags", "entities", "domain", "source",
		"content_hash", "created_at", "updated_at", "superseded_by",
	}
	if len(m) != len(want) {
		t.Fatalf("키 수 = %d, want %d: %v", len(m), len(want), m)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("키 %q 누락", k)
		}
	}
}

// TestFormatJSONLEscapesSpecialChars — 개행·따옴표·한글이 포함된
// content도 단일 라인으로 이스케이프된다.
func TestFormatJSONLEscapesSpecialChars(t *testing.T) {
	rows := []ExportRow{{
		ID: 9, Topic: "특수 \"따옴표\" 주제",
		Content: "1줄\n2줄 \"quoted\" 한글 지식\t탭",
		Kind:    "lesson", Status: "knowledge", Confidence: 0.9,
		ContentHash: "hash9",
		CreatedAt:   time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
	}}
	out, err := FormatJSONL(rows)
	if err != nil {
		t.Fatalf("FormatJSONL error: %v", err)
	}
	nl := bytes.Count(out, []byte("\n"))
	if nl != 1 {
		t.Fatalf("물리 개행 수 = %d, want 1 (개행은 이스케이프되어야 함)", nl)
	}
	var got exportJSONLRow
	if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
		t.Fatalf("Unmarshal 실패: %v", err)
	}
	if got.Content != rows[0].Content {
		t.Errorf("왕복 content 불일치: %q", got.Content)
	}
	if got.Topic != rows[0].Topic {
		t.Errorf("왕복 topic 불일치: %q", got.Topic)
	}
}

// TestFormatJSONLEmpty — 빈 slice는 빈 바이트·nil 오류.
func TestFormatJSONLEmpty(t *testing.T) {
	out, err := FormatJSONL([]ExportRow{})
	if err != nil {
		t.Fatalf("빈 입력 오류: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("빈 입력 출력 길이 = %d, want 0", len(out))
	}
}
