// export_txt.go — v1.3.0 T5: flint export --format txt 포맷터.
// 항목당 구분선 `==== #<id> <topic> ====` + 본문(content 원문 — 개행
// 정규화 없음). 메타 최소(id·topic만 — LLM 주입·grep 용도).
package knowledge

import (
	"strconv"
	"strings"
)

// FormatTXT — rows를 grep 가능한 플레인 텍스트로 직렬화한다.
// 항목 형식: 구분선 1줄 + content 원문 + 빈 줄(항목 구분). 순수 함수:
// 원천 조회(T2)·파일 기록(T7)과 무관. rows가 비면 빈 바이트.
func FormatTXT(rows []ExportRow) []byte {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString("==== #")
		b.WriteString(strconv.Itoa(r.ID))
		b.WriteString(" ")
		b.WriteString(r.Topic)
		b.WriteString(" ====\n")
		b.WriteString(r.Content)
		b.WriteString("\n\n")
	}
	return []byte(b.String())
}
