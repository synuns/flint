// text.go — 텍스트 출력 보조 유틸 단일 소스(v0.38.0 패키지 분할 1/3:
// lib/cli textutil.go의 truncateRunes와 pending.go의 countPending을
// 통합 이동, 스펙 §2). rune 절단 3중 복제 수렴(v0.34.3 감사 스윕)과
// pending 수 단일 조회 관례를 승계한다. v0.39.0 T4 룰링: CountPending은
// pending 도메인 소유라 lib/knowledge/pending.go로 이동했다(render에는
// 임시 소속이었다 — render→knowledge 임포트는 사이클).
package render

import (
	"strconv"
)

// TruncateRunes — rune 상한 절단(멀티바이트 안전). 상한 이하·빈 문자열은
// 불변 반환. 소비자 관례: import 본문 500룬·maintain 요약 접두 40룬·
// pending 한 줄화 상한(호출자 지정).
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// CellToInt — 셀(any)→int 단일 강제 변환 유틸(v0.39.0 T3 부채 수렴 —
// 구 lib/cli/read.go 복제본 삭제, 이 구현만 남는다). pgx 숫자형(int·
// int32·int64)·float64·10진 문자열을 수용하고 nil·비수는 (0, false).
// 소비자: knowledge read(rowIDs)·cli 잔류(maintain stale·report 집계·
// doctor) — 별칭 경유.
func CellToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}
