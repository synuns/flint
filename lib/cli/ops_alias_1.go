// ops_alias_1.go — re-exports the doctor, audit, diff, status, and
// tuning surfaces moved to lib/ops as lowercase var and type aliases so remaining cli files and
// residual tests compile without edits. The var aliases carry over the test seam-swap convention
// (lowercase var) (composePsFn, flintBinPathFn — swapped by cli tests).
// The seam-injection init is owned by the root.go composition root.
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// Type aliases — interface implementations, reflection, and fake assertions unchanged (statusOutput
// in the status_v06/v08 reflection tests, the doctorCheck return type in rules_sync.go, etc.).
type (
	doctorCheck  = ops.DoctorCheck
	statusOutput = ops.StatusOutput
)

// Function/var aliases — lowercase-call compatibility in consumer files (runDoctor and
// checkConflicts in residual doctor_v*_test, statusWindowedRead in report.go, tuningMetaKeys in
// maintain.go, harnessRegistry in setup.go, relTime in audit_v08_test) plus existing
// public names.
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

	// composePsFn and flintBinPathFn — cli-owned vars (the doctor_v07/v094 test swap
	// convention). Initialized to the real ops implementations; the root.go init replaces the ops seam
	// with a wrapper that reads these vars at call time, so cli var reassignment propagates to the ops
	// execution path.
	composePsFn    = ops.ComposePsFn
	flintBinPathFn = ops.FlintBinPathFn
)

// doctorConflictLimit — cap on the doctor conflicts lookup (moved along with the registry).
const doctorConflictLimit = ops.DoctorConflictLimit
