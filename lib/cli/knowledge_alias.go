package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

type (
	UsageStore       = knowledge.UsageStore
	UsageCountStore  = knowledge.UsageCountStore
	DupIDStore       = knowledge.DupIDStore
	DupMetaStore     = knowledge.DupMetaStore
	DupMeta          = knowledge.DupMeta
	ReadEventStore   = knowledge.ReadEventStore
	HotEventRecorder = knowledge.HotEventRecorder
)

var (
	runWrite         = knowledge.RunWrite
	runImport        = knowledge.RunImport
	recordDups       = knowledge.RecordDups
	writeThresholds  = knowledge.WriteThresholds
	ParseSourceFlag  = knowledge.ParseSourceFlag
	ErrInvalidSource = knowledge.ErrInvalidSource
)

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
