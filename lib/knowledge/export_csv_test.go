package knowledge

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// export_csv_test.go — v1.3.0 T6: FormatCSV 계약 테스트.
// 열 10개(content 제외 — 목록 인덱스 용도)·헤더행 포함·LF 개행·
// tags는 `|` 결합. 원천 행은 합성 데모 데이터(개인 데이터 금지).

func csvTestTime() time.Time {
	return time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
}

func csvTestRows() []ExportRow {
	t0 := csvTestTime()
	sup := 88
	return []ExportRow{
		{
			ID:           88,
			Topic:        "배포 관례",
			Content:      "main 브랜치는 항상 green이어야 한다",
			Kind:         "rule",
			Status:       "active",
			Confidence:   0.9,
			Tags:         []string{"deploy", "ops"},
			Entities:     []string{"main"},
			Domain:       "demo",
			Source:       "manual",
			ContentHash:  "aa11",
			CreatedAt:    t0,
			UpdatedAt:    t0,
			SupersededBy: nil,
		},
		{
			ID:           12,
			Topic:        "회고 형식",
			Content:      "회고는 성공/실패/시도 순서로 기록한다",
			Kind:         "note",
			Status:       "invalidated",
			Confidence:   0.5,
			Tags:         []string{"meeting"},
			Entities:     nil,
			Domain:       "demo",
			Source:       "hook",
			ContentHash:  "bb22",
			CreatedAt:    t0,
			UpdatedAt:    t0,
			SupersededBy: &sup,
		},
	}
}

// csvRecords — 출력을 csv.Reader로 왕복 파싱(이스케이프 검증 포함).
func csvRecords(t *testing.T, out []byte) [][]string {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("csv 파싱 실패: %v", err)
	}
	return recs
}

func TestFormatCSVHeaderAndRows(t *testing.T) {
	out, err := FormatCSV(csvTestRows())
	if err != nil {
		t.Fatalf("FormatCSV err = %v", err)
	}
	recs := csvRecords(t, out)
	if len(recs) != 3 {
		t.Fatalf("레코드 수 = %d, want 3(헤더+2행)", len(recs))
	}
	wantHeader := []string{"id", "topic", "kind", "status", "confidence",
		"tags", "source", "created_at", "updated_at", "superseded_by"}
	for i, h := range wantHeader {
		if recs[0][i] != h {
			t.Errorf("헤더[%d] = %q, want %q", i, recs[0][i], h)
		}
	}
	r1 := recs[1]
	if r1[0] != "88" || r1[1] != "배포 관례" || r1[2] != "rule" || r1[3] != "active" {
		t.Errorf("행1 앞 열 = %v", r1[:4])
	}
	if r1[4] != "0.9" {
		t.Errorf("행1 confidence = %q, want 0.9", r1[4])
	}
	if r1[5] != "deploy|ops" {
		t.Errorf("행1 tags = %q, want deploy|ops", r1[5])
	}
	if r1[6] != "manual" {
		t.Errorf("행1 source = %q, want manual", r1[6])
	}
	if want := csvTestTime().Format(time.RFC3339); r1[7] != want || r1[8] != want {
		t.Errorf("행1 timestamps = %q,%q want %q", r1[7], r1[8], want)
	}
	if r1[9] != "" {
		t.Errorf("행1 superseded_by = %q, want 빈 값", r1[9])
	}
	if recs[2][9] != "88" {
		t.Errorf("행2 superseded_by = %q, want 88", recs[2][9])
	}
}

func TestFormatCSVEscapeNewlineAndComma(t *testing.T) {
	rows := []ExportRow{{
		ID: 7, Topic: "개행, 쉼표 포함", Kind: "note", Status: "active",
		Confidence: 1, Tags: []string{"a", "b"}, Source: "mcp",
		CreatedAt: csvTestTime(), UpdatedAt: csvTestTime(),
	}}
	out, err := FormatCSV(rows)
	if err != nil {
		t.Fatalf("FormatCSV err = %v", err)
	}
	if !strings.Contains(string(out), `"`) {
		t.Error("특수문자 셀이 따옴표로 이스케이프되지 않음")
	}
	recs := csvRecords(t, out)
	if recs[1][1] != "개행, 쉼표 포함" {
		t.Errorf("왕복 파싱 후 topic = %q", recs[1][1])
	}
}

func TestFormatCSVColumnCountFixed(t *testing.T) {
	out, err := FormatCSV(csvTestRows())
	if err != nil {
		t.Fatalf("FormatCSV err = %v", err)
	}
	for i, rec := range csvRecords(t, out) {
		if len(rec) != 10 {
			t.Errorf("레코드[%d] 열 수 = %d, want 10", i, len(rec))
		}
	}
}

func TestFormatCSVEmptyRowsHeaderOnly(t *testing.T) {
	out, err := FormatCSV(nil)
	if err != nil {
		t.Fatalf("FormatCSV err = %v", err)
	}
	recs := csvRecords(t, out)
	if len(recs) != 1 || recs[0][0] != "id" {
		t.Fatalf("빈 결과 = %v, want 헤더만", recs)
	}
}

func TestFormatCSVLFDelimiters(t *testing.T) {
	out, err := FormatCSV(csvTestRows())
	if err != nil {
		t.Fatalf("FormatCSV err = %v", err)
	}
	if bytes.ContainsRune(out, '\r') {
		t.Error("CRLF 포함 — LF만 계약 위반")
	}
}
