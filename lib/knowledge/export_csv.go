// export_csv.go — v1.3.0 T6: flint export --format csv 포맷터.
// encoding/csv 표준, 헤더행 포함, LF 개행. 열 10개 — content 제외
// (장문 개행 셀 회피 — 목록 분석용 인덱스). tags는 `|` 결합.
package knowledge

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

// FormatCSV는 원천 ExportRow(T2 export.go)를 소비한다.

// formatCSVHeader — 열 순서 계약(10열, content 제외).
var formatCSVHeader = []string{
	"id", "topic", "kind", "status", "confidence",
	"tags", "source", "created_at", "updated_at", "superseded_by",
}

// FormatCSV — rows를 CSV(헤더행 + 행별 레코드, LF)로 직렬화한다.
// 순수 함수: 원천 조회(T2)·파일 기록(T7)과 무관.
func FormatCSV(rows []ExportRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf) // 기본 UseCRLF=false → LF
	if err := w.Write(formatCSVHeader); err != nil {
		return nil, err
	}
	for _, r := range rows {
		superseded := ""
		if r.SupersededBy != nil {
			superseded = strconv.Itoa(*r.SupersededBy)
		}
		rec := []string{
			strconv.Itoa(r.ID),
			r.Topic,
			r.Kind,
			r.Status,
			strconv.FormatFloat(r.Confidence, 'g', -1, 64),
			strings.Join(r.Tags, "|"),
			r.Source,
			r.CreatedAt.Format(time.RFC3339),
			r.UpdatedAt.Format(time.RFC3339),
			superseded,
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
