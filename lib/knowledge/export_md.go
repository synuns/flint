// export_md.go — the flint export --format md formatter.
// File header (`# flint export`, created-at UTC, item count) + a section per
// item (`## #<id> <topic>` + meta line + content verbatim). Designed for
// note-app portability.
package knowledge

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// FormatMD — serializes rows into Markdown. Pure function: independent of
// source queries (T2) and file writing (T7). Content is emitted verbatim
// including newlines; heading-collision characters in topics (# etc.) are
// printed as-is (section headings are the fixed `## ` prefix).
// superseded_by omits the fragment entirely when nil (or unset).
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
