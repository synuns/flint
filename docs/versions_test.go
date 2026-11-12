// versions_test.go — 버전별 문서 회귀 테스트 통합(v0.6~v0.20.0, 26파일→1).
// 단순 키워드 계약(docStr + Contains 루프)은 versionDocContracts 표로,
// 부정 단언·정렬·정규식·교차 검증 등 커스텀 로직은 원본 함수명 그대로
// 이 파일에 보존한다. 기존 파일명·함수명은 표 name으로 추적 가능하다.
package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// versionDocContracts — 버전별 문서 키워드 계약. 행 name은 원본 테스트
// 함수명(v0XX_docs_test.go 소속), 다중 문서 루프는 /구분자로 분할.
var versionDocContracts = []struct {
	name  string
	doc   string
	wants []string
}{
	// v09_docs_test.go — T3 어댑터 문서 계약(codex·gemini·copilot).
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
		"조정 가이드", "재조정 트리거", "daily_retention_days", "backup_interval_h",
		"vocab_tags", "방어 상한",
	}},
	// v0.44.0 T4: 릴리스 엔트리는 재무장본 PLAN의 에라 표·헤더로 기록
	// (§14 부재 — 병인 ⑤ 해소 완결 선언).
	{name: "TestVersion0440Milestone", doc: "PLAN.md", wants: []string{
		"v0.44.0", "병인 ⑤ 해소",
	}},
	// v0.44.0 T1: §14 전체 엔트리가 HISTORY.md로 이관됨 — 계약 의미(버전
	// 기록 존재)는 불변, 위치만 재지정.
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

	// v093_docs_test.go
	{name: "TestPlanRecordsV092AndV093", doc: "HISTORY.md", wants: []string{"v0.9.2", "v0.9.3"}},

	// v096_docs_test.go
	{name: "TestReadmeMentionsFallback/README.md", doc: "../README.md", wants: []string{"fallback"}},
	{name: "TestReadmeMentionsFallback/README.ko.md", doc: "../README.ko.md", wants: []string{"fallback"}},
	{name: "TestPlanRecordsV095AndV096", doc: "HISTORY.md", wants: []string{"v0.9.5", "v0.9.6"}},

	// v0100_docs_test.go — "suggestions" 문자열 요구는 v0.14.0 침묵 계약으로
	// 철회(README v0.17.0 재작성) — 백업 무결성만 단얱.
	{name: "TestReadmeMentionsSuggest/README.md", doc: "../README.md", wants: []string{"sha256"}},
	{name: "TestReadmeMentionsSuggest/README.ko.md", doc: "../README.ko.md", wants: []string{"sha256"}},

	// v0101_docs_test.go
	{name: "TestTuningRecordsV0101Constants", doc: "TUNING.md", wants: []string{
		"backupAgeWarn", "72", "suggestMinScore", "0.15",
	}},
	// backupAgeWarn 근거 문장이 doctor의 warn 조건(age >= backupAgeWarn)과
	// 모순되지 않는지: 2회 연속 실패는 경고 유예, 3회째부터 경고임을 명시.
	{name: "TestTuningBackupAgeWarnRationaleMatchesCode", doc: "TUNING.md", wants: []string{
		"2회 연속 실패까지는 경고하지 않고",
	}},

	// v0102_docs_test.go
	{name: "TestPlanRecordsV094AndV097", doc: "HISTORY.md", wants: []string{"v0.9.4", "v0.9.7"}},
	{name: "TestSkillDocumentsReadFlags", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{
		"--tag", "--entity", "--domain", "--limit",
	}},

	// v0110_docs_test.go
	{name: "TestSkillMentionsHotFull", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"hot --full"}},
	{name: "TestReadmeMentionsExposure/README.md", doc: "../README.md", wants: []string{"exposure"}},
	{name: "TestReadmeMentionsExposure/README.ko.md", doc: "../README.ko.md", wants: []string{"exposure"}},
	{name: "TestPlanRecordsV0110", doc: "HISTORY.md", wants: []string{"v0.11.0"}},

	// v0120_docs_test.go
	{name: "TestSkillDocumentsReadStatus", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"read --status pending"}},
	{name: "TestPlanRecordsV0120", doc: "HISTORY.md", wants: []string{"v0.12.0"}},

	// v0130_docs_test.go — TestReadmeMentionsAdoption은 v0.17.0 README 재작성으로 요구 철회.
	{name: "TestPlanRecordsV0130", doc: "HISTORY.md", wants: []string{"v0.13.0"}},
	{name: "TestTuningRecordsConflictThreshold", doc: "TUNING.md", wants: []string{"conflictDupThreshold", "0.6"}},

	// v0140_docs_test.go
	{name: "TestPlanRecordsV0140", doc: "HISTORY.md", wants: []string{"v0.14.0"}},
	{name: "TestTuningRecordsZeroNear", doc: "TUNING.md", wants: []string{"zero_near"}},

	// v0150_docs_test.go — exit 3 서술은 v0.17.1 MapExit SchemaError 전반으로 확장.
	{name: "TestPlanRecordsV0150", doc: "HISTORY.md", wants: []string{"v0.15.0"}},
	{name: "TestTuningRecordsWindowed", doc: "TUNING.md", wants: []string{"read_zero_near", "30"}},
	{name: "TestReadmeExitTable/README.md", doc: "../README.md", wants: []string{
		"exit 3", "invalid input (valueless trailing flags",
	}},
	{name: "TestReadmeExitTable/README.ko.md", doc: "../README.ko.md", wants: []string{
		"exit 3", "잘못된 입력 (값 없는 trailing 플래그",
	}},

	// v0151_docs_test.go
	{name: "TestPlanRecordsV0151", doc: "HISTORY.md", wants: []string{"v0.15.1"}},

	// v0152_docs_test.go
	{name: "TestPlanRecordsV0152", doc: "HISTORY.md", wants: []string{"v0.15.2"}},

	// v0160_docs_test.go
	{name: "TestPlanRecordsV0160", doc: "HISTORY.md", wants: []string{"v0.16.0"}},
	{name: "TestCiTriggersOnVersionTags", doc: "../.github/workflows/ci.yml", wants: []string{`tags: ["v*"]`}},
	{name: "TestReadmePluginInstall/README.md", doc: "../README.md", wants: []string{"marketplace add synuns/flint"}},
	{name: "TestReadmePluginInstall/README.ko.md", doc: "../README.ko.md", wants: []string{"marketplace add synuns/flint"}},

	// v0161_docs_test.go
	{name: "TestPlanRecordsV0161", doc: "HISTORY.md", wants: []string{"v0.16.1"}},
	{name: "TestReadmePathRequirement/README.md", doc: "../README.md", wants: []string{"PATH"}},
	{name: "TestReadmePathRequirement/README.ko.md", doc: "../README.ko.md", wants: []string{"PATH"}},

	// v0162_docs_test.go
	{name: "TestSkillWriteProtocol", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{
		"갱신 우선", "최소화", "flint read",
	}},
	{name: "TestPlanRecordsV0162", doc: "HISTORY.md", wants: []string{"v0.16.2"}},

	// v0163_docs_test.go
	{name: "TestPlanRecordsV0163", doc: "HISTORY.md", wants: []string{"v0.16.3"}},
	{name: "TestTuningKeysV0163Documented", doc: "TUNING.md", wants: []string{
		"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
		"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
		"pending_age_days",
	}},
	{name: "TestSkillRationaleNeutral", doc: "../flint-plugin/skills/flint/SKILL.md", wants: []string{"중복 재제출"}},

	// v0165_docs_test.go
	{name: "TestReadmeSetupMcp/README.md", doc: "../README.md", wants: []string{"setup --mcp"}},
	{name: "TestReadmeSetupMcp/README.ko.md", doc: "../README.ko.md", wants: []string{"setup --mcp"}},

	// v0170_docs_test.go — R1: dependabot 2 ecosystem 주간 업데이트.
	{name: "TestDependabotConfigured", doc: "../.github/dependabot.yml", wants: []string{
		`package-ecosystem: "gomod"`,
		`package-ecosystem: "github-actions"`,
		`interval: "weekly"`,
	}},

	// v0180_docs_test.go — recall_warn_writes는 thresholds 레지스트리+TUNING §2 동기 키.
	{name: "TestVersion0180Milestone/PLAN", doc: "HISTORY.md", wants: []string{"v0.18.0", "회상 우선 메모리"}},
	{name: "TestVersion0180Milestone/TUNING", doc: "TUNING.md", wants: []string{"recall_warn_writes"}},

	// v0190_docs_test.go — METRICS.md는 GSM 목표 4종·지표명·증거 등급·금지조항의 단일 소스.
	{name: "TestVersion0190Milestone", doc: "HISTORY.md", wants: []string{"v0.19.0", "효과 계측"}},
	{name: "TestMetricsDocContract", doc: "METRICS.md", wants: []string{
		"목표→신호→지표", // GSM 원칙
		"G1 회상 명중", // 목표 4종
		"G2 재발견 저지",
		"G3 자산 건전성",
		"G4 회상 우선 정착",
		"hit_pct", "near_pct", "fallback_pct", "dup_prevented",
		"reuse_pct", "dead_pct", "read_write_pairs", "pair_pct",
		"Tier 1", "Tier 2", // 증거 등급
		"금지조항", // 반론 방어
	}},

	// v0200_docs_test.go — 신지표·계측 원천 3테이블·폴백 규칙(문서-코드 정합).
	{name: "TestVersion0200Milestone", doc: "HISTORY.md", wants: []string{
		"v0.20.0", "효과 계측 정밀화", "기저율 셔플(C) 이연",
	}},
	{name: "TestMetricsDocV0200", doc: "METRICS.md", wants: []string{
		"recall_led_pct", "chain_pct",
		"session_starts", "hot_events",
		"구배포·계측 첫일", // 시간창 폴백 조건 문서화
		"최근 30일 hot 노출 기록 제외",
		"기저율 셔플 이연",
	}},
}

// TestVersionDocContracts — 표 계약 러너: 각 문서가 wants 키워드를 전부 담는지.
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

// ── 커스텀 로직 보존(부정 단언·교차·정규식 — 표로 환원 불가) ──────────────

// TestDocsV06 — T69 문서 계약 검증. v0.6 신규 커맨드(doctor·report)가
// usageLine·4개 문서(PLAN·TUNING·RUNBOOK·QUICKSTART)에 누락 없이 반영됐는지
// grep 정합 검증한다. 체크리스트 [x] 표기는 마일스톤 종료 시점이므로 여기서는
// 섹션 존재만 확인한다.
func TestDocsV06(t *testing.T) {
	// ① usageLine(v0.34.2부터 usageOrder 슬라이스에서 조립 — 리터럴 한 줄
	// grep 전제는 폐기. v0.38.0 §1부터 슬라이스는 lib/wire/registry.go에
	// 있다)에 신규 2커맨드. 슬라이스 블록을 추출해 항목 존재를 검사한다
	// (단언 의도 불변).
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

	// ② 이력(HISTORY.md — v0.21.2 아카이브 이전에는 PLAN §14)의 v0.6 섹션.
	plan := read(t, "HISTORY.md")
	if !strings.Contains(plan, "v0.6") {
		t.Error("PLAN.md에 v0.6 섹션 없음")
	}
	for _, item := range []string{"doctor", "report", "--stale", "stale_days", "read_count", "--source"} {
		if !strings.Contains(plan, item) {
			t.Errorf("PLAN.md v0.6에 %s 누락", item)
		}
	}

	// ③ TUNING.md §2 임계값 표에 3키(기본값 포함).
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

	// ④ RUNBOOK.md에 doctor 5검사·maintain --stale 절.
	runbook := read(t, "RUNBOOK.md")
	if !strings.Contains(runbook, "doctor") {
		t.Error("RUNBOOK.md에 doctor 절 없음")
	}
	if !strings.Contains(runbook, "maintain --stale") {
		t.Error("RUNBOOK.md에 maintain --stale 절차 없음")
	}

	// ⑤ QUICKSTART.md 확인 커맨드에 doctor·report 예시.
	quick := read(t, "QUICKSTART.md")
	for _, cmd := range []string{"doctor", "report"} {
		if !strings.Contains(quick, cmd) {
			t.Errorf("QUICKSTART.md에 %s 예시 없음", cmd)
		}
	}
}

// TestReadmeEmbedsDiagrams — README EN/KO가 아키텍처·라이프사이클 다이어그램을
// 각각 임베드하는지(EN/로케일 변형 허용).
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

// TestReadmePortIs5434 — ADR-0010: README의 DSN·포트 안내가 5434로 전환됐는지
// (v0.9.7 리뷰 — EN/KO 각 4곳 5433 잔존 발견의 회귀 방지).
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

// planV0100Section — PLAN.md에서 v0.10.0 마일스톤 섹션(### v0.10.0부터
// 다음 ## 헤더 전)까지를 잘라 반환한다.
func planV0100Section(t *testing.T) string {
	t.Helper()
	s := docStr(t, "../docs/HISTORY.md") // v0.21.2 아카이브 이관
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

// TestPlanV0100MilestoneNumbering — 마일스톤 R번호가 스펙(§3 R1–R5)·
// 구현 커밋(T2:R3·T3:R4·T4:R5)과 일치하는지. 압축·이동 번호 회귀 방지.
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

// TestReadmeBackupLineAttribution — sha256 사이드카 신규 적용 대상은
// `flint done` 자동 백업(R3). `flint backup`에 오귀속한 문구 회귀 방지.
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

// TestSkillReadLineDocumentsOnlyRealFlags — read는 runRead(lib/cli/read.go)가
// 받는 플래그만 문서화해야 한다. --kind는 파싱되지 않는 미구현 플래그라
// read 줄에 등장하면 안 된다. --status는 v0.12.0 R1로 실구현되어
// `read --status pending` 안내가 SKILL.md에 존재해야 한다.
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

// TestSkillHookCommandsMatchR4 — SKILL.md 훅 설명이 v0.11.0 R4 이후
// 실제 hooks.json 커맨드(session-start·session-end)와 정합해야 한다.
// 낡은 직접 호출 표기("Stop 훅은 `flint nudge`")가 부활하면 실패.
func TestSkillHookCommandsMatchR4(t *testing.T) {
	skill := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(skill, "session-end") {
		t.Error("SKILL.md Stop 훅 session-end 커맨드 미언급")
	}
	if strings.Contains(skill, "Stop 훅은 `flint nudge`") {
		t.Error("SKILL.md 낡은 Stop 훅 표기(flint nudge 직접 호출)")
	}
}

// TestReadmeHookEntriesUseR4Commands — README(EN/KO) 훅 3엔트리 괄호
// 표기가 실제 설치 커맨드(session-start·compact·session-end)와 일치.
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

// TestPlanV0110MilestonesChecked — R1~R5 전 구현 완료 시점부터 v0.11.0
// 마일스톤 섹션에 미완료 체크박스가 남으면 실패(관례: 완료 항목 [x]).
func TestPlanV0110MilestonesChecked(t *testing.T) {
	plan := docStr(t, "HISTORY.md") // v0.21.2 아카이브 이관
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

// TestReadmeSilenceContract — v0.14.0 침묵 계약: README에 폐지된 제안 문단이
// 부활하면 실패.
func TestReadmeSilenceContract(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if strings.Contains(docStr(t, p), "still hits nothing") ||
			strings.Contains(docStr(t, p), "근접 후보 제안") {
			t.Errorf("%s 제안 문단 잔존", p)
		}
	}
}

// TestTuningSuggestAdjustmentObservable — §8 suggestMinScore 조정 지침은
// 폐지된 제안 출력 재확인 대신 zero_near_pct(`flint report`) 관측을 안내한다.
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

// TestExit1DisambiguationDocs — exit 1 구분(무출력=침묵 정상 · 오류 JSON 한
// 줄 동반=실패)이 README EN/KO·TUNING에 문서화됐다.
func TestExit1DisambiguationDocs(t *testing.T) {
	if !strings.Contains(docStr(t, "../README.md"), "silent no-results") && !strings.Contains(docStr(t, "../README.md"), "exit 1 with no output") {
		t.Error("README.md exit 1 구분 문구 누락")
	}
	if !strings.Contains(docStr(t, "../README.ko.md"), "무결과 침묵") && !strings.Contains(docStr(t, "../README.ko.md"), "출력 없는 exit 1") {
		t.Error("README.ko.md exit 1 구분 문구 누락")
	}
	if !strings.Contains(docStr(t, "TUNING.md"), "무출력") {
		t.Error("TUNING.md exit 1 구분 문구 누락")
	}
}

// TestReadmeExit3TrailingWording — exit 3의 trailing은 값 없는 trailing
// 플래그(F1/N10)다. read는 위치 인자를 전부 질의로 join하므로 "질의 뒤
// 잔여 텍스트"는 exit 3이 아니라는 오표현을 방어한다.
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

// TestPluginVersionMatchesBadge — plugin.json 버전이 README 배지와 동기되는지
// — v0.16.3 범프 누락 회귀(역대 릴리스는 항상 범프됐으나 v0.16.3에서 누락됐었다).
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

// TestServerJsonValid — mcp-registry server.json 스키마 계약(이름·설명 길이).
func TestServerJsonValid(t *testing.T) {
	b, err := os.ReadFile("../mcp-registry/server.json")
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

// TestMcpPublisherPinned — R1: CI mcp-publisher 다운로드가 latest 부유가
// 아닌 특정 태그로 고정됐는지(재현성).
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

// TestVersionArtifactsSync — 버전 동기 가드의 범용화(v0.17.1): 단일 버전
// 소스(VERSION)를 기준으로 VERSION = plugin.json = EN/KO 배지 4종 일치.
// 버전별 하드코딩(TestVersion0170 등)을 폐지 — 범프마다 테스트를 고칠
// 필요가 없어지고 누락 가드는 그대로 유지된다.
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

// TestReadmeReleaseBadgeSingle — v1.0.1 감사(릴리스 동기): release 배지는
// README당 1개뿐이어야 한다. 릴리스마다 배지를 갱신하지 않고 추가해 온
// 드리프트가 v1.0.1에서 동일 src 3중 배지(alt만 v0.43.0·v0.44.0·v1.0.1)
// 로 드러났다 — src 전량 동기(갱신)는 맞지만 중복 행은 남아 있다. 배지
// 1개·alt=VERSION 동기까지 강제한다.
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

// TestPlanRearmedTwoPageContract — v0.44.0 T1(PLAN 재무장): PLAN.md는
// 현행 계약 2페이지(≤150줄)로 유지된다. ① 줄 수 상한 ② 현행 구조·원칙·
// v1.0.0 마일스톤 서술 ③ 구 본문 4개 모순 지점(서브커맨드 14·MCP 도구
// 3종·훅 2종·실현된 비목표) 부재 ④ 실측 백분율 부재(저장소 최상위 원칙).
func TestPlanRearmedTwoPageContract(t *testing.T) {
	s := docStr(t, "PLAN.md")
	if n := strings.Count(s, "\n") + 1; n > 150 {
		t.Errorf("PLAN.md %d줄 — 재무장 계약(≤150줄) 초과", n)
	}
	for _, want := range []string{
		"v1.0.0",           // 다음 마일스톤 1개
		"wire",             // 계약층
		"knowledge",        // 지식 명령층
		"ops",              // 진단·정리·관측층
		"render",           // 출력층
		"composition root", // cli 구성 루트
		"비간섭",              // 5원칙 승계
		"HISTORY.md",       // 이력 이관처 안내
	} {
		if !strings.Contains(s, want) {
			t.Errorf("PLAN.md에 %q 누락(현행 계약 요소)", want)
		}
	}
	for _, stale := range []string{
		"서브커맨드 14",               // 실제는 25+2종
		"read/write/hot MCP",     // 실제 MCP 도구는 5종
		"SessionStart→flint hot", // 실제 훅은 3종(session-start·session-end·PostCompact)
		"self-update",            // v0.22.0 flint update로 실현 — 비목표 잔류 모순
	} {
		if strings.Contains(s, stale) {
			t.Errorf("PLAN.md에 구 본문 모순 표기 %q 잔존", stale)
		}
	}
	if m := regexp.MustCompile(`\d+(?:\.\d+)?%`).FindString(s); m != "" {
		t.Errorf("PLAN.md에 실측 백분율 %q 인용(저장소 원칙 위반)", m)
	}
}

// contractsSemverRe — 고정 금지 대상 semver(단어 경계).
var contractsSemverRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

// pinnedExamples — 계약 문서 본문에서 버전 숫자를 고정한 '예시' 조각을
// 전부 반환한다. 예시 범위 v1.0.1 R1-D 확장: 펜스 코드 블록(``` ```)
// 뿐 아니라 인라인 백틱 스팬(`flint@0.43.0` — 펜스 밖이라 구 가드가
// 못 봤다)도 예시로 취급한다. 백틱 없는 산문의 버전 서술("v0.12.0부터"
// 류 역사 언급)은 예시가 아니므로 대상 아니다.
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

// TestContractsExamplesNotPinned — v0.44.0 리뷰: 계약 문서(docs/contracts/)
// 예시(펜스 블록·인라인 백틱)는 버전 숫자를 고정하지 않는다. plugin.md가
// 릴리스 직후 구버전(0.43.0) 예시로 부실화된 사례의 재발 방지 — 표면
// 문서는 버전 동기 게이트(TestVersionArtifactsSync) 대상이 아니므로
// 여기서 금지.
func TestContractsExamplesNotPinned(t *testing.T) {
	files, err := filepath.Glob("contracts/*.md")
	if err != nil || len(files) != 6 {
		t.Fatalf("contracts/*.md 6종 탐색 실패: %v %v", files, err)
	}
	for _, f := range files {
		for _, span := range pinnedExamples(docStr(t, f)) {
			t.Errorf("%s 예시(펜스·인라인)에 버전 고정 포함 — 버전 불문 서술로(드리프트 원천): %q", f, span)
		}
	}
}

// TestPinnedExamplesCoverInlineBackticks — R1-D 회귀: 인라인 백틱 버전
// 고정(`flint@0.43.0`)도 검출되고, 백틱 없는 산문 버전 언급은 오탐
// 아니어야 한다(합성 문서로 단언).
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

// TestHistoryCoversAllErasChronological — v0.44.0 T1(HISTORY 이관): §14
// 전체 엔트리(v0.19.0+)가 HISTORY.md로 이관됐고 시간순(오름차순) 배열.
// v1.0.1 감사 F7: v0.44.0·v1.0.0 엔트리도 이관 계약상 여기 있어야
// 한다(헤더 "v0.1~v1.0.0" 범위와 일치).
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
	if !strings.Contains(s, "v0.1~v1.0.0") {
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

// TestHistoryEntryDatesMatchMerges — v1.0.1 감사 F7(날짜 오프셋): v0.41~
// v1.0.0 엔트리 제목 날짜는 실제 릴리스 머지일(2026-11-12)이다. 계획일
// 오프셋(2026-11-13) 제목 회귀 방지 — 세부 링크의 plans/ 파일명은 제외.
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

// TestCliContractExitSemantics — v1.0.1 감사 F7: contracts/cli.md exit 표는
// 코드(MapExit·read 침묵 계약·doctor fail)와 정합 — exit 3은 무효 인자·
// 검증 실패(SchemaError)지 스키마 버전 불일치가 아니고(그건 doctor fail
// = exit 1), exit 1 과적재와 MCP isError 출력 유무 판정·실제 read 출력
// 형식이 명시된다.
func TestCliContractExitSemantics(t *testing.T) {
	s := docStr(t, "contracts/cli.md")
	for _, want := range []string{
		"무효 인자·검증 실패",
		"제로히트 침묵",
		"isError",
		"행별 JSONL",
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

// TestReadmeHighlightMatchesVersion — v0.20.3(패치 루프 3차): README
// 인용 하이라이트 줄은 최신 VERSION을 언급해야 한다. 이 줄은 배지·
// plugin.json 동기 게이트 밖이라 3마이너 방치됐던 전례(패치 루프 3차).
func TestReadmeHighlightMatchesVersion(t *testing.T) {
	ver := strings.TrimSpace(read(t, "../VERSION"))
	for _, name := range []string{"../README.md", "../README.ko.md"} {
		doc := read(t, name)
		if !strings.Contains(doc, "> v"+ver+" ") {
			t.Errorf("%s 하이라이트 줄에 v%s 없음(최신 버전 동기)", name, ver)
		}
	}
}
