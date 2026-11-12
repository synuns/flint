// knowledge_alias.go — v0.39.0 T2: lib/knowledge로 이동한 write·import·
// read·hot 표면을 소문자 var·타입 별칭으로 재노출해 잔여 cli 파일과 잔류
// 테스트가 무편집 컴파일되게 한다. var 별칭은 테스트 seam 교체 관례
// (소문자 var)를 승계한다. knowledge 주입 seam 초기화는 v0.40.0 T4부터
// root.go composition root가 소유한다.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// 타입 별칭 — 인터페이스 구현·임베딩·fake 단언 무편집(usage_wire.go의
// realStore 단언·read.go ReadUsageStore 계약 주석 등).
type (
	UsageStore       = knowledge.UsageStore
	UsageCountStore  = knowledge.UsageCountStore
	DupIDStore       = knowledge.DupIDStore
	DupMetaStore     = knowledge.DupMetaStore
	DupMeta          = knowledge.DupMeta
	ReadEventStore   = knowledge.ReadEventStore
	HotEventRecorder = knowledge.HotEventRecorder
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
	ParseSourceFlag  = knowledge.ParseSourceFlag
	ErrInvalidSource = knowledge.ErrInvalidSource
)

// v0.39.0 T3(read·hot 이동분) — 잔여 cli 소비자(report·setup·update 및
// root.go seam 주입)와 잔류 테스트가 무편집 컴파일되게 하는 별칭.
var (
	runHot                 = knowledge.RunHot
	runRead                = knowledge.RunRead
	HotBudget              = knowledge.HotBudget
	hotFields              = knowledge.HotFields
	hotHeadline            = knowledge.HotHeadline
	hotTopics              = knowledge.HotTopics
	pendingAgeSuffix       = knowledge.PendingAgeSuffix
	hotKnowledgeIDsSQL     = knowledge.HotKnowledgeIDsSQL
	bumpReadFallbackToMeta = knowledge.BumpReadFallbackToMeta
	parseIDsCSV            = knowledge.ParseIDsCSV
	rowIDs                 = knowledge.RowIDs
	readSuggestEligible    = knowledge.ReadSuggestEligible
	firstExactRank         = knowledge.FirstExactRank
)
