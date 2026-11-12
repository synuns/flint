// ops_alias_2.go — v0.40.0 T2: lib/ops로 이동한 maintain·metrics·marker·
// nudge·sessionhook 표면을 소문자 var·타입·상수 별칭으로 재노출해 잔여 cli
// 파일(report·profile·update·backup·knowledge_alias init 및 잔류 테스트)이
// 무편집 컴파일되게 한다(ops_alias_1 관례 승계). var 별칭은 테스트 seam
// 교체 관례(소문자 var)를 승계한다. 교체 전파가 필요한 seam
// (maintainRetryDelays·exposureProfileFn)은 ops가 cli var를 호출 시점에
// 읽는 래퍼로 주입한다(composePsFn 패턴). T3·T4 이동 완료 후 소비자 0
// 별칭은 T4가 정리한다.
package cli

import (
	"time"

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
	markerKindDistill    = ops.MarkerKindDistill
	markerKindRecall     = ops.MarkerKindRecall
	nudgePendingLine     = ops.NudgePendingLine
	nudgeReadHint        = ops.NudgeReadHint
	nudgeRecallStartLine = ops.NudgeRecallStartLine
	nudgeReasonRecall    = ops.NudgeReasonRecall
)

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환 및 기존 공개명. cliwire_
// alias·knowledge_alias·knowledge_curation_alias·ops_alias_1 init의 seam
// 주입(RecordHotLenFn·WriteTypedMarkerFn·SessionLabelFn·AnyToTimeFn 등)도
// 이 별칭 경로로 ops 함수와 동일 값을 주입한다.
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
	hotP95                = ops.HotP95
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
	ReadTypedMarker   = ops.ReadTypedMarker
	ClearMarker       = ops.ClearMarker
	ClearTypedMarker  = ops.ClearTypedMarker
	WriteSessionLabel = ops.WriteSessionLabel
	ClearSessionLabel = ops.ClearSessionLabel

	// nudge·sessionhook
	runNudge        = ops.RunNudge
	nudgeProfile    = ops.NudgeProfile
	runSessionStart = ops.RunSessionStart
	runSessionEnd   = ops.RunSessionEnd
	sessionLabel    = ops.SessionLabel

	// exposureProfileFn — cli 소유 var(nudge_v0110 테스트 교체 관례).
	// 초기값은 cli profile.go 실구현, 아래 init 래퍼가 교체를 ops에 전파.
	exposureProfileFn = ExposureProfile
)

func init() {
	// seam 주입(교체 전파 래퍼 — ops_alias_1 관례 승계).
	ops.MaintainRetryDelaysFn = func() []time.Duration { return maintainRetryDelays }
	ops.ExposureProfileFn = func(getKV func(string) (string, error)) string { return exposureProfileFn(getKV) }

	// seam 주입(cli 소유 실구현 — T3·T4 이동 전 잔류분).
	ops.MaybeUpdateNoticeFn = maybeUpdateNotice
	ops.UpdateNoticeTTL = updateNoticeTTL
	ops.PctOfFn = pctOf
}
