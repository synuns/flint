package ops

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDeprecationNoticeServed(t *testing.T) {
	n := DeprecationNotice("--html")
	for _, want := range []string{
		"--html is deprecated",
		"`flint report --serve`",
		"removed in a later release",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("폐기 안내에 %q 부재: %s", want, n)
		}
	}
	if !strings.HasPrefix(DeprecationNotice("--out"), "--out is deprecated") {
		t.Errorf("플래그명 반영 안 됨: %s", DeprecationNotice("--out"))
	}
}

func TestStaticPathStillWorks(t *testing.T) {
	t.Chdir(t.TempDir())
	var out strings.Builder
	if code := runReportHTML(context.Background(), &fakeStore{}, 7, "report.html", &out); code != 0 {
		t.Fatalf("deprecated 경유 exit=%d, want 0 (출력 %s)", code, out.String())
	}
	data, err := os.ReadFile("report.html")
	if err != nil {
		t.Fatalf("정적 산출 누락: %v", err)
	}
	if !strings.Contains(string(data), "<!doctype html") && !strings.Contains(string(data), "<!DOCTYPE html") {
		t.Errorf("정적 HTML 골격 아님: %.80s", data)
	}
	if !strings.Contains(out.String(), DeprecationNotice("--html")) {
		t.Errorf("마이그레이션 안내 미출력: %s", out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Errorf("stdout 요약 불변 아님: %s", out.String())
	}
}
