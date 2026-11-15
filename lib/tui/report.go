// report.go — v1.5.0 T9: `flint report --tui` 화면 모델. 진단 카드 목록
// (심각도 색)·선택 상세(Basis·Action·tuning key)·지표 요약 탐색 뷰.
// 뷰 전용 — 쓰기 없음. 데이터는 호출부(cli 진입 분기)가 기존 ReportOutput
// 수집 경로(JSON 뷰와 동일 원천)에서 모은 산물을 그대로 받는다 — TUI가
// 별도 지표를 만들지 않는다(3뷰+TUI=4뷰 동일 원천 원칙).
// 키: ↑/k·↓/j 이동 · enter 상세 확장/축소 · tab 진단↔지표 뷰 · q/esc 종료.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/synuns/flint/lib/ops"
)

// 심각도 스타일 — 테마 철학 승계(ANSI 기본색 번호만 — 터미널 팔레트 존중).
// DiagnosisItem에 심각도 필드가 없어 신호 id로 파생한다(severityOf).
var (
	sevWarn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")) // 경고: 노랑
	sevInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))            // 정보: 시안
)

// severityOf — 진단 신호 id → 심각도 라벨. 유일하게 '정보성'으로 규정된
// hot_budget_slack만 info, 나머지는 warn(진단 문구 계약 기준 파생).
func severityOf(signal string) string {
	if signal == "hot_budget_slack" {
		return "info"
	}
	return "warn"
}

// ReportModel — report TUI 화면. mode 0=진단 카드, 1=지표 요약.
type ReportModel struct {
	rep    ops.ReportOutput
	days   int
	cursor int
	mode   int // 0 diagnosis · 1 metrics
	detail bool
}

// NewReportModel — 수집 산물(ReportOutput)과 창 일수로 화면 모델 구성.
func NewReportModel(rep ops.ReportOutput, days int) ReportModel {
	return ReportModel{rep: rep, days: days}
}

// Init — 시작 커맨드 없음.
func (m ReportModel) Init() tea.Cmd { return nil }

// Update — 키 입력만 처리(뷰 전용 — 외부 메시지 없음).
func (m ReportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		switch string(key.Runes) {
		case "q":
			return m, tea.Quit
		case "j":
			if m.mode == 0 && m.cursor < len(m.rep.Diagnosis)-1 {
				m.cursor++
			}
		case "k":
			if m.mode == 0 && m.cursor > 0 {
				m.cursor--
			}
		}
	case tea.KeyDown:
		if m.mode == 0 && m.cursor < len(m.rep.Diagnosis)-1 {
			m.cursor++
		}
	case tea.KeyUp:
		if m.mode == 0 && m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyEnter:
		m.detail = !m.detail
	case tea.KeyTab:
		m.mode = 1 - m.mode
	}
	return m, nil
}

// View — 현재 모드 렌더.
func (m ReportModel) View() string {
	var b strings.Builder
	title := fmt.Sprintf("flint report — Diagnosis (%d-day window)", m.days)
	if m.mode == 1 {
		title = fmt.Sprintf("flint report — Metrics (%d-day window)", m.days)
	}
	b.WriteString(Theme.Accent.Render(title) + "\n\n")
	if m.mode == 1 {
		b.WriteString(m.metricsView())
		b.WriteString("\n" + Theme.Secondary.Render("tab: diagnosis · q: quit"))
		return b.String()
	}
	b.WriteString(m.diagnosisView())
	b.WriteString("\n" + Theme.Secondary.Render("↑/k ↓/j: move · enter: detail · tab: metrics · q: quit"))
	return b.String()
}

// diagnosisView — 진단 카드 목록 렌더. 0건은 정상 침묵 대응 안내 문장.
func (m ReportModel) diagnosisView() string {
	if len(m.rep.Diagnosis) == 0 {
		return Theme.Neutral.Render("No diagnosis findings — all quiet.") + "\n"
	}
	var b strings.Builder
	for i, d := range m.rep.Diagnosis {
		marker, style := "  ", sevWarn
		if severityOf(d.Signal) == "info" {
			style = sevInfo
		}
		if i == m.cursor {
			marker = "▸ "
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", marker, d.Signal)))
		b.WriteString("  " + d.Value + "\n")
		if i == m.cursor && m.detail {
			for _, line := range []struct{ label, val string }{
				{"Basis:", d.Basis},
				{"Action:", d.Action},
				{"Tuning key:", d.TuningKey},
			} {
				if line.val == "" {
					continue // tuning_key 없는 규칙은 행 생략(omitempty 대응)
				}
				b.WriteString(fmt.Sprintf("    %s %s\n", Theme.Neutral.Render(line.label), line.val))
			}
		}
	}
	return b.String()
}

// metricsView — 지표 요약. text 뷰(printReportText)와 같은 원천 필드의
// 영문 요약(계열 재계산 없음 — Days 합산만).
func (m ReportModel) metricsView() string {
	var write, read, dup int
	for _, d := range m.rep.Days {
		write += d.Write
		read += d.Read
		dup += d.Dup
	}
	e := m.rep.Effect
	return fmt.Sprintf(
		"writes %d · reads %d · dup prevented %d (dup %d%%)\n"+
			"zero-hit %d%% · zero-near %d%% · fallback %d%% · hit %d%%\n"+
			"active %d · pending %d · invalidated %d · stale 90d+ %d · never read %d\n"+
			"hot p95 %d\n",
		write, read, dup, m.rep.DupPct,
		m.rep.ZeroPct, m.rep.ZeroNearPct, m.rep.FallbackPct, e.HitPct,
		m.rep.Counts.Active, m.rep.Counts.Pending, m.rep.Counts.Invalidated,
		m.rep.Stale.Days90, m.rep.Stale.NeverRead,
		m.rep.HotP95,
	)
}

// RunReportTUI — 수집 완료된 ReportOutput으로 화면 기동(T5 Run 래퍼 —
// 비터미널은 즉시 오류, 호출부 exit 3 폴백).
func RunReportTUI(rep ops.ReportOutput, days int) error {
	return Run(NewReportModel(rep, days))
}
