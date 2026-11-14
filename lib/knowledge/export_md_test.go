// export_md_test.go — v1.3.0 T4: FormatMD 구조 단언(헤더·섹션 수·
// 메타 라인 형식·계보 링크)·빈 결과(헤더만)·개행 포함 content 원문.
package knowledge

import (
	"bytes"
	"regexp"
	"testing"
	"time"
)

func mdSampleRows() []ExportRow {
	superseded := 90
	return []ExportRow{
		{
			ID:           88,
			Topic:        "배포 관례",
			Content:      "본문 1줄",
			Kind:         "rule",
			Status:       "active",
			Confidence:   0.9,
			Tags:         []string{"deploy", "ops"},
			Entities:     []string{"deploy"},
			Domain:       "ops",
			Source:       "manual",
			ContentHash:  "abc123",
			CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
			SupersededBy: &superseded,
		},
		{
			ID:         91,
			Topic:      "리뷰 관례",
			Content:    "본문 2행",
			Kind:       "rule",
			Status:     "active",
			Confidence: 0.8,
			Tags:       []string{"review"},
			Source:     "hook",
			CreatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
			UpdatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		},
	}
}

// 헤더(`# flint export`·생성일 UTC·항목 수)·섹션 수·메타 라인 형식·
// 계보 링크(`→ #N`) 단언.
func TestFormatMDStructure(t *testing.T) {
	out := FormatMD(mdSampleRows())
	s := string(out)

	if !bytes.HasPrefix(out, []byte("# flint export\n")) {
		t.Fatalf("헤더 라인 누락: %q", firstLine(s))
	}
	if !regexp.MustCompile(`(?m)^created: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(s) {
		t.Errorf("생성일(UTC RFC3339) 라인 누락:\n%s", s)
	}
	if want := "items: 2"; !bytes.Contains(out, []byte(want)) {
		t.Errorf("항목 수 라인 %q 누락", want)
	}
	if got := bytes.Count(out, []byte("\n## #")); got != 2 {
		t.Errorf("섹션 수 = %d, want 2", got)
	}

	wantMeta1 := "status: active · confidence: 0.9 · tags: deploy|ops · source: manual · created: 2026-01-02T03:04:05Z · superseded_by: → #90"
	if !bytes.Contains(out, []byte(wantMeta1)) {
		t.Errorf("메타 라인(계보 링크 포함) 불일치:\nwant %q\n전체:\n%s", wantMeta1, s)
	}
	wantMeta2 := "status: active · confidence: 0.8 · tags: review · source: hook · created: 2026-03-04T05:06:07Z"
	if !bytes.Contains(out, []byte(wantMeta2)) {
		t.Errorf("메타 라인(superseded 없음) 불일치:\nwant %q", wantMeta2)
	}
	if !bytes.Contains(out, []byte("## #88 배포 관례")) {
		t.Errorf("섹션 헤딩 `## #88 배포 관례` 누락")
	}
}

// superseded_by가 nil이면 메타 라인에 해당 조각이 없어야 한다.
func TestFormatMDNoSupersededLeak(t *testing.T) {
	out := string(FormatMD(mdSampleRows()))
	if n := regexp.MustCompile(`superseded_by: → #`).FindAllString(out, -1); len(n) != 1 {
		t.Errorf("superseded_by 링크 개수 = %d, want 1(nil은 미출력):\n%s", len(n), out)
	}
}

// 빈 결과 — 헤더만(items: 0), 섹션 없음.
func TestFormatMDEmpty(t *testing.T) {
	out := FormatMD(nil)
	s := string(out)
	if !bytes.HasPrefix(out, []byte("# flint export\n")) {
		t.Fatalf("빈 결과에도 헤더 필요: %q", s)
	}
	if !bytes.Contains(out, []byte("items: 0")) {
		t.Errorf("빈 결과 items: 0 누락:\n%s", s)
	}
	if bytes.Contains(out, []byte("\n## #")) {
		t.Errorf("빈 결과에 섹션 존재:\n%s", s)
	}
}

// content는 원문 그대로(개행 보존) — 노트앱 이식 전제.
func TestFormatMDContentVerbatim(t *testing.T) {
	rows := []ExportRow{{
		ID:        7,
		Topic:     "C# 네이밍 #관례", // 헤딩 충돌 문자 — 그대로 출력
		Content:   "1줄\n2줄\n\n4줄",
		Status:    "active",
		Source:    "manual",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
	out := string(FormatMD(rows))
	if !bytes.Contains([]byte(out), []byte("## #7 C# 네이밍 #관례")) {
		t.Errorf("topic 헤딩 원문 누락:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("1줄\n2줄\n\n4줄")) {
		t.Errorf("content 개행 원문 미보존:\n%q", out)
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
