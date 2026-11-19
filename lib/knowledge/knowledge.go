// Package knowledge — flint knowledge intake and recall command implementations
// (v0.39.0 §2 waves 1 and 2). Owns the write·import·read·hot handlers and the
// shared surfaces for intake metering (RecordUsage·RecordDups), decision
// threshold loading (WriteThresholds), and sanitization (SanitizePayload).
// Command registration happens in init via wire.Register — cli imports this
// package (cli→knowledge one-way; knowledge importing cli would be a cycle),
// and residual cli consumers compile unmodified through the lowercase var and
// type aliases in cli/knowledge_alias.go.
package knowledge

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("write", RunWrite)
	wire.Register("import", RunImportRouted) // --format presence branch (default RunImport unchanged)
	wire.Register("read", RunRead)
	wire.Register("hot", RunHot)
}
