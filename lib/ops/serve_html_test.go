package ops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func fakeServeOutput() ReportOutput {
	var out ReportOutput
	out.Days = make([]ReportDay, 7)
	for i := range out.Days {
		out.Days[i].Date = fmt.Sprintf("2026-11-%02d", 18+i)
		out.Days[i].Read = i + 1
	}
	out.DupPct = 9
	out.ZeroPct = 12
	out.Counts.Pending = 3
	out.Counts.Active = 40
	out.Counts.Invalidated = 5
	out.Stale.Days90 = 7
	out.Stale.NeverRead = 11
	out.Effect.ReadEventsN = 142
	out.Effect.HitPct = 61
	out.Effect.ZeroHitUnownedPct = 7.5
	out.Effect.ReusePct = 33
	out.Effect.PairPct = 44
	out.Effect.ProfileHint = "<script>alert(1)</script>"
	out.Curation.ApproveDone = 4
	out.Curation.ImportAdded = 6
	out.Diagnosis = []DiagnosisItem{{Signal: "synthetic-signal", Value: "demo", Basis: "demo", Action: "demo"}}
	return out
}

func serveHTMLFixture(t *testing.T) http.Handler {
	t.Helper()
	real := os.Getenv("SUPERLITE_REAL") == "1"
	saved := collectReportFn
	t.Cleanup(func() { collectReportFn = saved })
	if !real {
		collectReportFn = func(ctx context.Context, st wire.Store, days int) (ReportOutput, error) {
			return fakeServeOutput(), nil
		}
	}
	return HTMLHandler(func(ctx context.Context) (wire.Store, error) {
		return &fakeStore{}, nil
	}, 30)
}

func serveHTMLGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestFiveSections(t *testing.T) {
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	order := []string{"knowledge-hygiene", "recall-quality", "token-economy", "auto-tuning", "engine-ops"}
	last := -1
	for _, id := range order {
		attr := `data-area="` + id + `"`
		idx := strings.Index(body, attr)
		if idx < 0 {
			t.Fatalf("missing %s in rendered page", attr)
		}
		if idx <= last {
			t.Fatalf("%s out of order (idx %d after %d)", id, idx, last)
		}
		last = idx
	}
}

func TestCSSVarHook(t *testing.T) {
	css := string(handCSS)
	rootLike := regexp.MustCompile(`(?m)^\s*--[a-z0-9-]+\s*:`)
	for _, line := range strings.Split(css, "\n") {
		if m := rootLike.FindStringSubmatch(line); m != nil && !strings.HasPrefix(strings.TrimSpace(line), "--flint-") {
			t.Fatalf("non-flint custom property: %q", strings.TrimSpace(line))
		}
	}
	colorLit := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(`)
	for _, line := range strings.Split(css, "\n") {
		if colorLit.MatchString(line) && !strings.Contains(line, "--flint-") {
			t.Fatalf("hardcoded color outside --flint-* token: %q", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(css, "--flint-") {
		t.Fatal("hand.css defines no --flint-* tokens")
	}
}

func TestHTMLEscape(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("fake-only injection; structural escape covered by html/template")
	}
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("raw script payload leaked through template")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("payload not escaped as expected")
	}
}

func TestServeHTMLValues(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("synthetic values only")
	}
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/")
	body := rec.Body.String()
	for _, want := range []string{"142", "9"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing value %s", want)
		}
	}
}

func serveSection(t *testing.T, body, id string) string {
	t.Helper()
	attr := `data-area="` + id + `"`
	i := strings.Index(body, attr)
	if i < 0 {
		t.Fatalf("missing %s in rendered page", attr)
	}
	rest := body[i+len(attr):]
	if j := strings.Index(rest, `data-area="`); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestServeHTMLZeroHitUnownedInHygieneArea(t *testing.T) {
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	hygiene := serveSection(t, body, "knowledge-hygiene")
	if !strings.Contains(hygiene, "zero-hit unowned pct") {
		t.Fatal("zero-hit unowned pct 행은 지식 위생 영역에 렌더되어야 함 (spec M3)")
	}
	if !strings.Contains(hygiene, "7.5%") {
		t.Fatal("지식 위생 영역에 zero-hit unowned 값 7.5% 누락")
	}
	if strings.Contains(serveSection(t, body, "recall-quality"), "zero-hit unowned pct") {
		t.Fatal("zero-hit unowned pct 행이 recall-quality에 남아 있음")
	}
}

func TestServeHTMLProtocol(t *testing.T) {
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/hand.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("hand.css status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("hand.css content-type = %q", ct)
	}
	if rec := serveHTMLGet(t, h, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", rec.Code)
	}
}

func TestServeHTMLCollectError(t *testing.T) {
	saved := collectReportFn
	t.Cleanup(func() { collectReportFn = saved })
	collectReportFn = func(ctx context.Context, st wire.Store, days int) (ReportOutput, error) {
		return ReportOutput{}, errors.New("engine down")
	}
	h := HTMLHandler(func(ctx context.Context) (wire.Store, error) {
		return &fakeStore{}, nil
	}, 30)
	rec := serveHTMLGet(t, h, "/")
	if rec.Code == http.StatusOK {
		t.Fatal("collect error must not render 200 page")
	}
}

func TestServeHTMLRefreshTargetsMainOnly(t *testing.T) {
	h := serveHTMLFixture(t)
	rec := serveHTMLGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "documentElement.innerHTML") {
		t.Fatal("refresh가 문서 전체를 교체 — 첫 갱신 후 #refresh 리스너 유실(inline script는 innerHTML로 재실행되지 않음)")
	}
	if !strings.Contains(body, "querySelector('main')") {
		t.Fatal("refresh는 main 영역만 교체해야 리스너·폴링 간섭 없음")
	}
}
