// asof.go — v0.40.0 T4: as-of 정규화 원본은 lib/wire/asof.go로 이동(공용
// Store 유틸 — knowledge.read·ops.diff·ops.status seam이 소비). 이 파일은
// cli 잔여 소비자(asof_test 실DB 회귀·root.go seam 주입) 호환 별칭만 담는다.
package cli

import (
	"github.com/synuns/flint/lib/wire"
)

// 함수/var 별칭 — 무편집 재노출. ErrInvalidAsOf는 동일 인스턴스라
// errors.Is 판정 불변.
var (
	ErrInvalidAsOf = wire.ErrInvalidAsOf
	ParseRelative  = wire.ParseRelative
	ParseAsOfSpec  = wire.ParseAsOfSpec
)

// asofMinPrefix — 해시 접두어 최소 길이(원본 wire.AsofMinPrefix).
const asofMinPrefix = wire.AsofMinPrefix
