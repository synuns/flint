// ops_alias_2.go — v0.40.0 T2: lib/ops로 이동한 maintain·metrics·marker·
// nudge·sessionhook 표면을 소문자 var·타입·상수 별칭으로 재노출해 잔여 cli
// 파일(report·profile·update·knowledge_alias 및 잔류 테스트)이 무편집
// 컴파일되게 한다(ops_alias_1 관례 승계). var 별칭은 테스트 seam 교체
// 관례(소문자 var)를 승계한다(maintainRetryDelays·exposureProfileFn —
// root.go init이 교체 전파 래퍼로 주입).
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// 타입 별칭 — 인터페이스 구현·fake 단언 무편집(maintain_test의 MaintStore·
// conflicts_v0131의 ConflictStore·sessionhook_v0200의 SessionStartRecorder).
type (
	MaintStore           = ops.MaintStore
	ConflictStore        = ops.ConflictStore
	SessionStartRecorder = ops.SessionStartRecorder
)

// 상수 별칭 — 잔류 cli 소비자(maintain_v0130의 suspectsDefaultLimit·
// nudge_v0181/182의 markerKindRecall·nudge_v0110의 nudgePendingLine 등).
const (
	suspectsDefaultLimit = ops.SuspectsDefaultLimit
	markerKindRecall     = ops.MarkerKindRecall
	nudgePendingLine     = ops.NudgePendingLine
	nudgeReadHint        = ops.NudgeReadHint
	nudgeRecallStartLine = ops.NudgeRecallStartLine
	nudgeReasonRecall    = ops.NudgeReasonRecall
)

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환 및 기존 공개명.
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

	// exposureProfileFn — cli 소유 var(nudge_v0110 테스트 교체 관례).
	// 초기값은 wire 이동분 실구현, root.go init 래퍼가 교체를 ops에 전파.
	exposureProfileFn = ExposureProfile
)
