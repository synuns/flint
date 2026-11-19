// asof.go — the canonical as-of normalization implementation moved to lib/wire/asof.go (shared
// Store utilities — consumed by the knowledge.read, ops.diff, and ops.status seams). This file
// holds only compatibility aliases for remaining cli consumers (asof_test real-DB regression, root.go seam injection).
package cli

import (
	"github.com/synuns/flint/lib/wire"
)

// Function/var aliases — re-exported without edits. ErrInvalidAsOf stays the same instance,
// so errors.Is checks are unchanged.
var (
	ErrInvalidAsOf = wire.ErrInvalidAsOf
	ParseRelative  = wire.ParseRelative
	ParseAsOfSpec  = wire.ParseAsOfSpec
)

// asofMinPrefix — minimum hash-prefix length (original wire.AsofMinPrefix).
const asofMinPrefix = wire.AsofMinPrefix
