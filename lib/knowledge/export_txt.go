package knowledge

import (
	"strconv"
	"strings"
)

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
