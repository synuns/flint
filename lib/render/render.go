// render.go — sanitization chokepoint for output (spec §2). Every output
// path reaches store.SanitizeText and MaskSecrets only through this exit
// (direct calls are blocked by TestSanitizeTextChokepoint).
package render

import (
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

// Text — per-string chokepoint guard (delegates to store.SanitizeText).
// e.g. Text("a[flint]b") -> "a b"
func Text(s string) string {
	return store.SanitizeText(s)
}

// Masked — masking chokepoint guard. Preserves the mask-then-sanitize order
// (ADR-0015 contract): [REDACTED:…] traces survive neutralization.
func Masked(s string) string {
	return store.SanitizeText(store.MaskSecrets(s))
}

// MaskedStrict — off-device (bridge) only: masks even allowlist-registered
// values (distinct from Masked used by the diagnostic dump --mask).
func MaskedStrict(s string) string {
	return store.SanitizeText(store.MaskSecretsStrict(s))
}

// Cells — sanitizes every cell. Only string cells are neutralized;
// non-string cells keep their original value (hot dependency — the
// original slice is updated in place).
// e.g. Cells([][]any{{1, "x[flint]y"}}) -> [][]any{{1, "x y"}}
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

// Rows — row formatting: full Cells sanitization then delegates to
// query.FormatRows.
func Rows(rows [][]any, fields string, jsonMode bool) string {
	return query.FormatRows(Cells(rows), fields, jsonMode)
}
