// cliwire_alias.go — v0.38.0 §1 계약층 추출 호환 별칭. lib/wire로 이동한
// Store·디스패치·registry·DSN·에러 라인·meta KV 표면을 소문자/var 별칭으로
// 재노출해 잔여 cli 파일이 무편집 컴파일되게 한다. var 별칭은 테스트
// seam 교체 관례(소문자 var)를 승계한다. 초기 등록 init(구 registry.go)과
// seam 주입은 v0.40.0 T4부터 root.go composition root가 소유한다.
package cli

import (
	"github.com/synuns/flint/lib/wire"
)

// 타입 별칭 — 인터페이스 구현·임베딩 무편집.
type Store = wire.Store
type Handler = wire.Handler

// 함수/var 별칭 — 소비 파일의 소문자 호출·seam 교체 호환.
var (
	Register   = wire.Register
	Lookup     = wire.Lookup
	unregister = wire.Unregister
	usageLine  = wire.UsageLine
	errLine    = wire.ErrLine
	queryOnce  = wire.QueryOnce
	runHelp    = wire.RunHelp
	getMetaKV  = wire.GetMetaKV
	setMetaKV  = wire.SetMetaKV
	bumpMetaKV = wire.BumpMetaKV

	// DSN 해결(config.go 이동분) — ResolveDSN·ConfigPath·LoadConfigDSN는
	// 기존 공개명 그대로 별칭.
	dsnFromEnv    = wire.DSNFromEnv
	ResolveDSN    = wire.ResolveDSN
	LoadConfigDSN = wire.LoadConfigDSN
	ConfigPath    = wire.ConfigPath
	defaultDSN    = wire.DefaultDSN

	// v0.39.0 T1(thresholds.go·session.go 이동분) — 임계값 체계·세션
	// 라벨 읽기. 소문자 var는 잔류 소비자(tuning·doctor·maintain·read·
	// marker) 호환용이다(v0.40.0 T4 사용 0 별칭 정리 완료).
	// v0.42.0 T2: LoadReadStats·BumpReadStats 별칭 폐지(read_stats blob
	// 폐지 — 파생 경로는 ops.LoadReadStatsFromMeta가 담당).
	loadThresholds   = wire.LoadThresholds
	thresholdsKeys   = wire.ThresholdsKeys
	ReadSessionLabel = wire.ReadSessionLabel
)
