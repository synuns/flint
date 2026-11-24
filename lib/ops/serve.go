package ops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const (
	servePortDefault  = 7480
	servePortAttempts = 5
)

var serveCollectReportFn = CollectReport

type ServeServer struct {
	newStore func(context.Context) (wire.Store, error)
	days     int
	ln       net.Listener
	Port     int
	err      error
}

func NewServeServer(newStore func(context.Context) (wire.Store, error), days, port int) *ServeServer {
	s := &ServeServer{newStore: newStore, days: days}
	base := port
	if base <= 0 {
		base = servePortDefault
	}
	var lastErr error
	for i := 0; i < servePortAttempts; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+i))
		if err == nil {
			s.ln = ln
			s.Port = base + i
			return s
		}
		lastErr = err
	}
	s.err = fmt.Errorf("serve: no free port in %d..%d: %w", base, base+servePortAttempts-1, lastErr)
	return s
}

func (s *ServeServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/diagnose.json", s.handleDiagnose)
	mux.HandleFunc("GET /api/sections/", s.handleSection)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, r, http.StatusNotFound, map[string]any{"error": "unknown endpoint"})
	})
	mux.Handle("/", HTMLHandler(s.newStore, s.days))
	return mux
}

func (s *ServeServer) ListenAndServe() error {
	if s.err != nil {
		return s.err
	}
	if s.ln == nil {
		return fmt.Errorf("serve: no listener")
	}
	return http.Serve(s.ln, s.Handler())
}

func (s *ServeServer) Close() {
	if s.ln != nil {
		s.ln.Close()
	}
}

func (s *ServeServer) collect(ctx context.Context) (ReportOutput, error) {
	st, err := s.newStore(ctx)
	if err != nil {
		return ReportOutput{}, fmt.Errorf("serve: store: %w", err)
	}
	defer st.Close()
	return serveCollectReportFn(ctx, st, s.days)
}

func (s *ServeServer) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	out, err := s.collect(r.Context())
	if err != nil {
		writeServeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"error": "diagnose unavailable"})
		return
	}
	writeServeJSON(w, r, http.StatusOK, diagnosePayload(out, s.days))
}

func (s *ServeServer) handleSection(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sections/"), ".json")
	if strings.ContainsAny(name, "/") || name == "" {
		writeServeJSON(w, r, http.StatusNotFound, map[string]any{"error": "unknown section"})
		return
	}
	section, ok := serveSections[name]
	if !ok {
		writeServeJSON(w, r, http.StatusNotFound, map[string]any{"error": "unknown section"})
		return
	}
	out, err := s.collect(r.Context())
	if err != nil {
		writeServeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"error": "diagnose unavailable"})
		return
	}
	payload := section(out)
	payload["area"] = name
	payload["days"] = s.days
	writeServeJSON(w, r, http.StatusOK, payload)
}

var serveSections = map[string]func(ReportOutput) map[string]any{
	"knowledge-hygiene": func(o ReportOutput) map[string]any {
		return map[string]any{
			"counts":             o.Counts,
			"stale":              o.Stale,
			"dup_pct":            o.DupPct,
			"invalidated_period": o.InvalidatedPeriod,
		}
	},
	"recall-quality": func(o ReportOutput) map[string]any {
		return map[string]any{
			"zero_pct":      o.ZeroPct,
			"zero_near_pct": o.ZeroNearPct,
			"effect":        renderEffect(o.Effect),
		}
	},
	"token-economy": func(o ReportOutput) map[string]any {
		e := renderEffect(o.Effect)
		return map[string]any{
			"bytes": e.Bytes,
			"cost":  e.Cost,
		}
	},
	"auto-tuning": func(o ReportOutput) map[string]any {
		return map[string]any{
			"curation":     o.Curation,
			"fallback_pct": o.FallbackPct,
		}
	},
	"engine-ops": func(o ReportOutput) map[string]any {
		daily := make([]ReportDay, 0, len(o.Days))
		for _, d := range o.Days {
			d.Date = render.Text(d.Date)
			daily = append(daily, d)
		}
		return map[string]any{
			"daily":   daily,
			"hot_p95": o.HotP95,
		}
	},
}

func renderEffect(e EffectOutput) EffectOutput {
	e.ProfileHint = render.Masked(e.ProfileHint)
	e.Cost.SavingsLabel = render.Masked(e.Cost.SavingsLabel)
	e.Cost.HitCostLabel = render.Masked(e.Cost.HitCostLabel)
	e.Cost.Read.Mode = render.Text(e.Cost.Read.Mode)
	e.Cost.Hot.Mode = render.Text(e.Cost.Hot.Mode)
	e.Cost.Brief.Mode = render.Text(e.Cost.Brief.Mode)
	return e
}

func diagnosePayload(out ReportOutput, days int) map[string]any {
	diags := make([]DiagnosisItem, 0, len(out.Diagnosis))
	for _, d := range out.Diagnosis {
		d.Signal = render.Masked(d.Signal)
		d.Value = render.Masked(d.Value)
		d.Basis = render.Masked(d.Basis)
		d.Action = render.Masked(d.Action)
		d.TuningKey = render.Text(d.TuningKey)
		diags = append(diags, d)
	}
	return map[string]any{
		"days":               days,
		"zero_pct":           out.ZeroPct,
		"dup_pct":            out.DupPct,
		"fallback_pct":       out.FallbackPct,
		"counts":             out.Counts,
		"stale":              out.Stale,
		"effect":             renderEffect(out.Effect),
		"curation":           out.Curation,
		"diagnosis":          diags,
		"hot_p95":            out.HotP95,
		"invalidated_period": out.InvalidatedPeriod,
	}
}

func writeServeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(body)))
	if ifNoneMatchHit(r.Header.Get("If-None-Match"), w.Header().Get("ETag")) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func ifNoneMatchHit(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == etag || part == "*" {
			return true
		}
	}
	return false
}
