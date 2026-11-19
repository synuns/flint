// render_alias.go — aliases for the lib/render output layer (package split 1/3,
// spec §2). The render.go and textutil.go originals moved to lib/render; same-name
// var aliases keep remaining cli consumer files (read, hot, pending, brief, maintain, etc.)
// compiling without edits. Lowercase vars (truncateRunes,
// countPending) keep test seam-swap convention compatibility.
// The CountPending definition belongs to the pending domain, so it
// moved to lib/knowledge — only the alias is re-pointed at knowledge.CountPending.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

// output sanitization choke point — delegated to the single render-package implementation.
var (
	Text          = render.Text
	Masked        = render.Masked
	Cells         = render.Cells
	Rows          = render.Rows
	TruncateRunes = render.TruncateRunes
	CountPending  = knowledge.CountPending
)

// Consumer-compat lowercase aliases (no-edit convention — import, maintain, done and
// sessionhook, status, nudge call them as-is).
var (
	truncateRunes = render.TruncateRunes
	countPending  = knowledge.CountPending
	cellToInt     = render.CellToInt
)
