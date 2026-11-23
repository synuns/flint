package data

import _ "embed"

//go:embed seed.jsonl
var seedJSONL []byte

func SeedJSONL() []byte { return seedJSONL }
