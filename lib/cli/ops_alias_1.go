// ops_alias_1.go — v0.40.0 T1: lib/ops로 이동한 doctor·audit·diff·status·
// tuning 표면을 소문자 var·타입 별칭으로 재노출해 잔여 cli 파일(maintain·
// metrics·marker·nudge·report·rules_sync·sessionhook·setup·backup 및 잔류
// 테스트)이 무편집 컴파일되게 한다. var 별칭은 테스트 seam 교체 관례
// (소문자 var)를 승계한다. composePsFn·flintBinPathFn 교체는 cli var에
// 일어나므로 ops seam을 cli var 읽기 래퍼로 주입한다(교체 전파).
// cliwire_alias.go init의 Register("status", runStatus)도 이 별칭 경로로
// ops.RunStatus와 동일 함수를 등록한다(이중 등록 무해 — 동일 핸들러).
// T2·T3 이동 완료 후 소비자 0 별칭은 T4가 정리한다.
package cli

import (
	"context"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// 타입 별칭 — 인터페이스 구현·리플렉션·fake 단언 무편집(status_v06/v08
// 리플렉션 테스트의 statusOutput·rules_sync.go의 doctorCheck 반환형 등).
type (
	doctorCheck  = ops.DoctorCheck
	Check        = ops.DoctorCheck
	checkSpec    = ops.CheckSpec
	harnessSpec  = ops.HarnessSpec
	statusOutput = ops.StatusOutput
)

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환(잔류 doctor_v*_test의
// runDoctor·checkConflicts 등, report.go의 statusWindowedRead·
// fallbackCallsOrNull, maintain.go의 tuningMetaKeys, setup.go의
// harnessRegistry, audit_v08_test의 relTime) 및 기존 공개명.
var (
	runDoctor           = ops.RunDoctor
	runDiff             = ops.RunDiff
	runStatus           = ops.RunStatus
	runTuning           = ops.RunTuning
	claudeHome          = ops.ClaudeHome
	applyHookFix        = ops.ApplyHookFix
	doctorFixReport     = ops.DoctorFixReport
	checkBackupAge      = ops.CheckBackupAge
	checkConflicts      = ops.CheckConflicts
	checkZeroNear       = ops.CheckZeroNear
	checkPendingAge     = ops.CheckPendingAge
	isFlintContainer    = ops.IsFlintContainer
	relTime             = ops.RelTime
	statusWindowedRead  = ops.StatusWindowedRead
	fallbackCallsOrNull = ops.FallbackCallsOrNull
	tuningKeyMap        = ops.TuningKeyMap
	tuningKeyAll        = ops.TuningKeyAll
	tuningCurrent       = ops.TuningCurrent
	tuningList          = ops.TuningList
	tuningResetAll      = ops.TuningResetAll
	tuningKeys          = ops.TuningKeys
	tuningMetaKeys      = ops.TuningMetaKeys
	harnessRegistry     = ops.HarnessRegistry
	DoctorChecks        = ops.DoctorChecks

	// composePsFn·flintBinPathFn — cli 소유 var(doctor_v07·v094 테스트
	// 교체 관례). 초기값은 ops 기본 실구현, init이 ops seam을 이 var를
	// 호출 시점에 읽는 래퍼로 교체해 cli var 재할당이 ops 실행 경로에
	// 전파된다.
	composePsFn    = ops.ComposePsFn
	flintBinPathFn = ops.FlintBinPathFn
)

// doctorConflictLimit — doctor conflicts 조회 상한(등록표 이동 수반).
const doctorConflictLimit = ops.DoctorConflictLimit

func init() {
	// seam 주입 ①(교체 전파 래퍼): cli var 재할당을 ops가 관측.
	ops.ComposePsFn = func(ctx context.Context) ([]byte, error) { return composePsFn(ctx) }
	ops.FlintBinPathFn = func() string { return flintBinPathFn() }

	// seam 주입 ②(cli 소유 실구현 — T2·T3·T4 이동 전 잔류분).
	ops.CurrentVersionFn = CurrentVersion
	ops.FetchLatestVersionFn = fetchLatestVersion
	ops.ValidVersionFn = validVersion
	ops.CompareVersionsFn = compareVersions
	ops.KnownMetaKeyFn = knownMetaKey
	ops.AnyToTimeFn = anyToTime
	ops.IsConflictGroupFn = isConflictGroup
	ops.ConflictDupThresholdFn = func() float64 { return conflictDupThreshold }
	ops.BackupEncryptOnFn = backupEncryptOn
	ops.BackupKeyPathFn = BackupKeyPath
	ops.SkillSrcDirFn = skillSrcDir
	ops.CheckSkillFileFn = checkSkillFile
	ops.ParseAsOfSpecFn = ParseAsOfSpec
	ops.ErrInvalidAsOf = ErrInvalidAsOf
	ops.ReadZeroPctFn = readZeroPct
	ops.HotStatsFn = HotStats
	ops.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	ops.LoadReadStatsFromMetaFn = loadReadStatsFromMeta
	ops.QuoteJSONFn = quoteJSON

	// seam 주입 ③(변환 래퍼): cli report.go windowedDaily의 reportDay를
	// ops.DailyRow로 변환해 주입(T3 report 이동 후 직접 호출 수렴).
	ops.WindowedDailyFn = func(ctx context.Context, st wire.Store, days int) ([]ops.DailyRow, error) {
		rows, err := windowedDaily(ctx, st, days)
		if err != nil {
			return nil, err
		}
		out := make([]ops.DailyRow, 0, len(rows))
		for _, d := range rows {
			out = append(out, ops.DailyRow{Read: d.Read, ReadZero: d.ReadZero, ReadNear: d.ReadNear})
		}
		return out, nil
	}
}
