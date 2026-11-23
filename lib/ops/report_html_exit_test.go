package ops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func htmlExitCode(t *testing.T, f *fakeStore) int {
	t.Helper()
	t.Chdir(t.TempDir())
	var out strings.Builder
	return runReportHTML(context.Background(), f, 7, "report.html", &out)
}

func TestHTMLExitServerDown(t *testing.T) {
	if code := htmlExitCode(t, &fakeStore{pingErr: store.ErrServerDown}); code != 2 {
		t.Fatalf("report --html 서버 다운 exit=%d, want 2", code)
	}
}

func TestHTMLExitSchemaMismatch(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: &store.SchemaError{Field: "schema_version", Expect: "v9"}}); code != 3 {
		t.Fatalf("report --html 스키마 불일치 exit=%d, want 3", code)
	}
}

func TestHTMLExitOtherCollectionErrorStays1(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: errors.New("synthetic: query boom")}); code != 1 {
		t.Fatalf("report --html 일반 수집 오류 exit=%d, want 1", code)
	}
}

func TestHTMLExitViaRunReportMatchesJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := []struct {
		name string
		f    *fakeStore
		want int
	}{
		{"server_down", &fakeStore{pingErr: store.ErrServerDown}, 2},
		{"schema", &fakeStore{queryErr: &store.SchemaError{Field: "schema_version", Expect: "v9"}}, 3},
		{"other", &fakeStore{queryErr: errors.New("synthetic: boom")}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			if code := RunReport(context.Background(), c.f, []string{"--html"}, nil, &out); code != c.want {
				t.Fatalf("RunReport --html %s exit=%d, want %d (출력 %s)", c.name, code, c.want, out.String())
			}
		})
	}
}
