// ops_alias_1.go — v0.40.0 T1: lib/ops로 이동한 doctor·audit·diff·status·
// tuning 표면을 소문자 var·타입 별칭으로 재노출해 잔여 cli 파일 및 잔류
// 테스트가 무편집 컴파일되게 한다. var 별칭은 테스트 seam 교체 관례
// (소문자 var)를 승계한다(composePsFn·flintBinPathFn — cli 테스트가 교체).
// seam 주입 init은 v0.40.0 T4부터 root.go composition root가 소유한다.
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// 타입 별칭 — 인터페이스 구현·리플렉션·fake 단언 무편집(status_v06/v08
// 리플렉션 테스트의 statusOutput·rules_sync.go의 doctorCheck 반환형 등).
type (
	doctorCheck  = ops.DoctorCheck
	statusOutput = ops.StatusOutput
)

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환(잔류 doctor_v*_test의
// runDoctor·checkConflicts 등, report.go의 statusWindowedRead, maintain.go의
// tuningMetaKeys, setup.go의 harnessRegistry, audit_v08_test의 relTime) 및
// 기존 공개명.
var (
	runDoctor          = ops.RunDoctor
	runTuning          = ops.RunTuning
	applyHookFix       = ops.ApplyHookFix
	doctorFixReport    = ops.DoctorFixReport
	checkBackupAge     = ops.CheckBackupAge
	checkConflicts     = ops.CheckConflicts
	checkZeroNear      = ops.CheckZeroNear
	checkPendingAge    = ops.CheckPendingAge
	isFlintContainer   = ops.IsFlintContainer
	relTime            = ops.RelTime
	statusWindowedRead = ops.StatusWindowedRead
	tuningKeyMap       = ops.TuningKeyMap
	tuningKeyAll       = ops.TuningKeyAll
	tuningMetaKeys     = ops.TuningMetaKeys
	harnessRegistry    = ops.HarnessRegistry
	DoctorChecks       = ops.DoctorChecks

	// composePsFn·flintBinPathFn — cli 소유 var(doctor_v07·v094 테스트
	// 교체 관례). 초기값은 ops 기본 실구현, root.go init이 ops seam을
	// 이 var를 호출 시점에 읽는 래퍼로 교체해 cli var 재할당이 ops 실행
	// 경로에 전파된다.
	composePsFn    = ops.ComposePsFn
	flintBinPathFn = ops.FlintBinPathFn
)

// doctorConflictLimit — doctor conflicts 조회 상한(등록표 이동 수반).
const doctorConflictLimit = ops.DoctorConflictLimit
