// dispatch_v0132_test.go — 심층 리뷰 N9(mcp)·N11 회귀. N9: dispatchRead은
// 외부 입력 query를 플래그 파싱으로부터 보호하도록 "--" 뒤에 조립한다.
// N11: 실구현 seam(installSimple)은 FLINT_TEST_DSN 설정 시만 실DB를 허용
// (운영 기본 DSN 하드코딩·접속 금지). 신규 파일(기존 테스트 수정 금지).
package mcp

import (
	"testing"
)

// TestDispatchReadFlagLikeQueryGuard — N9: query "--json"은 CLI 인자에서
// "--" 뒤에 놓여 검색어로 전달된다(플래그 오인 방지 — 출력 모드 불변은
// lib/cli 파서 회귀 TestReadDashDashTreatsRestAsQuery가 담당).
func TestDispatchReadFlagLikeQueryGuard(t *testing.T) {
	calls, _ := installRun(t, "", 0)
	DispatchTool("flint_read", map[string]any{"query": "--json"})
	wantArgs(t, calls, []string{"read", "--source", "mcp", "--", "--json"})
}
