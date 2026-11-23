package cli

import (
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

var (
	Text          = render.Text
	Masked        = render.Masked
	Cells         = render.Cells
	Rows          = render.Rows
	TruncateRunes = render.TruncateRunes
	CountPending  = knowledge.CountPending
)

var (
	truncateRunes = render.TruncateRunes
	countPending  = knowledge.CountPending
	cellToInt     = render.CellToInt
)
