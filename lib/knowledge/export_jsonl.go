package knowledge

import (
	"bytes"
	"encoding/json"
	"time"
)

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

func toExportJSONLRow(r ExportRow) exportJSONLRow {
	return exportJSONLRow(r)
}

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
