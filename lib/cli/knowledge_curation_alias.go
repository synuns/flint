// knowledge_curation_alias.go — residual cli-consumer compatibility
// aliases for the wave-3 (brief, pending, approve, done, compact) surfaces moved to
// lib/knowledge. Ruling: the lowercase countPending var in render_alias.go is
// re-pointed at knowledge.CountPending (not redeclared in this file —
// no double declaration). Initialization of the knowledge injection seam is owned by the root.go
// composition root.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// Function/var aliases — let remaining cli consumer files (the direct runCompact call in
// compact_test, the oneLine convention-delegation equivalence check in textutil_test, the
// consumer-side bumpMetaCounter check in meta_kv_test) compile without edits.
var (
	runCompact      = knowledge.RunCompact
	oneLine         = knowledge.OneLine
	bumpMetaCounter = knowledge.BumpMetaCounter
)
