// Package ops — the flint diagnostic, maintenance, and observation command implementations.
// doctor·audit·diff·status·tuning·maintain·metrics·marker·nudge·
// sessionhook·report·report_html·backup·restore·sigverify·rules_sync
// It owns the handler and diagnostic/maintenance-family surfaces (the check registry,
// harness checks, the thresholds interface, markers, metering). Command registration happens
// in init via wire.Register — cli imports this package (cli -> ops one-way; ops importing
// cli would be a cycle), and residual cli consumers compile unedited via the lowercase
// var and type aliases in cli/ops_alias_1/2/3.go. The backup and restore registrations are
// done directly by the cli/root.go composition root init registering ops.RunBackup and RunRestore.
package ops

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("doctor", RunDoctor)
	wire.Register("audit", runAudit)
	wire.Register("diff", RunDiff)
	wire.Register("status", RunStatus)
	wire.Register("tuning", RunTuning)
	wire.Register("maintain", RunMaintain)
	wire.Register("nudge", RunNudge)
	wire.Register("session-start", RunSessionStart)
	wire.Register("session-end", RunSessionEnd)
	wire.Register("report", RunReport)
}
