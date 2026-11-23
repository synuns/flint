package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/synuns/flint/lib/ops"
)

var (
	sevWarn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	sevInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

func severityOf(signal string) string {
	if signal == "hot_budget_slack" {
		return "info"
	}
	return "warn"
}

type ReportModel struct {
	rep    ops.ReportOutput
	days   int
	cursor int
	mode   int
	detail bool
	width  int
}

func NewReportModel(rep ops.ReportOutput, days int) ReportModel {
	return ReportModel{rep: rep, days: days}
}

func (m ReportModel) Init() tea.Cmd { return nil }

func (m ReportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = sz.Width
		return m, nil
	}
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

func (m ReportModel) View() string {
	var b strings.Builder
	title := fmt.Sprintf("flint report — Diagnosis (%d-day window)", m.days)
	if m.mode == 1 {
		title = fmt.Sprintf("flint report — Metrics (%d-day window)", m.days)
	}
	b.WriteString(Theme.Accent.Render(title) + "\n\n")
	if m.mode == 1 {
		b.WriteString(m.metricsView())
		b.WriteString("\n" + Theme.Secondary.Render(keyHint(m.width, "tab: diagnosis · q: quit", "tab · q quit")))
		b.WriteString("\n" + Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
		return b.String()
	}
	b.WriteString(m.diagnosisView())
	b.WriteString("\n" + Theme.Secondary.Render(keyHint(m.width,
		"↑/k ↓/j: move · enter: detail · tab: metrics · q: quit",
		"j/k·enter·tab · q quit")))
	b.WriteString("\n" + Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	return b.String()
}

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
					continue
				}
				b.WriteString(fmt.Sprintf("    %s %s\n", Theme.Neutral.Render(line.label), line.val))
			}
		}
	}
	return b.String()
}

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

func RunReportTUI(rep ops.ReportOutput, days int) error {
	return Run(NewReportModel(rep, days))
}
