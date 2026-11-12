// knowledge_alias.go — v0.39.0 T2: lib/knowledge로 이동한 write·import
// 표면을 소문자 var·타입 별칭으로 재노출해 잔여 cli 파일(compact·done·
// doctor·hot·read·brief·nudge·pending·profile·report·sessionhook·
// status·maintain 및 잔류 테스트)이 무편집 컴파일되게 한다. var 별칭은
// 테스트 seam 교체 관례(소문자 var)를 승계한다. cliwire_alias.go init의
// Register("write", runWrite)도 이 별칭 경유로 knowledge.RunWrite와 동일
// 함수를 등록한다(이중 등록 무해 — 동일 핸들러). T3·T4 이동 완료 후
// 소비자 0 별칭은 T5가 정리한다.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// 타입 별칭 — 인터페이스 구현·임베딩·fake 단언 무편집(usage_wire.go의
// realStore 단언·read.go ReadUsageStore 계약 주석 등).
type (
	UsageStore      = knowledge.UsageStore
	UsageCountStore = knowledge.UsageCountStore
	DupIDStore      = knowledge.DupIDStore
	DupMetaStore    = knowledge.DupMetaStore
	DupMeta         = knowledge.DupMeta
)

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환(compact의 runWrite·
// done/doctor/…의 writeThresholds·brief/hot/read의 recordUsage·dup_v0102
// 테스트의 recordDups 등) 및 기존 공개명(ParseSourceFlag·ErrInvalidSource).
var (
	runWrite         = knowledge.RunWrite
	runImport        = knowledge.RunImport
	recordUsage      = knowledge.RecordUsage
	recordDups       = knowledge.RecordDups
	writeThresholds  = knowledge.WriteThresholds
	sanitizePayload  = knowledge.SanitizePayload
	ParseSourceFlag  = knowledge.ParseSourceFlag
	ErrInvalidSource = knowledge.ErrInvalidSource
)

// v0.39.0 T3(read·hot 이동분) — 잔여 cli 소비자(report의 parseIDsCSV·
// status의 fmtAsOfDate·asOfInfo·asOfLine·brief의 HotBudget·tuning의
// defaultHotBudget·metaGetKV와 잔류 테스트의 rowIDs·instrumentReads 등)
// 가 무편집 컴파일되게 하는 별칭.
const defaultHotBudget = knowledge.DefaultHotBudget

type (
	ReadEventStore   = knowledge.ReadEventStore
	HotEventRecorder = knowledge.HotEventRecorder
	asOfInfo         = knowledge.AsOfInfo
	asOfLine         = knowledge.AsOfLine
)

var (
	runRead                = knowledge.RunRead
	runHot                 = knowledge.RunHot
	HotBudget              = knowledge.HotBudget
	metaGetKV              = knowledge.MetaGetKV
	hotFields              = knowledge.HotFields
	hotHeadline            = knowledge.HotHeadline
	hotTopics              = knowledge.HotTopics
	pendingAgeSuffix       = knowledge.PendingAgeSuffix
	hotKnowledgeIDsSQL     = knowledge.HotKnowledgeIDsSQL
	bumpReadFallbackToMeta = knowledge.BumpReadFallbackToMeta
	parseIDsCSV            = knowledge.ParseIDsCSV
	fmtAsOfDate            = knowledge.FmtAsOfDate
	rowIDs                 = knowledge.RowIDs
	instrumentReads        = knowledge.InstrumentReads
	readSuggestEligible    = knowledge.ReadSuggestEligible
	firstExactRank         = knowledge.FirstExactRank
)

func init() {
	// knowledge 주입 seam 실구현 주입(스펙 §1 전수 조사 누락분 — read·hot이
	// 쓰지만 cli 소유 파일에 잔류하는 헬퍼. wire.ConfigDir 주입과 같은
	// 관례: 사이클 없이 knowledge가 cli 구현을 쓴다).
	knowledge.ParseAsOfSpecFn = ParseAsOfSpec
	knowledge.ErrInvalidAsOf = ErrInvalidAsOf
	knowledge.SessionLabelFn = sessionLabel
	knowledge.ExposureProfileFn = ExposureProfile
	knowledge.RecordHotLenFn = RecordHotLen
	knowledge.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	knowledge.PersistHotStatsToMetaFn = persistHotStatsToMeta
}
