// export_md.go — v1.3.0 T4: flint export --format md 포맷터.
// 파일 헤더(`# flint export`·생성일 UTC·항목 수) + 항목별 섹션
// (`## #<id> <topic>` + 메타 라인 + content 원문). 노트앱 이식 전제.
package knowledge

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// FormatMD — rows를 Markdown으로 직렬화한다. 순수 함수: 원천 조회(T2)·
// 파일 기록(T7)과 무관. content는 개행 포함 원문 그대로, topic의 헤딩
// 충돌 문자(# 등)도 그대로 출력(섹션 헤딩은 `## ` 고정).
// superseded_by는 nil(또는 미설정)일 때 조각 자체를 생략한다.
func FormatMD(rows []ExportRow) []byte {
	var b bytes.Buffer
	b.WriteString("# flint export\n\n")
	b.WriteString("created: " + time.Now().UTC().Format(time.RFC3339) + "\n")
	b.WriteString("items: " + strconv.Itoa(len(rows)) + "\n")
	for _, r := range rows {
		b.WriteString("\n## #" + strconv.Itoa(r.ID) + " " + r.Topic + "\n\n")
		tags := strings.Join(r.Tags, "|")
		if tags == "" {
			tags = "-"
		}
		b.WriteString("status: " + r.Status +
			" · confidence: " + strconv.FormatFloat(r.Confidence, 'g', -1, 64) +
			" · tags: " + tags +
			" · source: " + r.Source +
			" · created: " + r.CreatedAt.Format(time.RFC3339))
		if r.SupersededBy != nil {
			b.WriteString(" · superseded_by: → #" + strconv.Itoa(*r.SupersededBy))
		}
		b.WriteString("\n\n")
		b.WriteString(r.Content)
		b.WriteString("\n")
	}
	return b.Bytes()
}
