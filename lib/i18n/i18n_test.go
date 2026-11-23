package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func TestLangDefaultsToEn(t *testing.T) {
	t.Setenv("FLINT_LANG", "")
	if got := Lang(); got != "en" {
		t.Fatalf("Lang()=%q, want en", got)
	}
}

func TestLangKo(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	if got := Lang(); got != "ko" {
		t.Fatalf("Lang()=%q, want ko", got)
	}
}

func TestLangUnsupportedFallsBackToEn(t *testing.T) {
	t.Setenv("FLINT_LANG", "fr")
	if got := Lang(); got != "en" {
		t.Fatalf("Lang()=%q, want en", got)
	}
}

func TestCatalogHasEnKo(t *testing.T) {
	for _, lang := range []string{"en", "ko"} {
		c, ok := Catalog[lang]
		if !ok {
			t.Fatalf("Catalog[%q] 부재", lang)
		}
		if len(c) == 0 {
			t.Fatalf("Catalog[%q] 비어 있음", lang)
		}
	}
}

func TestCatalogKeyParity(t *testing.T) {
	for k := range Catalog["en"] {
		if _, ok := Catalog["ko"][k]; !ok {
			t.Errorf("ko 카탈로그에 키 부재: %q", k)
		}
	}
	for k := range Catalog["ko"] {
		if _, ok := Catalog["en"][k]; !ok {
			t.Errorf("en 카탈로그에 키 부재: %q", k)
		}
	}
}

func TestTEnUsage(t *testing.T) {
	t.Setenv("FLINT_LANG", "")
	want := "usage: flint brief [query] [--limit N]"
	if got := T("usage.brief"); got != want {
		t.Fatalf("T(usage.brief)=%q, want %q", got, want)
	}
}

func TestTKoUsage(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	want := "사용법: flint brief [쿼리] [--limit N]"
	if got := T("usage.brief"); got != want {
		t.Fatalf("T(usage.brief)=%q, want %q", got, want)
	}
}

func TestTEnUsageLine(t *testing.T) {
	t.Setenv("FLINT_LANG", "")
	got := fmt.Sprintf(T("usage.line"), strings.Join([]string{"read", "write"}, "|"))
	want := `{"usage":"flint <read|write> [args]"}`
	if got != want {
		t.Fatalf("usage.line=%q, want %q", got, want)
	}
}

func TestTKoUsageLine(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	got := fmt.Sprintf(T("usage.line"), strings.Join([]string{"read", "write"}, "|"))
	want := `{"usage":"flint <read|write> [인자]"}`
	if got != want {
		t.Fatalf("usage.line=%q, want %q", got, want)
	}
}

func TestTEnUsageLinePlain(t *testing.T) {
	t.Setenv("FLINT_LANG", "")
	got := fmt.Sprintf(T("usage.line.plain"), strings.Join([]string{"read", "write"}, "|"))
	want := "flint <read|write> [args]"
	if got != want {
		t.Fatalf("usage.line.plain=%q, want %q", got, want)
	}
}

func TestTEnUnknownCommand(t *testing.T) {
	t.Setenv("FLINT_LANG", "")
	got := fmt.Sprintf(T("err.unknown_command"), "no-such-cmd")
	want := "unknown command: no-such-cmd"
	if got != want {
		t.Fatalf("err.unknown_command=%q, want %q", got, want)
	}
}

func TestTKoUnknownCommand(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	got := fmt.Sprintf(T("err.unknown_command"), "no-such-cmd")
	want := "알 수 없는 명령: no-such-cmd"
	if got != want {
		t.Fatalf("err.unknown_command=%q, want %q", got, want)
	}
}

func TestTKoMissFallsBackToEn(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	old, had := Catalog["ko"]["usage.brief"]
	if !had {
		t.Fatal("Catalog[ko]에 usage.brief 부재")
	}
	delete(Catalog["ko"], "usage.brief")
	defer func() { Catalog["ko"]["usage.brief"] = old }()
	want := "usage: flint brief [query] [--limit N]"
	if got := T("usage.brief"); got != want {
		t.Fatalf("ko 미스 en 폴백=%q, want %q", got, want)
	}
}

func TestTMissingKeyReturnsKey(t *testing.T) {
	t.Setenv("FLINT_LANG", "ko")
	if got := T("no.such.key"); got != "no.such.key" {
		t.Fatalf("T(부재 키)=%q, want 키 자체", got)
	}
}
