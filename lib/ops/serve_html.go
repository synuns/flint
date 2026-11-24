package ops

import (
	"context"
	_ "embed"
	"html/template"
	"net/http"
	"strconv"

	"github.com/synuns/flint/lib/wire"
)

//go:embed serve_page.tmpl.html
var servePageTmpl string

//go:embed hand.css
var handCSS []byte

var collectReportFn = CollectReport

var servePage = template.Must(template.New("serve_page").Parse(servePageTmpl))

type serveRow struct {
	Label string
	Value string
}

type serveArea struct {
	ID    string
	Title string
	Rows  []serveRow
}

type servePageData struct {
	GeneratedAt string
	Days        int
	Poll        int
	Areas       []serveArea
}

func serveAreas(rep ReportOutput) []serveArea {
	return []serveArea{
		{ID: "knowledge-hygiene", Title: "Knowledge hygiene", Rows: []serveRow{
			{"pending", strconv.Itoa(rep.Counts.Pending)},
			{"active", strconv.Itoa(rep.Counts.Active)},
			{"invalidated", strconv.Itoa(rep.Counts.Invalidated)},
			{"stale 90d", strconv.Itoa(rep.Stale.Days90)},
			{"never read", strconv.Itoa(rep.Stale.NeverRead)},
			{"dup pct", strconv.Itoa(rep.DupPct) + "%"},
		}},
		{ID: "recall-quality", Title: "Recall quality", Rows: []serveRow{
			{"zero-hit pct", strconv.Itoa(rep.ZeroPct) + "%"},
			{"zero-near pct", strconv.Itoa(rep.ZeroNearPct) + "%"},
			{"zero-hit unowned pct", strconv.FormatFloat(rep.Effect.ZeroHitUnownedPct, 'f', 1, 64) + "%"},
			{"hit pct", strconv.Itoa(rep.Effect.HitPct) + "%"},
			{"read events", strconv.Itoa(rep.Effect.ReadEventsN)},
			{"profile hint", rep.Effect.ProfileHint},
		}},
		{ID: "token-economy", Title: "Token economy", Rows: []serveRow{
			{"reuse pct", strconv.Itoa(rep.Effect.ReusePct) + "%"},
			{"read-write pair pct", strconv.Itoa(rep.Effect.PairPct) + "%"},
			{"dup prevented", strconv.Itoa(rep.Effect.DupPrevented)},
		}},
		{ID: "auto-tuning", Title: "Auto-tuning", Rows: []serveRow{
			{"approve done", strconv.Itoa(rep.Curation.ApproveDone)},
			{"maintain supersede", strconv.Itoa(rep.Curation.MaintainSupersede)},
			{"import added", strconv.Itoa(rep.Curation.ImportAdded)},
			{"import skipped", strconv.Itoa(rep.Curation.ImportSkipped)},
		}},
		{ID: "engine-ops", Title: "Engine ops", Rows: []serveRow{
			{"fallback pct", strconv.Itoa(rep.FallbackPct) + "%"},
			{"diagnoses", strconv.Itoa(len(rep.Diagnosis))},
		}},
	}
}

func servePollSeconds(r *http.Request) int {
	raw := r.URL.Query().Get("poll")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0
	}
	if n > 3600 {
		return 3600
	}
	return n
}

func HTMLHandler(newStore func(context.Context) (wire.Store, error), days int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hand.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write(handCSS)
			return
		case "/":
		default:
			http.NotFound(w, r)
			return
		}
		st, err := newStore(r.Context())
		if err != nil {
			http.Error(w, wire.ErrLine(err), http.StatusBadGateway)
			return
		}
		defer st.Close()
		rep, err := collectReportFn(r.Context(), st, days)
		if err != nil {
			http.Error(w, wire.ErrLine(err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := servePageData{
			GeneratedAt: timeNowUTC(),
			Days:        days,
			Poll:        servePollSeconds(r),
			Areas:       serveAreas(rep),
		}
		if err := servePage.Execute(w, data); err != nil {
			return
		}
	})
}
