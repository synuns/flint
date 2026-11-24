package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func serveTestFactory(context.Context) (wire.Store, error) { return &fakeStore{}, nil }

func newServeTestServer(t *testing.T, days, port int) *ServeServer {
	t.Helper()
	s := NewServeServer(serveTestFactory, days, port)
	if s.err != nil {
		t.Fatalf("NewServeServer: %v", s.err)
	}
	if s.ln == nil {
		t.Fatal("no listener")
	}
	t.Cleanup(s.Close)
	return s
}

func withCollectFake(t *testing.T, out ReportOutput, err error) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		return
	}
	old := serveCollectReportFn
	serveCollectReportFn = func(context.Context, wire.Store, int) (ReportOutput, error) { return out, err }
	t.Cleanup(func() { serveCollectReportFn = old })
}

func fixtureReport() ReportOutput {
	out := ReportOutput{Days: make([]ReportDay, 7)}
	out.ZeroPct = 12
	out.DupPct = 9
	out.Counts.Active = 40
	out.Stale.NeverRead = 3
	out.Effect.ProfileHint = "keep normal — recall-led majority"
	out.Diagnosis = []DiagnosisItem{{
		Signal: "dup_pressure",
		Value:  "9%",
		Basis:  "dup_pct",
		Action: "review duplicates",
	}}
	return out
}

func TestNewServeServerDefaultPort(t *testing.T) {
	s := NewServeServer(serveTestFactory, 30, 0)
	if s.err != nil {
		t.Fatalf("NewServeServer: %v", s.err)
	}
	t.Cleanup(s.Close)
	if s.Port < 7480 || s.Port > 7484 {
		t.Fatalf("Port = %d, want 7480..7484 (retry ruling)", s.Port)
	}
}

func TestServeJSONCacheControlRevalidation(t *testing.T) {
	withCollectFake(t, fixtureReport(), nil)
	s := newServeTestServer(t, 7, 17507)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/diagnose.json", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache — revalidation contract", cc)
	}
	etag := rec.Header().Get("ETag")

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/diagnose.json", nil)
	req2.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 304 {
		t.Fatalf("status = %d, want 304", rec2.Code)
	}
	if cc := rec2.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("304 Cache-Control = %q, want no-cache", cc)
	}
}

func TestServeLocalOnly(t *testing.T) {
	for _, port := range []int{0, 17501} {
		s := newServeTestServer(t, 7, port)
		host, _, err := net.SplitHostPort(s.ln.Addr().String())
		if err != nil {
			t.Fatalf("listener addr %q: %v", s.ln.Addr().String(), err)
		}
		if host != "127.0.0.1" {
			t.Fatalf("listener host = %q, want 127.0.0.1", host)
		}
	}
}

func TestPortRetry5(t *testing.T) {
	var held []net.Listener
	t.Cleanup(func() {
		for _, ln := range held {
			ln.Close()
		}
	})
	for p := 7480; p <= 7484; p++ {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			held = append(held, ln)
		}
	}
	s := NewServeServer(serveTestFactory, 30, 0)
	if s.err == nil {
		s.Close()
		t.Fatal("want error when 7480-7484 all occupied")
	}
	if s.ln != nil {
		t.Fatal("no listener should remain after exhausted retries")
	}
	if err := s.ListenAndServe(); err == nil {
		t.Fatal("ListenAndServe must surface port exhaustion error")
	}
}

func TestETag304(t *testing.T) {
	withCollectFake(t, fixtureReport(), nil)
	s := newServeTestServer(t, 7, 17502)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/diagnose.json", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag header")
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if payload["days"] != float64(7) {
		t.Fatalf("days = %v, want 7", payload["days"])
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/diagnose.json", nil)
	req2.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 304 {
		t.Fatalf("If-None-Match status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Fatalf("304 body = %q, want empty", rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/diagnose.json", nil)
	req3.Header.Set("If-None-Match", `"sha256:stale"`)
	h.ServeHTTP(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("stale If-None-Match status = %d, want 200", rec3.Code)
	}
}

func TestSection404(t *testing.T) {
	withCollectFake(t, fixtureReport(), nil)
	s := newServeTestServer(t, 7, 17503)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sections/unknown.json", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown section status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<") {
		t.Fatalf("404 body must not carry markup: %q", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/sections/knowledge-hygiene.json", nil))
	if rec2.Code != 200 {
		t.Fatalf("known section status = %d, want 200", rec2.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("section body not JSON: %v", err)
	}
	if payload["area"] != "knowledge-hygiene" {
		t.Fatalf("area = %v, want knowledge-hygiene", payload["area"])
	}
}

func TestServeSectionsAllFive(t *testing.T) {
	withCollectFake(t, fixtureReport(), nil)
	s := newServeTestServer(t, 7, 17504)
	h := s.Handler()
	for _, area := range []string{
		"knowledge-hygiene", "recall-quality", "token-economy", "auto-tuning", "engine-ops",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sections/"+area+".json", nil))
		if rec.Code != 200 {
			t.Fatalf("%s status = %d, want 200", area, rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s body not JSON: %v", area, err)
		}
		if payload["area"] != area {
			t.Fatalf("%s area = %v", area, payload["area"])
		}
	}
}

func TestServeDiagnoseSanitizesStrings(t *testing.T) {
	out := fixtureReport()
	out.Effect.ProfileHint = "hint\x07with\x1bcontrol"
	withCollectFake(t, out, nil)
	s := newServeTestServer(t, 7, 17505)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/diagnose.json", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.ContainsAny(body, "\x07\x1b") {
		t.Fatal("response leaks control characters — render chokepoint bypassed")
	}
}

func TestServeStoreErrorJSON(t *testing.T) {
	old := serveCollectReportFn
	serveCollectReportFn = func(context.Context, wire.Store, int) (ReportOutput, error) {
		return ReportOutput{}, fmt.Errorf("engine down")
	}
	t.Cleanup(func() { serveCollectReportFn = old })
	s := newServeTestServer(t, 7, 17506)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/diagnose.json", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "engine down") {
		t.Fatal("error detail leaked to response")
	}
}
