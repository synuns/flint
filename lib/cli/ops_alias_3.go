// ops_alias_3.go — re-exports the report, report_html, backup,
// sigverify, and rules_sync surfaces moved to lib/ops as lowercase var and type aliases so remaining
// cli files (profile, setup, update, knowledge_curation_alias and residual tests) compile without
// edits (carrying over the ops_alias_1/2 conventions). Backup registration is done
// directly by the root.go init registering ops.RunBackup and RunRestore.
// Backup seams that need swap propagation (dumpFn, restoreFn, rotateFn, keepFn —
// backup_test swaps the cli vars) are injected by the root.go init as wrappers.
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// Type aliases — literal construction, field access, and fake assertions unchanged (reportDay,
// DailyRow, effectOutput, and trendPoint in residual report/report_html tests, ReleaseVerifier in
// update.go, etc.). The ReadEventRow, WriteRow, and TrendStat fields were upper-cased along with
// the ops move (external-package field access contract).
type (
	reportDay       = ops.ReportDay
	DailyRow        = ops.ReportDay
	reportOutput    = ops.ReportOutput
	effectOutput    = ops.EffectOutput
	trendOutput     = ops.TrendOutput
	trendPoint      = ops.TrendPoint
	readEventRow    = ops.ReadEventRow
	writeRow        = ops.WriteRow
	htmlDashboard   = ops.HtmlDashboard
	trendRow        = ops.TrendRow
	barSeg          = ops.BarSeg
	ReleaseVerifier = ops.ReleaseVerifier
)

// Function/var aliases — lowercase-call compatibility in consumer files plus existing public names.
var (
	// report·report_html
	runReport       = ops.RunReport
	collectEffect   = ops.CollectEffect
	profileHint     = ops.ProfileHint
	readCounters    = ops.ReadCounters
	pctOf           = ops.PctOf
	dupPct          = ops.DupPct
	fallbackPct     = ops.FallbackPct
	trendWindow     = ops.TrendWindow
	chainPct        = ops.ChainPct
	timeWindowHit   = ops.TimeWindowHit
	cellToTime      = ops.CellToTime
	renderDashboard = ops.RenderDashboard
	chipFor         = ops.ChipFor
	EstTok          = ops.EstTok

	// backup
	runBackup        = ops.RunBackup
	runRestore       = ops.RunRestore
	outInsideGitRepo = ops.OutInsideGitRepo
	BackupKeyPath    = ops.BackupKeyPath
	backupEncryptOn  = ops.BackupEncryptOn
	quoteJSON        = ops.QuoteJSON

	// backup seam vars — the cli test swap convention (backup_test reassigns dumpFn etc.).
	// Initialized to the real ops implementations; the root.go init wrapper propagates swaps
	// into ops.
	dumpFn    = ops.DumpFn
	restoreFn = ops.RestoreFn
	rotateFn  = ops.RotateFn
	keepFn    = ops.KeepFn

	// sigverify
	ErrSignatureInvalid = ops.ErrSignatureInvalid

	// rules_sync
	syncRules      = ops.SyncRules
	skillSrcDir    = ops.SkillSrcDir
	checkSkillFile = ops.CheckSkillFile
	runUpdateRules = ops.RunUpdateRules
)
