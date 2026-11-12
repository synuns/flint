// ops_alias_3.go — v0.40.0 T3: lib/ops로 이동한 report·report_html·backup·
// sigverify·rules_sync 표면을 소문자 var·타입 별칭으로 재노출해 잔여 cli
// 파일(profile·setup·update·knowledge_curation_alias 및 잔류 테스트)이
// 무편집 컴파일되게 한다(ops_alias_1·2 관례 승계). backup 등록은
// root.go init이 ops.RunBackup·RunRestore를 직접 등록한다(T4 수렴).
// 교체 전파가 필요한 backup seam(dumpFn·restoreFn·rotateFn·keepFn —
// backup_test가 cli var를 교체)은 root.go init이 래퍼로 주입한다.
package cli

import (
	"github.com/synuns/flint/lib/ops"
)

// 타입 별칭 — 리터럴 구성·필드 접근·fake 단언 무편집(report·report_html
// 잔류 테스트의 reportDay·DailyRow·effectOutput·trendPoint, update.go의
// ReleaseVerifier 등). ReadEventRow·WriteRow·TrendStat 필드는 ops 이동과
// 함께 대문자화됐다(외부 패키지 필드 접근 계약).
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

// 함수/var 별칭 — 소비 파일의 소문자 호출 호환 및 기존 공개명.
var (
	// report·report_html
	runReport       = ops.RunReport
	windowedDaily   = ops.WindowedDaily
	collectCounts   = ops.CollectCounts
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

	// backup seam var — cli 테스트 교체 관례(backup_test가 dumpFn 등을
	// 재할당한다). 초기값은 ops 기본 실구현, root.go init 래퍼가 교체를
	// ops에 전파한다.
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
