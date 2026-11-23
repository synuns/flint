// Package data — the embedded seed-data source. seed.jsonl stays at the
// module root (the Makefile `seed` target reads it from disk — the ruling
// keeps both routes alive); this package embeds the same file so the release
// binary can seed the isolated demo database without a checkout
// (v1.26.0 T2 `flint seed --demo`).
package data

import _ "embed"

// seedJSONL — the embedded seed.jsonl bytes (build-time snapshot).
//
//go:embed seed.jsonl
var seedJSONL []byte

// SeedJSONL — the raw seed.jsonl bytes. Callers parse one JSON object per
// non-empty line; the file is synthetic demo data only (never real rows).
func SeedJSONL() []byte { return seedJSONL }
