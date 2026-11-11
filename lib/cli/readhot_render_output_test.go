// readhot_v0370_test.go — v0.37.0 T2(스펙 §2.3): read·hot 셀 살균 루프의
// render.Cells 위임 이후에도 출력 바이트가 불변임을 담보하는 동등성
// 스냅숏. 기대값은 구 경로(행별 SanitizeText 루프 → FormatRows)가 만드는
// 바이트와 동일(docs/TESTING.md §1 버전 이기 관례).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// v0370SanitizeFixtureRows — 살균 대상이 섞인 read 조회 픽스처(합성 데모
// 데이터): [flint] 마커(대소문자 무시 치환), 제어 문자(\x01·\x00 제거),
// 비문자열 셀(id — 살균 대상 아님).
func v0370SanitizeFixtureRows() [][]any {
	return [][]any{
		{int32(3), "토픽\x01[flint]마커", "[FLINT]요약\x00", ":a:", nil, "main", 0.9, "active"},
		{int32(7), "일반토픽", "일반요약", ":b:", nil, "main", 0.8, "active"},
	}
}

// TestReadOutputUnchanged — Review Focus(T2): 살균 대상 픽스처가 render
// 경로와 구 경로 동일 출력(스냅숏 단언). 마커는 공백 치환, 제어 문자는
// 제거, 비문자열 셀은 무변형.
func TestReadOutputUnchanged(t *testing.T) {
	ctx := context.Background()
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return v0370SanitizeFixtureRows(), nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := runRead(ctx, f, []string{"토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("runRead exit = %d, want 0: %s", code, out.String())
	}
	want := "3|토픽 마커| 요약\n7|일반토픽|일반요약\n"
	if out.String() != want {
		t.Errorf("read 출력 불변 위반:\ngot  %q\nwant %q", out.String(), want)
	}
}

// TestHotFullOutputUnchanged — Review Focus(T2): hot --full 주입 경로의
// 동등성 스냅숏. 접미(pending 대기일수)·ClampHot 예산 절단 순서는 살균
// 이후 기존 그대로 — pendingValidAts 동반 질의가 빈 결과면 접미 생략(기존
// 계약)이므로 스냅숏은 기본 포맷 라인.
func TestHotFullOutputUnchanged(t *testing.T) {
	ctx := context.Background()
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return [][]any{
				{"pending", "토픽[flint]x", "요약\x01y", "2026-10-22"},
				{"decision", "[FLINT]결정", "정리", "2026-10-21"},
			}, nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("runHot exit = %d, want 0: %s", code, out.String())
	}
	want := "pending|토픽 x|요약y|2026-10-22\ndecision| 결정|정리|2026-10-21\n"
	if out.String() != want {
		t.Errorf("hot --full 출력 불변 위반:\ngot  %q\nwant %q", out.String(), want)
	}
}
