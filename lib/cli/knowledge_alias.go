// knowledge_alias.go — re-exports the write, import,
// read, and hot surfaces moved to lib/knowledge as lowercase var and type aliases so remaining
// cli files and residual tests compile without edits. The var aliases
// carry over the test seam-swap convention (lowercase var). Initialization of the knowledge
// injection seam is owned by the root.go composition root.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// Type aliases — interface implementations, embedding, and fake assertions unchanged (the
// realStore assertion in usage_wire.go, the read.go ReadUsageStore contract comment, etc.).
type (
	UsageStore       = knowledge.UsageStore
	UsageCountStore  = knowledge.UsageCountStore
	DupIDStore       = knowledge.DupIDStore
	DupMetaStore     = knowledge.DupMetaStore
	DupMeta          = knowledge.DupMeta
	ReadEventStore   = knowledge.ReadEventStore
	HotEventRecorder = knowledge.HotEventRecorder
)

// Function/var aliases — compatible with lowercase calls in consumer files (runWrite in
// compact, writeThresholds in done/doctor/..., recordUsage in brief/hot/read, recordDups in the
// dup_v0102 test, etc.) and existing public names (ParseSourceFlag, ErrInvalidSource).
var (
	runWrite         = knowledge.RunWrite
	runImport        = knowledge.RunImport
	recordDups       = knowledge.RecordDups
	writeThresholds  = knowledge.WriteThresholds
	ParseSourceFlag  = knowledge.ParseSourceFlag
	ErrInvalidSource = knowledge.ErrInvalidSource
)

// Aliases letting remaining cli consumers (report, setup, update and
// root.go seam injection) and residual tests compile without edits.
var (
	runHot                 = knowledge.RunHot
	runRead                = knowledge.RunRead
	HotBudget              = knowledge.HotBudget
	hotFields              = knowledge.HotFields
	hotHeadline            = knowledge.HotHeadline
	hotTopics              = knowledge.HotTopics
	pendingAgeSuffix       = knowledge.PendingAgeSuffix
	bumpReadFallbackToMeta = knowledge.BumpReadFallbackToMeta
	parseIDsCSV            = knowledge.ParseIDsCSV
	rowIDs                 = knowledge.RowIDs
	readSuggestEligible    = knowledge.ReadSuggestEligible
	firstExactRank         = knowledge.FirstExactRank
)
