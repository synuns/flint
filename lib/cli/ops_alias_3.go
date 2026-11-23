package cli

import (
	"github.com/synuns/flint/lib/ops"
)

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

var (
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

	runBackup        = ops.RunBackup
	runRestore       = ops.RunRestore
	outInsideGitRepo = ops.OutInsideGitRepo
	BackupKeyPath    = ops.BackupKeyPath
	backupEncryptOn  = ops.BackupEncryptOn
	quoteJSON        = ops.QuoteJSON

	dumpFn    = ops.DumpFn
	restoreFn = ops.RestoreFn
	rotateFn  = ops.RotateFn
	keepFn    = ops.KeepFn

	ErrSignatureInvalid = ops.ErrSignatureInvalid

	syncRules      = ops.SyncRules
	skillSrcDir    = ops.SkillSrcDir
	checkSkillFile = ops.CheckSkillFile
	runUpdateRules = ops.RunUpdateRules
)
