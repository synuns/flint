package knowledge

import (
	"strings"
	"testing"
	"time"
)

// export_txt_test.go — v1.3.0 T5: FormatTXT 계약 테스트.
// 구분선 `==== #<id> <topic> ====` + 본문(content 원문 — 개행 정규화 없음).
// 메타 최소(id·topic만 — LLM 주입·grep 용도). 원천 행은 합성 데모 데이터.

func txtTestTime() time.Time {
	return time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
}

func txtTestRows() []ExportRow {
	t0 := txtTestTime()
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
			SupersededBy: nil,
		},
	}
}

func TestFormatTXTRows(t *testing.T) {
	out := string(FormatTXT(txtTestRows()))
	want1 := "==== #88 배포 관례 ===="
	want2 := "==== #12 회고 형식 ===="
	if !strings.Contains(out, want1) {
		t.Errorf("구분선1 누락: %q 미포함", want1)
	}
	if !strings.Contains(out, want2) {
		t.Errorf("구분선2 누락: %q 미포함", want2)
	}
	if strings.Index(out, want1) > strings.Index(out, want2) {
		t.Error("행 순서 위반 — id 88 구분선이 12 뒤에 있음")
	}
	if !strings.Contains(out, "main 브랜치는 항상 green이어야 한다") {
		t.Error("본문1 누락")
	}
	if !strings.Contains(out, "회고는 성공/실패/시도 순서로 기록한다") {
		t.Error("본문2 누락")
	}
	// 메타 최소 — kind·status·tags·confidence는 txt에 없어야 한다.
	for _, meta := range []string{"rule", "invalidated", "deploy", "0.9", "hook"} {
		if strings.Contains(out, meta) {
			t.Errorf("메타 %q이(가) txt 출력에 존재 — 메타 최소 위반", meta)
		}
	}
}

func TestFormatTXTContentVerbatim(t *testing.T) {
	rows := []ExportRow{{
		ID:      7,
		Topic:   "원문 보존",
		Content: "첫 줄\r\n둘째 줄  끝공백 .\n세째 \t줄",
		Kind:    "note", Status: "active",
		CreatedAt: txtTestTime(), UpdatedAt: txtTestTime(),
	}}
	out := string(FormatTXT(rows))
	if !strings.Contains(out, "첫 줄\r\n둘째 줄  끝공백 .\n세째 \t줄") {
		t.Errorf("content 원문 미보존:\n%q", out)
	}
}

func TestFormatTXTEmpty(t *testing.T) {
	if out := FormatTXT(nil); len(out) != 0 {
		t.Errorf("빈 결과 = %q, want 빈 바이트", out)
	}
	if out := FormatTXT([]ExportRow{}); len(out) != 0 {
		t.Errorf("빈 슬라이스 결과 = %q, want 빈 바이트", out)
	}
}
