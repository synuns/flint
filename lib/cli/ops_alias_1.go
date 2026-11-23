package cli

import (
	"github.com/synuns/flint/lib/ops"
)

type (
	doctorCheck  = ops.DoctorCheck
	statusOutput = ops.StatusOutput
)

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

	composePsFn    = ops.ComposePsFn
	flintBinPathFn = ops.FlintBinPathFn
)

const doctorConflictLimit = ops.DoctorConflictLimit
