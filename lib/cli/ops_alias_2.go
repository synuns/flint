// ops_alias_2.go — re-exports the maintain, metrics, marker,
// nudge, and sessionhook surfaces moved to lib/ops as lowercase var, type, and constant aliases so
// remaining cli files (report, profile, update, knowledge_alias and residual tests) compile without
// edits (carrying over the ops_alias_1 convention). The var aliases carry over the test seam-swap
// convention (lowercase var) (maintainRetryDelays, exposureProfileFn —
// injected by the root.go init as swap-propagation wrappers).
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// Type aliases — interface implementations and fake assertions unchanged (MaintStore in maintain_test,
// ConflictStore in conflicts_v0131, SessionStartRecorder in sessionhook_v0200).
type (
	MaintStore           = ops.MaintStore
	ConflictStore        = ops.ConflictStore
	SessionStartRecorder = ops.SessionStartRecorder
)

// Constant aliases — residual cli consumers (suspectsDefaultLimit in maintain_v0130,
// markerKindRecall in nudge_v0181/182, nudgePendingLine in nudge_v0110, etc.).
const (
	suspectsDefaultLimit = ops.SuspectsDefaultLimit
	markerKindRecall     = ops.MarkerKindRecall
	nudgePendingLine     = ops.NudgePendingLine
	nudgeReadHint        = ops.NudgeReadHint
	nudgeRecallStartLine = ops.NudgeRecallStartLine
	nudgeReasonRecall    = ops.NudgeReasonRecall
)

// Function/var aliases — lowercase-call compatibility in consumer files plus existing public names.
var (
	// maintain
	runMaintain           = ops.RunMaintain
	dupSuspectsJSON       = ops.DupSuspectsJSON
	conflictDice          = ops.ConflictDice
	classifyConflict      = ops.ClassifyConflict
	isConflictGroup       = ops.IsConflictGroup
	conflictSummaryPrefix = ops.ConflictSummaryPrefix
	conflictDupThreshold  = ops.ConflictDupThreshold
	maintainRetryDelays   = ops.MaintainRetryDelays
	anyToTime             = ops.AnyToTime
	knownMetaKey          = ops.KnownMetaKey

	// metrics
	RecordHotLen          = ops.RecordHotLen
	HotStats              = ops.HotStats
	LoadHotStats          = ops.LoadHotStats
	PersistHotStats       = ops.PersistHotStats
	readZeroPct           = ops.ReadZeroPct
	metaKV                = ops.MetaKV
	loadHotStatsFromMeta  = ops.LoadHotStatsFromMeta
	loadReadStatsFromMeta = ops.LoadReadStatsFromMeta
	persistHotStatsToMeta = ops.PersistHotStatsToMeta

	// marker
	configDir         = ops.ConfigDir
	dataFile          = ops.DataFile
	MarkerFile        = ops.MarkerFile
	RecallMarkerFile  = ops.RecallMarkerFile
	WriteMarker       = ops.WriteMarker
	WriteTypedMarker  = ops.WriteTypedMarker
	ReadMarker        = ops.ReadMarker
	ClearMarker       = ops.ClearMarker
	WriteSessionLabel = ops.WriteSessionLabel

	// nudge·sessionhook
	runNudge        = ops.RunNudge
	runSessionStart = ops.RunSessionStart
	runSessionEnd   = ops.RunSessionEnd
	sessionLabel    = ops.SessionLabel

	// exposureProfileFn — cli-owned var (the nudge_v0110 test swap convention).
	// Initialized to the real implementation moved to wire; the root.go init wrapper propagates swaps into ops.
	exposureProfileFn = ExposureProfile
)
