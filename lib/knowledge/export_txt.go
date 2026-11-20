// export_txt.go — the flint export --format txt formatter.
// Per item: a delimiter line `==== #<id> <topic> ====` + the body (content
// verbatim — no newline normalization). Minimal metadata (id·topic only —
// LLM injection and grep use cases).
package knowledge

import (
	"strconv"
	"strings"
)

// FormatTXT — serializes rows into greppable plain text.
// Item shape: one delimiter line + content verbatim + a blank line (item
// separation). Pure function: independent of source queries (T2) and file
// writing (T7). Empty rows yield empty bytes.
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
