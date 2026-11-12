// knowledge_curation_alias.go — v0.39.0 T4: lib/knowledge로 이동한
// 3탄(brief·pending·approve·done·compact) 표면의 잔여 cli 소비자 호환
// 별칭. 룰링: countPending 소문자 var는 render_alias.go 기존 선언을
// knowledge.CountPending으로 재지정한다(이 파일에는 선언하지 않는다 —
// 이중 선언 금지). knowledge 주입 seam 초기화는 v0.40.0 T4부터 root.go
// composition root가 소유한다.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// 함수/var 별칭 — 잔여 cli 소비 파일(compact_test의 runCompact 직접 호출·
// textutil_test의 oneLine 관례 위임 동치 검증·meta_kv_test의 bumpMetaCounter
// 소비측 검증)이 무편집 컴파일되게 한다.
var (
	runCompact      = knowledge.RunCompact
	oneLine         = knowledge.OneLine
	bumpMetaCounter = knowledge.BumpMetaCounter
)
