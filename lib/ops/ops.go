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
