// export_jsonl.go — flint export --format jsonl 포맷터.
// 행당 1 JSON 객체, LF 개행 구분. 전 필드 완전 스냅샷 — 재이식이
// 소비. snake_case 키는 csv·md와 동일 계약.
package knowledge

import (
	"bytes"
	"encoding/json"
	"time"
)

// exportJSONLRow — ExportRow의 jsonl 직렬화 뷰(snake_case 태그).
// ExportRow 원본(현재 적립 전 프록시)에는 태그를 두지 않는다.
// 스코어 2필드는 omitempty(additive) — 0/NULL 행은 기존 골든
// 출력·키 구성을 그대로 두고, 값 있는 행만 키를 발행한다(레거시 jsonl
// 파싱은 미지정 → 0/NULL 폴백).
type exportJSONLRow struct {
	ID             int        `json:"id"`
	Topic          string     `json:"topic"`
	Content        string     `json:"content"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Confidence     float64    `json:"confidence"`
	Tags           []string   `json:"tags"`
	Entities       []string   `json:"entities"`
	Domain         string     `json:"domain"`
	Source         string     `json:"source"`
	ContentHash    string     `json:"content_hash"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ValidAt        time.Time  `json:"valid_at"`
	SupersededBy   *int       `json:"superseded_by"`
	RecallScore    int        `json:"recall_score,omitempty"`
	LastRecalledAt *time.Time `json:"last_recalled_at,omitempty"`
}

// toExportJSONLRow — ExportRow를 snake_case 직렬화 뷰로 변환.
// 동일 필드 구조체라 타입 변환으로 치환된다(필드별 복사 불필요).
func toExportJSONLRow(r ExportRow) exportJSONLRow {
	return exportJSONLRow(r)
}

// FormatJSONL — rows를 jsonl(행당 1 JSON 객체 + LF)로 직렬화한다.
// 순수 함수: 원천 조회·파일 기록과 무관. 빈 입력은 빈 바이트.
func FormatJSONL(rows []ExportRow) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(toExportJSONLRow(r)); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
