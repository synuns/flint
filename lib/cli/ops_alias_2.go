package cli

import (
	"github.com/synuns/flint/lib/ops"
)

type (
	MaintStore           = ops.MaintStore
	ConflictStore        = ops.ConflictStore
	SessionStartRecorder = ops.SessionStartRecorder
)

const (
	suspectsDefaultLimit = ops.SuspectsDefaultLimit
	markerKindRecall     = ops.MarkerKindRecall
	nudgePendingLine     = ops.NudgePendingLine
	nudgeReadHint        = ops.NudgeReadHint
	nudgeRecallStartLine = ops.NudgeRecallStartLine
	nudgeReasonRecall    = ops.NudgeReasonRecall
)

var (
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

	RecordHotLen          = ops.RecordHotLen
	HotStats              = ops.HotStats
	LoadHotStats          = ops.LoadHotStats
	PersistHotStats       = ops.PersistHotStats
	readZeroPct           = ops.ReadZeroPct
	metaKV                = ops.MetaKV
	loadHotStatsFromMeta  = ops.LoadHotStatsFromMeta
	loadReadStatsFromMeta = ops.LoadReadStatsFromMeta
	persistHotStatsToMeta = ops.PersistHotStatsToMeta

	configDir         = ops.ConfigDir
	dataFile          = ops.DataFile
	MarkerFile        = ops.MarkerFile
	RecallMarkerFile  = ops.RecallMarkerFile
	WriteMarker       = ops.WriteMarker
	WriteTypedMarker  = ops.WriteTypedMarker
	ReadMarker        = ops.ReadMarker
	ClearMarker       = ops.ClearMarker
	WriteSessionLabel = ops.WriteSessionLabel

	runNudge        = ops.RunNudge
	runSessionStart = ops.RunSessionStart
	runSessionEnd   = ops.RunSessionEnd
	sessionLabel    = ops.SessionLabel

	exposureProfileFn = ExposureProfile
)
