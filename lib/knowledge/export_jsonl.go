// export_jsonl.go — the flint export --format jsonl formatter.
// One JSON object per line, LF-separated. Full snapshot of every field —
// re-import is the consumer. snake_case keys share the csv·md contract.
package knowledge

import (
	"bytes"
	"encoding/json"
	"time"
)

// exportJSONLRow — the jsonl serialization view of ExportRow (snake_case
// tags). The ExportRow original (a proxy before any accrual lands) carries no
// tags. The two score fields are omitempty (additive) — rows with 0/NULL keep
// the existing golden output and key composition, and only rows with values
// emit the keys (legacy jsonl parsing treats absent as 0/NULL fallback).
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

// toExportJSONLRow — converts an ExportRow to the snake_case serialization
// view. Identical field layout, so a type conversion suffices (no per-field
// copy needed).
func toExportJSONLRow(r ExportRow) exportJSONLRow {
	return exportJSONLRow(r)
}

// FormatJSONL — serializes rows into jsonl (one JSON object per line + LF).
// Pure function: independent of source queries and file writing. Empty input
// yields empty bytes.
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
