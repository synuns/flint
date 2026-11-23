package render

import (
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

func Text(s string) string {
	return store.SanitizeText(s)
}

func Masked(s string) string {
	return store.SanitizeText(store.MaskSecrets(s))
}

func MaskedStrict(s string) string {
	return store.SanitizeText(store.MaskSecretsStrict(s))
}

func Cells(rows [][]any) [][]any {
	for _, row := range rows {
		for i, cell := range row {
			if s, ok := cell.(string); ok {
				row[i] = store.SanitizeText(s)
			}
		}
	}
	return rows
}

func Rows(rows [][]any, fields string, jsonMode bool) string {
	return query.FormatRows(Cells(rows), fields, jsonMode)
}
