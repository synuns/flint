// knowledge_curation_alias.go — v0.39.0 T4: lib/knowledge로 이동한
// 3탄(brief·pending·approve·done·compact) 표면의 잔여 cli 소비자 호환
// 별칭과 knowledge 주입 seam 초기화. 룰링: countPending 소문자 var는
// render_alias.go 기존 선언을 knowledge.CountPending으로 재지정한다(이
// 파일에는 선언하지 않는다 — 이중 선언 금지). 소비자 0 별칭은 T5가
// 정리한다.
package cli

import (
	"github.com/synuns/flint/lib/knowledge"
)

// 함수/var 별칭 — 잔여 cli 소비 파일(sessionhook의 runDone·compact_test의
// runCompact 직접 호출·textutil_test의 oneLine 관례 위임 동치 검증·
// meta_kv_test의 bumpMetaCounter 소비측 검증)이 무편집 컴파일되게 한다.
var (
	runDone         = knowledge.RunDone
	runCompact      = knowledge.RunCompact
	oneLine         = knowledge.OneLine
	bumpMetaCounter = knowledge.BumpMetaCounter
)

func init() {
	// knowledge 주입 seam 실구현 주입(스펙 §2 — 마커 기록·암호화 키 경로는
	// cli 소유 인프라(marker.go·backup.go). wire.ConfigDir 주입과 같은
	// 관례: 사이클 없이 knowledge가 cli 구현을 쓴다).
	knowledge.WriteTypedMarkerFn = WriteTypedMarker
	knowledge.BackupKeyPathFn = BackupKeyPath
}
