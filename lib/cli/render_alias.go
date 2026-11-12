// render_alias.go — lib/render 출력층 별칭(v0.38.0 패키지 분할 1/3,
// 스펙 §2). render.go·textutil.go 원본은 lib/render로 이동됐고, 잔여
// cli 소비 파일(read·hot·pending·brief·maintain 등)이 무편집 컴파일
// 되도록 같은 이름의 var 별칭을 제공한다. 소문자 var(truncateRunes·
// countPending)는 테스트 seam 교체 관례 호환 유지.
package cli

import (
	"github.com/synuns/flint/lib/render"
)

// 출력 살균 조크포인트 — render 패키지 단일 구현 위임.
var (
	Text          = render.Text
	Masked        = render.Masked
	Cells         = render.Cells
	Rows          = render.Rows
	TruncateRunes = render.TruncateRunes
	CountPending  = render.CountPending
)

// 소비자 호환 소문자 별칭(무편집 관례 — import·maintain·pending·done
// 및 sessionhook·status·nudge가 그대로 호출한다).
var (
	truncateRunes = render.TruncateRunes
	countPending  = render.CountPending
)
