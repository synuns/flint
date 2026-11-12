// cliwire_alias.go — v0.38.0 §1 계약층 추출 호환 별칭. lib/wire로 이동한
// Store·디스패치·registry·DSN·에러 라인·meta KV 표면을 소문자/var 별칭으로
// 재노출해 잔여 cli 파일(~30)이 무편집 컴파일되게 한다. var 별칭은 테스트
// seam 교체 관례(소문자 var)를 승계한다. 초기 등록 init(구 registry.go)도
// 이 파일이 이어받는다 — 핸들러(runWrite 등)는 cli 소유라 계약층에 둘 수 없다.
package cli

import (
	"github.com/synuns/flint/lib/wire"
)

// 타입 별칭 — 인터페이스 구현·임베딩 무편집.
type Store = wire.Store
type Handler = wire.Handler

// 함수/var 별칭 — 소비 파일의 소문자 호출·seam 교체 호환.
var (
	Register     = wire.Register
	Lookup       = wire.Lookup
	unregister   = wire.Unregister
	usageLine    = wire.UsageLine
	errLine      = wire.ErrLine
	queryOnce    = wire.QueryOnce
	runHelp      = wire.RunHelp
	helpSummary  = wire.HelpSummary
	getMetaKV    = wire.GetMetaKV
	getMetaKVErr = wire.GetMetaKVErr
	setMetaKV    = wire.SetMetaKV
	bumpMetaKV   = wire.BumpMetaKV

	// DSN 해결(config.go 이동분) — ResolveDSN·ConfigPath·LoadConfigDSN는
	// 기존 공개명 그대로 별칭.
	dsnFromEnv    = wire.DSNFromEnv
	ResolveDSN    = wire.ResolveDSN
	LoadConfigDSN = wire.LoadConfigDSN
	ConfigPath    = wire.ConfigPath
	defaultDSN    = wire.DefaultDSN

	// v0.39.0 T1(thresholds.go·session.go 이동분) — 임계값 체계·read
	// 계측·세션 라벨 읽기. 소문자 var는 잔류 소비자(tuning·doctor·
	// maintain·read·marker)와 테스트 seam 교체 관례(read_test의
	// loadThresholdsFn 주입)를 승계한다.
	loadThresholds          = wire.LoadThresholds
	loadThresholdsFn        = wire.LoadThresholdsFn
	applyThresholdsFallback = wire.ApplyThresholdsFallback
	thresholdsKeys          = wire.ThresholdsKeys
	LoadReadStats           = wire.LoadReadStats
	BumpReadStats           = wire.BumpReadStats
	bumpReadStatsToMeta     = wire.BumpReadStatsToMeta
	SessionLabelFile        = wire.SessionLabelFile
	ReadSessionLabel        = wire.ReadSessionLabel
)

func init() {
	// 계약층 ConfigDir 주입 — 설정 디렉터리 규칙(FLINT_CONFIG_DIR >
	// os.UserConfigDir()/flint)은 cli marker.go가 소유한다.
	wire.ConfigDir = configDir
	// 초기 등록(구 registry.go init 이관): 핵심 6커맨드. help는 미등록 —
	// wire.Run이 store 접속 전에 출력하는 usage 경로가 그대로 담당한다.
	Register("write", runWrite)
	Register("read", runRead)
	Register("hot", runHot)
	Register("status", runStatus)
	Register("backup", runBackup)
	Register("restore", runRestore)
}
