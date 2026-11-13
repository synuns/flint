// report_html_exit_test.go — v1.2.3 R17 C2: report --html exit 코드 정합.
// 병인: runReportHTML 수집 경로가 Ping 실패·전 수집 스텝 오류를
// 하드코딩 1로 반환 — JSON 경로(RunReport)는 store.MapExit(
// server_down→2·SchemaError→3)로 같은 커맨드의 두 뷰가 같은 실패에
// 다른 코드를 냈다. HTML 경로도 MapExit을 재사용해 계약을 일치시킨다.
// 주의: RunReport 진입부의 Ping 선검사가 이미 MapExit이라 상위 경로는
// 우연히 통과 — 결함은 runReportHTML 직접 경로(수집 스텝·내부 Ping)에
// 있으므로 단위 테스트는 runReportHTML을 직접 호출한다.
// 리뷰 포인트: exit 3은 인자 검증 실패(RunReport 플래그 파서)와 별개.
// 여기의 3은 '스토어가 SchemaError 계열을 반환'할 때만 — MapExit 소스라
// JSON 경로와 의미가 같다.
package ops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// htmlExitFake — exit 매핑 검증용 최소 fake(fakeStore 관례 재사용):
// pingErr·queryErr stub만 지정.
func htmlExitCode(t *testing.T, f *fakeStore) int {
	t.Helper()
	t.Chdir(t.TempDir()) // report.html 기본 출력 격리
	var out strings.Builder
	return runReportHTML(context.Background(), f, 7, "report.html", &out)
}

// TestHTMLExitServerDown — Ping 실패(서버 다운)는 JSON 경로와 동일하게
// exit 2. 수정 전에는 하드코딩 1이었다(실패 실증).
func TestHTMLExitServerDown(t *testing.T) {
	if code := htmlExitCode(t, &fakeStore{pingErr: store.ErrServerDown}); code != 2 {
		t.Fatalf("report --html 서버 다운 exit=%d, want 2", code)
	}
}

// TestHTMLExitSchemaMismatch — 수집 중 SchemaError 계열(스키마 버전
// 불일치 포함)은 JSON 경로와 동일하게 exit 3.
func TestHTMLExitSchemaMismatch(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: &store.SchemaError{Field: "schema_version", Expect: "v9"}}); code != 3 {
		t.Fatalf("report --html 스키마 불일치 exit=%d, want 3", code)
	}
}

// TestHTMLExitOtherCollectionErrorStays1 — 그 외 수집 오류는 기존 1 유지
// (계약: 변경되는 것은 server_down·SchemaError 매핑뿐).
func TestHTMLExitOtherCollectionErrorStays1(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: errors.New("synthetic: query boom")}); code != 1 {
		t.Fatalf("report --html 일반 수집 오류 exit=%d, want 1", code)
	}
}

// TestHTMLExitViaRunReportMatchesJSON — 엔드투엔드 대조: 같은 실패가
// RunReport --html 진입에서도 동일 코드(진입부 Ping MapExit + 전달
// 경로 회귀 가드). JSON 뷰와 값이 같은지가 이 태스크의 계약.
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
