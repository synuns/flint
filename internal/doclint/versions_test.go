package doclint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var versionDocContracts = []struct {
	name  string
	doc   string
	wants []string
}{

	{name: "TestV09AdapterDocsCodex", doc: "adapters/codex.md", wants: []string{
		"[mcp_servers.flint]",
		`command = "flint"`,
		`args = ["mcp"]`,
		`requires_approval = "never"`,
		"AGENTS.md",
	}},
	{name: "TestV09AdapterDocsGemini", doc: "adapters/gemini.md", wants: []string{
		"mcpServers",
		`"trust": true`,
		"GEMINI.md",
		"flint mcp",
	}},
	{name: "TestV09AdapterDocsCopilot", doc: "adapters/copilot.md", wants: []string{
		"mcp-config.json",
		"instructions.md",
		"AGENTS.md",
		"flint mcp",
	}},

	{name: "TestTuningGuideV0240", doc: "TUNING.md", wants: []string{
		"Tuning guide", "Retuning triggers", "daily_retention_days", "backup_interval_h",
		"vocab_tags", "defensive cap",
	}},

	{name: "TestVersion0440Milestone", doc: "PLAN.md", wants: []string{
		"v0.44.0", "병인 ⑤ 해소",
	}},

	{name: "TestVersion0124Milestone", doc: "HISTORY.md", wants: []string{
		"v1.2.4", "MCP store 재사용",
	}},
	{name: "TestVersion0123Milestone", doc: "HISTORY.md", wants: []string{
		"v1.2.3", "감사 R17",
	}},

	{name: "TestVersion0130Milestone", doc: "HISTORY.md", wants: []string{
		"v1.3.0", "export",
	}},

	{name: "TestVersion0131Milestone", doc: "HISTORY.md", wants: []string{
		"v1.3.1", "R19",
	}},

	{name: "TestVersion0140Milestone", doc: "HISTORY.md", wants: []string{
		"v1.4.0", "PAM",
	}},

	{name: "TestVersion0141Milestone", doc: "HISTORY.md", wants: []string{
		"v1.4.1", "R22",
	}},

	{name: "TestVersion0142Milestone", doc: "HISTORY.md", wants: []string{
		"v1.4.2", "R23",
	}},

	{name: "TestVersion0150Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.0", "TUI",
	}},

	{name: "TestVersion0151Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.1", "R25",
	}},

	{name: "TestVersion0152Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.2", "TUI default",
	}},

	{name: "TestVersion0153Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.3", "agent",
	}},

	{name: "TestVersion0154Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.4", "usage",
	}},

	{name: "TestVersion0155Milestone", doc: "HISTORY.md", wants: []string{
		"v1.5.5", "terminal",
	}},

	{name: "TestVersion0160Milestone", doc: "HISTORY.md", wants: []string{
		"v1.6.0", "performance",
	}},

	{name: "TestPerfDocContractV1600", doc: "PERF.md", wants: []string{
		"synthetic", "median of 3", "pg_trgm", "Reproducing",
	}},

	{name: "TestVersion0161Milestone", doc: "HISTORY.md", wants: []string{
		"v1.6.1", "filter",
	}},

	{name: "TestVersion0162Milestone", doc: "HISTORY.md", wants: []string{
		"v1.6.2", "mcp",
	}},

	{name: "TestVersion0240Milestone", doc: "HISTORY.md", wants: []string{
		"v0.24.0", "임계값 설계 완결",
	}},
	{name: "TestVersion0230Milestone", doc: "HISTORY.md", wants: []string{
		"v0.23.0", "tuning",
	}},
	{name: "TestVersion0220Milestone", doc: "HISTORY.md", wants: []string{
		"v0.22.0", "자동 업데이트",
	}},
	{name: "TestV0210AdapterDocsQwen", doc: "adapters/qwen.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsDevin", doc: "adapters/devin.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsCline", doc: "adapters/cline.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsWindsurf", doc: "adapters/windsurf.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsZed", doc: "adapters/zed.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsAmp", doc: "adapters/amp.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsVscode", doc: "adapters/vscode.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsContinue", doc: "adapters/continue.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsGoose", doc: "adapters/goose.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},
	{name: "TestV0210AdapterDocsTrae", doc: "adapters/trae.md", wants: []string{
		"flint_read", "flint_write", "flint mcp", "지시파일",
	}},

	{name: "TestPlanRecordsV092AndV093", doc: "HISTORY.md", wants: []string{"v0.9.2", "v0.9.3"}},

	{name: "TestReadmeMentionsFallback/README.md", doc: "../README.md", wants: []string{"fallback"}},
	{name: "TestReadmeMentionsFallback/README.ko.md", doc: "../README.ko.md", wants: []string{"fallback"}},
	{name: "TestPlanRecordsV095AndV096", doc: "HISTORY.md", wants: []string{"v0.9.5", "v0.9.6"}},

	{name: "TestReadmeMentionsSuggest/README.md", doc: "../README.md", wants: []string{"sha256"}},
	{name: "TestReadmeMentionsSuggest/README.ko.md", doc: "../README.ko.md", wants: []string{"sha256"}},

	{name: "TestTuningRecordsV0101Constants", doc: "TUNING.md", wants: []string{
		"backupAgeWarn", "72", "suggestMinScore", "0.15",
	}},

	{name: "TestTuningBackupAgeWarnRationaleMatchesCode", doc: "TUNING.md", wants: []string{
		"does not warn through two consecutive failures",
	}},

	{name: "TestPlanRecordsV094AndV097", doc: "HISTORY.md", wants: []string{"v0.9.4", "v0.9.7"}},
	{name: "TestSkillDocumentsReadFlags", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{
		"--tag", "--entity", "--domain", "--limit",
	}},

	{name: "TestSkillMentionsHotFull", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"hot --full"}},
	{name: "TestReadmeMentionsExposure/README.md", doc: "../README.md", wants: []string{"exposure"}},
	{name: "TestReadmeMentionsExposure/README.ko.md", doc: "../README.ko.md", wants: []string{"exposure"}},
	{name: "TestPlanRecordsV0110", doc: "HISTORY.md", wants: []string{"v0.11.0"}},

	{name: "TestSkillDocumentsReadStatus", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"read --status pending"}},
	{name: "TestPlanRecordsV0120", doc: "HISTORY.md", wants: []string{"v0.12.0"}},

	{name: "TestPlanRecordsV0130", doc: "HISTORY.md", wants: []string{"v0.13.0"}},
	{name: "TestTuningRecordsConflictThreshold", doc: "TUNING.md", wants: []string{"conflictDupThreshold", "0.6"}},

	{name: "TestPlanRecordsV0140", doc: "HISTORY.md", wants: []string{"v0.14.0"}},
	{name: "TestTuningRecordsZeroNear", doc: "TUNING.md", wants: []string{"zero_near"}},

	{name: "TestPlanRecordsV0150", doc: "HISTORY.md", wants: []string{"v0.15.0"}},
	{name: "TestTuningRecordsWindowed", doc: "TUNING.md", wants: []string{"read_zero_near", "30"}},
	{name: "TestReadmeExitTable/README.md", doc: "../README.md", wants: []string{
		"exit 3", "invalid input (valueless trailing flags",
	}},
	{name: "TestReadmeExitTable/README.ko.md", doc: "../README.ko.md", wants: []string{
		"exit 3", "잘못된 입력 (값 없는 trailing 플래그",
	}},

	{name: "TestPlanRecordsV0151", doc: "HISTORY.md", wants: []string{"v0.15.1"}},

	{name: "TestPlanRecordsV0152", doc: "HISTORY.md", wants: []string{"v0.15.2"}},

	{name: "TestPlanRecordsV0160", doc: "HISTORY.md", wants: []string{"v0.16.0"}},
	{name: "TestCiTriggersOnVersionTags", doc: "../.github/workflows/ci.yml", wants: []string{`tags: ["v*"]`}},
	{name: "TestReadmePluginInstall/README.md", doc: "../README.md", wants: []string{"marketplace add synuns/flint"}},
	{name: "TestReadmePluginInstall/README.ko.md", doc: "../README.ko.md", wants: []string{"marketplace add synuns/flint"}},

	{name: "TestPlanRecordsV0161", doc: "HISTORY.md", wants: []string{"v0.16.1"}},
	{name: "TestReadmePathRequirement/README.md", doc: "../README.md", wants: []string{"PATH"}},
	{name: "TestReadmePathRequirement/README.ko.md", doc: "../README.ko.md", wants: []string{"PATH"}},

	{name: "TestSkillWriteProtocol", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{
		"갱신 우선", "최소화", "flint read",
	}},
	{name: "TestPlanRecordsV0162", doc: "HISTORY.md", wants: []string{"v0.16.2"}},

	{name: "TestPlanRecordsV0163", doc: "HISTORY.md", wants: []string{"v0.16.3"}},
	{name: "TestTuningKeysV0163Documented", doc: "TUNING.md", wants: []string{
		"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
		"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
		"pending_age_days",
	}},
	{name: "TestSkillRationaleNeutral", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"중복 재제출"}},

	{name: "TestReadmeSetupMcp/README.md", doc: "../README.md", wants: []string{"setup --mcp"}},
	{name: "TestReadmeSetupMcp/README.ko.md", doc: "../README.ko.md", wants: []string{"setup --mcp"}},

	{name: "TestDependabotConfigured", doc: "../.github/dependabot.yml", wants: []string{
		`package-ecosystem: "gomod"`,
		`package-ecosystem: "github-actions"`,
		`interval: "weekly"`,
	}},

	{name: "TestVersion0180Milestone/PLAN", doc: "HISTORY.md", wants: []string{"v0.18.0", "회상 우선 메모리"}},
	{name: "TestVersion0180Milestone/TUNING", doc: "TUNING.md", wants: []string{"recall_warn_writes"}},

	{name: "TestVersion0190Milestone", doc: "HISTORY.md", wants: []string{"v0.19.0", "효과 계측"}},
	{name: "TestMetricsDocContract", doc: "METRICS.md", wants: []string{
		"Goal→Signal→Metric",
		"G1 recall hit",
		"G2 rediscovery block",
		"G3 asset health",
		"G4 recall-first habit",
		"hit_pct", "near_pct", "fallback_pct", "dup_prevented",
		"reuse_pct", "dead_pct", "read_write_pairs", "pair_pct",
		"Tier 1", "Tier 2",
		"Prohibitions",
	}},

	{name: "TestVersion0200Milestone", doc: "HISTORY.md", wants: []string{
		"v0.20.0", "효과 계측 정밀화", "기저율 셔플(C) 이연",
	}},
	{name: "TestMetricsDocV0200", doc: "METRICS.md", wants: []string{
		"recall_led_pct", "chain_pct",
		"session_starts", "hot_events",
		"older deployments and first instrumentation days",
		"excluding hot exposure within the last 30 days",
		"baseline shuffle deferred",
	}},

	{name: "TestVersion0171Milestone", doc: "HISTORY.md", wants: []string{
		"v1.7.1", "partial-sort",
	}},

	{name: "TestVersion0172Milestone", doc: "HISTORY.md", wants: []string{
		"v1.7.2", "telemetry",
	}},

	{name: "TestVersion0180Milestone/DbSeverance", doc: "HISTORY.md", wants: []string{
		"v1.8.0", "db-severance",
	}},

	{name: "TestVersion0181Milestone", doc: "HISTORY.md", wants: []string{
		"v1.8.1", "r40-hardening",
	}},

	{name: "TestVersion0190Milestone/MultiDevice", doc: "HISTORY.md", wants: []string{
		"v1.9.0", "multi-device",
	}},

	{name: "TestVersion0191Milestone", doc: "HISTORY.md", wants: []string{
		"v1.9.1", "r42-hardening",
	}},

	{name: "TestVersion0192Milestone", doc: "HISTORY.md", wants: []string{
		"v1.9.2", "r43-recovery",
	}},

	{name: "TestVersion0193Milestone", doc: "HISTORY.md", wants: []string{
		"v1.9.3", "c-tui-perf",
	}},

	{name: "TestVersion0194Milestone", doc: "HISTORY.md", wants: []string{
		"v1.9.4", "r45-hardening",
	}},

	{name: "TestVersion01100Milestone", doc: "HISTORY.md", wants: []string{
		"v1.10.0", "hygiene",
	}},

	{name: "TestVersion01101Milestone", doc: "HISTORY.md", wants: []string{
		"v1.10.1", "r47-counts",
	}},
	{name: "TestVersion01102Milestone", doc: "HISTORY.md", wants: []string{
		"v1.10.2", "r48-notes",
	}},

	{name: "TestVersion01110Milestone", doc: "HISTORY.md", wants: []string{
		"v1.11.0", "correction-channel",
	}},
	{name: "TestVersion01111Milestone", doc: "HISTORY.md", wants: []string{
		"v1.11.1", "r50-metering",
	}},

	{name: "TestVersion01112Milestone", doc: "HISTORY.md", wants: []string{
		"v1.11.2", "r52-reporting",
	}},

	{name: "TestVersion01120Milestone", doc: "HISTORY.md", wants: []string{
		"v1.12.0", "gap-candidates",
	}},

	{name: "TestVersion01121Milestone", doc: "HISTORY.md", wants: []string{
		"v1.12.1", "r54-docs",
	}},

	{name: "TestVersion01130Milestone", doc: "HISTORY.md", wants: []string{
		"v1.13.0", "age-audit",
	}},

	{name: "TestVersion01140Milestone", doc: "HISTORY.md", wants: []string{
		"v1.14.0", "pipeline-complete",
	}},

	{name: "TestVersion01150Milestone", doc: "HISTORY.md", wants: []string{
		"v1.15.0", "distribution",
	}},

	{name: "TestVersion01160Milestone", doc: "HISTORY.md", wants: []string{
		"v1.16.0", "search-perf",
	}},
	{name: "TestVersion01161Milestone", doc: "HISTORY.md", wants: []string{
		"v1.16.1", "r61-parity",
	}},
	{name: "TestVersion01162Milestone", doc: "HISTORY.md", wants: []string{
		"v1.16.2", "r62-guard",
	}},

	{name: "TestVersion01170Milestone", doc: "HISTORY.md", wants: []string{
		"v1.17.0", "echo",
	}},

	{name: "TestVersion01171Milestone", doc: "HISTORY.md", wants: []string{
		"v1.17.1", "r64-observability",
	}},

	{name: "TestVersion01172Milestone", doc: "HISTORY.md", wants: []string{
		"v1.17.2", "r65-dedup",
	}},

	{name: "TestVersion01180Milestone", doc: "HISTORY.md", wants: []string{
		"v1.18.0", "mcp-structured",
	}},
	{name: "TestVersion01181Milestone", doc: "HISTORY.md", wants: []string{
		"v1.18.1", "r67-schema",
	}},
	{name: "TestVersion01182Milestone", doc: "HISTORY.md", wants: []string{
		"v1.18.2", "r68-hygiene",
	}},
	{name: "TestVersion01183Milestone", doc: "HISTORY.md", wants: []string{
		"v1.18.3", "r69-grammar",
	}},

	{name: "TestVersion01190Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.0", "observability-foundation",
	}},

	{name: "TestVersion01191Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.1", "r71-r74",
	}},

	{name: "TestVersion01192Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.2", "r75-r76",
	}},

	{name: "TestVersion01193Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.3", "r77-r78",
	}},

	{name: "TestVersion01194Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.4", "r79-r80",
	}},

	{name: "TestVersion01195Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.5", "r81",
	}},

	{name: "TestVersion01196Milestone", doc: "HISTORY.md", wants: []string{
		"v1.19.6", "r82",
	}},

	{name: "TestVersion01200Milestone", doc: "HISTORY.md", wants: []string{
		"v1.20.0", "knowledge-schema-search",
	}},

	{name: "TestVersion01201Milestone", doc: "HISTORY.md", wants: []string{
		"v1.20.1", "r84-r87",
	}},

	{name: "TestVersion01202Milestone", doc: "HISTORY.md", wants: []string{
		"v1.20.2", "r88",
	}},
	{name: "TestVersion01203Milestone", doc: "HISTORY.md", wants: []string{
		"v1.20.3", "r89",
	}},

	{name: "TestVersion01210Milestone", doc: "HISTORY.md", wants: []string{
		"v1.21.0", "vault-bridge-index",
	}},

	{name: "TestVersion01211Milestone", doc: "HISTORY.md", wants: []string{
		"v1.21.1", "r91-r94",
	}},

	{name: "TestVersion01220Milestone", doc: "HISTORY.md", wants: []string{
		"v1.22.0", "vault-staleness",
	}},

	{name: "TestVersion01221Milestone", doc: "HISTORY.md", wants: []string{
		"v1.22.1", "r96-r98",
	}},

	{name: "TestVersion01230Milestone", doc: "HISTORY.md", wants: []string{
		"v1.23.0", "autotune-shadow",
	}},

	{name: "TestVersion01231Milestone", doc: "HISTORY.md", wants: []string{
		"v1.23.1", "r100-r101",
	}},

	{name: "TestVersion01232Milestone", doc: "HISTORY.md", wants: []string{
		"v1.23.2", "r102",
	}},

	{name: "TestVersion01240Milestone", doc: "HISTORY.md", wants: []string{
		"v1.24.0", "curation-residual",
	}},

	{name: "TestVersion01241Milestone", doc: "HISTORY.md", wants: []string{
		"v1.24.1", "r104-r105",
	}},
	{name: "TestVersion01242Milestone", doc: "HISTORY.md", wants: []string{
		"v1.24.2", "r106",
	}},

	{name: "TestVersion01250Milestone", doc: "HISTORY.md", wants: []string{
		"v1.25.0", "engine-ops",
	}},

	{name: "TestVersion01251Milestone", doc: "HISTORY.md", wants: []string{
		"v1.25.1", "r108", "r109-1",
	}},

	{name: "TestVersion01260Milestone", doc: "HISTORY.md", wants: []string{
		"v1.26.0", "surface-trust",
	}},

	{name: "TestVersion01261Milestone", doc: "HISTORY.md", wants: []string{
		"v1.26.1", "surface-trust audit fixes",
	}},
	{name: "TestVersion01262Milestone", doc: "HISTORY.md", wants: []string{
		"v1.26.2", "comment sweep",
	}},
	{name: "TestVersion01263Milestone", doc: "HISTORY.md", wants: []string{
		"v1.26.3", "comment gate",
	}},
	{name: "TestVersion01290Milestone", doc: "HISTORY.md", wants: []string{
		"v1.29.0", "engine-isolation",
	}},
	{name: "TestVersion01291Milestone", doc: "HISTORY.md", wants: []string{
		"v1.29.1", "isolation-audit",
	}},
	{name: "TestVersion01292Milestone", doc: "HISTORY.md", wants: []string{
		"v1.29.2", "isolation-polish",
	}},
	{name: "TestVersion01280Milestone", doc: "HISTORY.md", wants: []string{
		"v1.28.0", "surface-cleanup",
	}},
	{name: "TestVersion01281Milestone", doc: "HISTORY.md", wants: []string{
		"v1.28.1", "i18n-fallback",
	}},
	{name: "TestVersion01300Milestone", doc: "HISTORY.md", wants: []string{
		"v1.30.0", "install-journey",
	}},
	{name: "TestVersion01301Milestone", doc: "HISTORY.md", wants: []string{
		"v1.30.1", "install-audit",
	}},
	{name: "TestVersion01302Milestone", doc: "HISTORY.md", wants: []string{
		"v1.30.2", "install-audit followup",
	}},
	{name: "TestVersion01310Milestone", doc: "HISTORY.md", wants: []string{
		"v1.31.0", "native-engine",
	}},
	{name: "TestVersion01311Milestone", doc: "HISTORY.md", wants: []string{
		"v1.31.1", "native-service",
	}},
	{name: "TestVersion01312Milestone", doc: "HISTORY.md", wants: []string{
		"v1.31.2", "native-rollback-guard",
	}},
	{name: "TestVersion0170Milestone", doc: "HISTORY.md", wants: []string{
		"v1.7.0", "sync",
	}},

	{name: "TestV1700CliContractSync", doc: "contracts/cli.md", wants: []string{
		"remote add", "masked", "--dry", "decay-audit", "recall_score",
	}},

	{name: "TestV1700TuningDecayKeys", doc: "TUNING.md", wants: []string{
		"decay_rate", "archive_below",
	}},
	{name: "TestV1700ReadmeSyncDecay/README.md", doc: "../README.md", wants: []string{
		"`flint sync`", "decay",
	}},
	{name: "TestV1700ReadmeSyncDecay/README.ko.md", doc: "../README.ko.md", wants: []string{
		"`flint sync`", "감쇠",
	}},
}

func TestVersionDocContracts(t *testing.T) {
	for _, c := range versionDocContracts {
		t.Run(c.name, func(t *testing.T) {
			s := docStr(t, c.doc)
			for _, w := range c.wants {
				if !strings.Contains(s, w) {
					t.Errorf("%s에 %q 누락", c.doc, w)
				}
			}
		})
	}
}

func TestDocsV06(t *testing.T) {

	reg := read(t, "../lib/wire/registry.go")
	order := ""
	inOrder := false
	for _, line := range strings.Split(reg, "\n") {
		if strings.Contains(line, "var usageOrder") {
			inOrder = true
		}
		if inOrder {
			order += line + "\n"
			if strings.TrimSpace(line) == "}" {
				break
			}
		}
	}
	if order == "" {
		t.Fatal("lib/wire/registry.go에 usageOrder 슬라이스 없음")
	}
	for _, cmd := range []string{"doctor", "report"} {
		if !strings.Contains(order, `"`+cmd+`"`) {
			t.Errorf("usageOrder에 %s 없음", cmd)
		}
	}

	plan := read(t, "HISTORY.md")
	if !strings.Contains(plan, "v0.6") {
		t.Error("PLAN.md에 v0.6 섹션 없음")
	}
	for _, item := range []string{"doctor", "report", "--stale", "stale_days", "read_count", "--source"} {
		if !strings.Contains(plan, item) {
			t.Errorf("PLAN.md v0.6에 %s 누락", item)
		}
	}

	tuning := read(t, "TUNING.md")
	for _, kv := range []string{"stale_days", "stall_hot_h", "stall_write_h"} {
		if !strings.Contains(tuning, kv) {
			t.Errorf("TUNING.md에 %s 없음", kv)
		}
	}
	for _, def := range []string{"90", "48", "72"} {
		if !strings.Contains(tuning, def) {
			t.Errorf("TUNING.md에 기본값 %s 없음", def)
		}
	}

	runbook := read(t, "RUNBOOK.md")
	if !strings.Contains(runbook, "doctor") {
		t.Error("RUNBOOK.md에 doctor 절 없음")
	}
	if !strings.Contains(runbook, "maintain --stale") {
		t.Error("RUNBOOK.md에 maintain --stale 절차 없음")
	}

	quick := read(t, "QUICKSTART.md")
	for _, cmd := range []string{"doctor", "report"} {
		if !strings.Contains(quick, cmd) {
			t.Errorf("QUICKSTART.md에 %s 예시 없음", cmd)
		}
	}
}

func TestReadmeEmbedsDiagrams(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStr(t, p)
		hasArch := strings.Contains(s, "assets/flint-architecture.svg") || strings.Contains(s, "assets/flint-architecture-en.svg")
		hasLife := strings.Contains(s, "assets/flint-lifecycle.svg") || strings.Contains(s, "assets/flint-lifecycle-en.svg")
		if !hasArch || !hasLife {
			t.Errorf("%s 다이어그램 임베드 누락", p)
		}
	}
}

func TestReadmePortIs5434(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStr(t, p)
		if strings.Contains(s, "5433") {
			t.Errorf("%s에 구 포트 5433 잔존(ADR-0010 위반)", p)
		}
		if !strings.Contains(s, "localhost:5434") {
			t.Errorf("%s에 신 포트 5434 DSN 없음", p)
		}
	}
}

func planV0100Section(t *testing.T) string {
	t.Helper()
	s := docStr(t, "../docs/HISTORY.md")
	i := strings.Index(s, "### v0.10.0")
	if i < 0 {
		t.Fatal("HISTORY.md에 v0.10.0 마일스톤 없음")
	}
	s = s[i:]
	if j := strings.Index(s, "\n## "); j >= 0 {
		s = s[:j]
	}
	return s
}

func TestPlanV0100MilestoneNumbering(t *testing.T) {
	sec := planV0100Section(t)
	for _, want := range []string{
		"R1 did-you-mean",
		"R2 NFKC",
		"R3 자동 백업 사이드카",
		"R4 doctor backup-age",
		"R5 문서 동기화",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("v0.10.0 마일스톤에 %q 없음(스펙 R1–R5 번호와 불일치)", want)
		}
	}
	if strings.Contains(sec, "R4 문서 동기화") {
		t.Errorf("v0.10.0 마일스톤이 이동 번호(R4 문서 동기화)로 회귀함")
	}
}

func TestReadmeBackupLineAttribution(t *testing.T) {
	for _, bad := range []string{
		"`flint backup`은 자동 백업 sha256",
		"`flint backup` verifies dump integrity via an automatic",
	} {
		for _, p := range []string{"../README.md", "../README.ko.md"} {
			if strings.Contains(docStr(t, p), bad) {
				t.Errorf("%s 백업 문구 오귀속(%q) — done 자동 백업이 아닌 flint backup에 귀속", p, bad)
			}
		}
	}
}

func TestSkillReadLineDocumentsOnlyRealFlags(t *testing.T) {
	s := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "`flint read ") {
			for _, ghost := range []string{"--kind"} {
				if strings.Contains(line, ghost) {
					t.Errorf("SKILL.md read 줄이 미구현 플래그 문서화: %s — %s", ghost, line)
				}
			}
		}
	}
	if !strings.Contains(s, "read --status pending") {
		t.Error("SKILL.md pending 열람(read --status pending) 안내 누락")
	}
}

func TestSkillHookCommandsMatchR4(t *testing.T) {
	skill := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(skill, "session-end") {
		t.Error("SKILL.md Stop 훅 session-end 커맨드 미언급")
	}
	if strings.Contains(skill, "Stop 훅은 `flint nudge`") {
		t.Error("SKILL.md 낡은 Stop 훅 표기(flint nudge 직접 호출)")
	}
}

func TestReadmeHookEntriesUseR4Commands(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		doc := docStr(t, p)
		for _, cmd := range []string{"session-start", "compact", "session-end"} {
			if !strings.Contains(doc, cmd) {
				t.Errorf("%s 훅 엔트리 커맨드 %s 미언급", p, cmd)
			}
		}
		if strings.Contains(doc, "SessionStart (`hot`)") {
			t.Errorf("%s 낡은 SessionStart 표기(hot 직접 호출)", p)
		}
	}
}

func TestPlanV0110MilestonesChecked(t *testing.T) {
	plan := docStr(t, "HISTORY.md")
	start := strings.Index(plan, "v0.11.0")
	end := strings.Index(plan[start:], "\n### v0.12.0")
	if start < 0 || end < 0 {
		t.Fatal("PLAN.md v0.11.0 섹션 경계 탐색 실패")
	}
	section := plan[start : start+end]
	if strings.Contains(section, "- [ ]") {
		t.Error("PLAN.md v0.11.0 섹션에 미완료 체크박스 잔존")
	}
	want := "- [x] R5 문서 동기화"
	if !strings.Contains(section, want) {
		t.Errorf("PLAN.md v0.11.0 R5 완료 표기(%q) 누락", want)
	}
}

func TestReadmeSilenceContract(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if strings.Contains(docStr(t, p), "still hits nothing") ||
			strings.Contains(docStr(t, p), "근접 후보 제안") {
			t.Errorf("%s 제안 문단 잔존", p)
		}
	}
}

func TestTuningSuggestAdjustmentObservable(t *testing.T) {
	s := docStr(t, "TUNING.md")
	section := s
	if i := strings.Index(s, "## 8."); i >= 0 {
		section = s[i:]
	}
	if end := strings.Index(section, "## 9."); end >= 0 {
		section = section[:end]
	}
	if strings.Contains(section, "top 제안을 재확인") {
		t.Error("§8 제안 출력 재확인 지침 잔존 — 제안 출력은 폐지돼 실행 불가")
	}
	if !strings.Contains(section, "zero_near_pct") {
		t.Error("§8 zero_near_pct 관측 안내 누락(§9 정합)")
	}
}

func TestExit1DisambiguationDocs(t *testing.T) {
	if !strings.Contains(docStr(t, "../README.md"), "zero-hit") || !strings.Contains(docStr(t, "../README.md"), `{"hits":0}`) {
		t.Error("README.md 제로히트 exit 0 문구 누락")
	}
	if !strings.Contains(docStr(t, "../README.ko.md"), "제로히트") || !strings.Contains(docStr(t, "../README.ko.md"), `{"hits":0}`) {
		t.Error("README.ko.md 제로히트 exit 0 문구 누락")
	}
	if !strings.Contains(docStr(t, "TUNING.md"), `{"hits":0}`) {
		t.Error("TUNING.md 제로히트 hits:0 문구 누락")
	}
}

func TestReadmeExit3TrailingWording(t *testing.T) {
	cases := []struct{ path, want, stale string }{
		{"../README.md", "valueless trailing flags", "trailing text after the query"},
		{"../README.ko.md", "값 없는 trailing 플래그", "질의 뒤 잔여 텍스트"},
	}
	for _, c := range cases {
		s := docStr(t, c.path)
		if !strings.Contains(s, c.want) || strings.Contains(s, c.stale) {
			t.Errorf("%s exit 3 문안: %q 포함·%q 부재여야 함", c.path, c.want, c.stale)
		}
	}
}

func TestPluginVersionMatchesBadge(t *testing.T) {
	m := regexp.MustCompile(`release-v(\d+\.\d+\.\d+)-blue`).FindStringSubmatch(docStr(t, "../README.md"))
	if m == nil {
		t.Fatal("README 배지 버전 파싱 실패")
	}
	manifest := docStr(t, "../flint-plugin/.claude-plugin/plugin.json")
	v := regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`).FindStringSubmatch(manifest)
	if v == nil || v[1] != m[1] {
		t.Errorf("plugin.json 버전 %q != README 배지 %q", v, m[1])
	}
}

func TestServerJsonValid(t *testing.T) {
	b, err := os.ReadFile(docPath("../mcp-registry/server.json.template"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "io.github.synuns/flint" {
		t.Errorf("name=%v", m["name"])
	}
	if d, _ := m["description"].(string); len(d) > 100 {
		t.Errorf("description %d자, want ≤100", len(d))
	}
}

func TestMcpPublisherPinned(t *testing.T) {
	ci := docStr(t, "../.github/workflows/ci.yml")
	re := regexp.MustCompile(`registry/releases/((?:latest|download/v[0-9.]+))/mcp-publisher`)
	m := re.FindStringSubmatch(ci)
	if m == nil {
		t.Fatal("ci.yml mcp-publisher 다운로드 URL 파싱 실패")
	}
	if m[1] == "latest" {
		t.Errorf("mcp-publisher URL이 버전 미고정(latest): %s", m[1])
	}
}

func TestVersionArtifactsSync(t *testing.T) {
	ver := strings.TrimSpace(docStr(t, "../VERSION"))
	if m := regexp.MustCompile(`^\d+\.\d+\.\d+$`).FindString(ver); m == "" {
		t.Fatalf("VERSION 형식 오류: %q", ver)
	}
	got := regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`).
		FindStringSubmatch(docStr(t, "../flint-plugin/.claude-plugin/plugin.json"))
	if got == nil || got[1] != ver {
		t.Errorf("plugin.json version=%v, want %s(VERSION)", got, ver)
	}
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "release-v"+ver+"-blue") {
			t.Errorf("%s 배지 v%s 미기록(VERSION 불일치)", p, ver)
		}
	}
}

func TestReadmeReleaseBadgeSingle(t *testing.T) {
	ver := strings.TrimSpace(docStr(t, "../VERSION"))
	badgeRe := regexp.MustCompile(`<img src="https://img\.shields\.io/badge/release-v(\d+\.\d+\.\d+)-blue" alt="v(\d+\.\d+\.\d+)">`)
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		ms := badgeRe.FindAllStringSubmatch(docStr(t, p), -1)
		if len(ms) != 1 {
			t.Errorf("%s release 배지 %d건, want 1(중복 배지 드리프트)", p, len(ms))
			continue
		}
		if ms[0][1] != ver || ms[0][2] != ver {
			t.Errorf("%s release 배지 src=v%s alt=v%s, want 둘 다 v%s(VERSION)", p, ms[0][1], ms[0][2], ver)
		}
	}
}

func TestPlanRearmedTwoPageContract(t *testing.T) {
	s := docStr(t, "PLAN.md")
	if n := strings.Count(s, "\n") + 1; n > 150 {
		t.Errorf("PLAN.md %d줄 — 재무장 계약(≤150줄) 초과", n)
	}
	for _, want := range []string{
		"v1.0.0",
		"wire",
		"knowledge",
		"ops",
		"render",
		"composition root",
		"비간섭",
		"HISTORY.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("PLAN.md에 %q 누락(현행 계약 요소)", want)
		}
	}
	for _, stale := range []string{
		"서브커맨드 14",
		"read/write/hot MCP",
		"SessionStart→flint hot",
		"self-update",
	} {
		if strings.Contains(s, stale) {
			t.Errorf("PLAN.md에 구 본문 모순 표기 %q 잔존", stale)
		}
	}
	if m := regexp.MustCompile(`\d+(?:\.\d+)?%`).FindString(s); m != "" {
		t.Errorf("PLAN.md에 실측 백분율 %q 인용(저장소 원칙 위반)", m)
	}
}

var contractsSemverRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

func pinnedExamples(doc string) []string {
	var pinned []string
	check := func(span string) {
		if contractsSemverRe.MatchString(span) {
			pinned = append(pinned, span)
		}
	}
	for _, block := range regexp.MustCompile("(?s)```.*?```").FindAllString(doc, -1) {
		check(block)
	}
	withoutFences := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(doc, "")
	for _, span := range regexp.MustCompile("`[^`\n]*`").FindAllString(withoutFences, -1) {
		check(span)
	}
	return pinned
}

func TestContractsExamplesNotPinned(t *testing.T) {
	files, err := filepath.Glob("../../docs/contracts/*.md")
	if err != nil || len(files) != 6 {
		t.Fatalf("contracts/*.md 6종 탐색 실패: %v %v", files, err)
	}
	for _, f := range files {
		for _, span := range pinnedExamples(docStr(t, f)) {
			t.Errorf("%s 예시(펜스·인라인)에 버전 고정 포함 — 버전 불문 서술로(드리프트 원천): %q", f, span)
		}
	}
}

func TestPinnedExamplesCoverInlineBackticks(t *testing.T) {
	doc := "설치는 `flint@0.43.0` 태그를 쓴다.\n\n```sh\n/plugin install flint@latest\n```\n"
	got := pinnedExamples(doc)
	if len(got) != 1 || !strings.Contains(got[0], "flint@0.43.0") {
		t.Fatalf("인라인 백틱 버전 고정 미검출: %v", got)
	}
	if got := pinnedExamples("v0.12.0부터 도입된 기능이다(산문 서술)."); len(got) != 0 {
		t.Fatalf("산문 버전 언급 오탐: %v", got)
	}
}

func TestHistoryCoversAllErasChronological(t *testing.T) {
	s := docStr(t, "HISTORY.md")
	anchors := []string{
		"### v0.19.0", "### v0.20.0", "### v0.21.0", "### v0.22.0",
		"### v0.23.0", "### v0.24.0", "### v0.25.0", "### v0.26.0",
		"### v0.27.0", "### v0.28.0", "### v0.29.0", "### v0.30.0",
		"### v0.31.0", "### v0.32.0", "### v0.33.0", "### v0.34.0",
		"### v0.35.0", "### v0.36.0", "### v0.37.0", "### v0.38.0",
		"### v0.39.0", "### v0.40.0", "### v0.41.0", "### v0.42.0",
		"### v0.43.0", "### v0.44.0", "### v1.0.0",
	}
	if !strings.Contains(s, "v0.1~v1.2.0") {
		t.Error("HISTORY.md 헤더 범위 'v0.1~v1.0.0' 없음")
	}
	prev := -1
	for _, a := range anchors {
		i := strings.Index(s, a)
		if i < 0 {
			t.Errorf("HISTORY.md에 %s 엔트리 없음(§14 이관 누락)", a)
			continue
		}
		if prev >= 0 && i < prev {
			t.Errorf("HISTORY.md 시간순 위반: %s가 이전 엔트리보다 앞섬", a)
		}
		prev = i
	}
}

func TestHistoryEntryDatesMatchMerges(t *testing.T) {
	s := docStr(t, "HISTORY.md")
	for _, h := range []string{
		"### v0.41.0 마이너 (2026-11-12)",
		"### v0.42.0 마이너 (2026-11-12)",
		"### v0.43.0 마이너 (2026-11-12)",
		"### v0.44.0 마이너 (2026-11-12)",
		"### v1.0.0 메이저 (2026-11-12)",
	} {
		if !strings.Contains(s, h) {
			t.Errorf("HISTORY.md에 머지일 제목 %q 없음", h)
		}
	}
	for _, stale := range []string{
		"### v0.41.0 마이너 (2026-11-13)",
		"### v0.42.0 마이너 (2026-11-13)",
		"### v0.43.0 마이너 (2026-11-13)",
	} {
		if strings.Contains(s, stale) {
			t.Errorf("HISTORY.md에 계획일 오프셋 제목 %q 잔존", stale)
		}
	}
	if !strings.Contains(docStr(t, "PLAN.md"), "2026-11-12 v1.0.0") {
		t.Error("PLAN.md 날짜줄에 '2026-11-12 v1.0.0' 없음(머지일 오프셋)")
	}
}

func TestCliContractExitSemantics(t *testing.T) {
	s := docStr(t, "contracts/cli.md")
	for _, want := range []string{
		"invalid arguments and validation failures",
		"zero-hit silence",
		"isError",
		"per-row JSONL",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("contracts/cli.md에 %q 누락", want)
		}
	}
	for _, stale := range []string{
		"| 3 | 스키마 불일치(schema) |",
		`{"rows":[`,
	} {
		if strings.Contains(s, stale) {
			t.Errorf("contracts/cli.md에 낡은 서술 %q 잔존", stale)
		}
	}
}

func TestReadmeHighlightMatchesVersion(t *testing.T) {
	ver := strings.TrimSpace(read(t, "../VERSION"))
	for _, name := range []string{"../README.md", "../README.ko.md"} {
		doc := read(t, name)
		if !strings.Contains(doc, "> v"+ver+" ") {
			t.Errorf("%s 하이라이트 줄에 v%s 없음(최신 버전 동기)", name, ver)
		}
	}
}

func TestV1700SkillOmitsSync(t *testing.T) {
	s := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	if strings.Contains(s, "flint sync") {
		t.Error("SKILL.md에 `flint sync` 안내 존재 — sync는 사람 운영자 명령(미기재 판정 위반)")
	}
	if !strings.Contains(s, "운영자") {
		t.Error("SKILL.md에 sync 미기재 판정(운영자 명령) 근거 문구 없음")
	}
}
