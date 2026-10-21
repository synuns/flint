package query

import (
	"strconv"
	"unicode/utf8"
)

// truncatedSuffix — ClampHot 절단 시 붙는 접미사.
const truncatedSuffix = "…[truncated]"

// HotSQL는 hot_view 조회 SQL을 반환한다(활성 프로젝트+pending+최근 결정 7일).
// sessionID는 v0.1 hot_view에 session_id 컬럼이 없어 서명 호환용으로만 받는다.
func HotSQL(sessionID string) string {
	_ = sessionID
	limit := ClampLimit(0) // 토큰 예산: 기본 10
	return "SELECT kind, topic, summary, ts FROM hot_view ORDER BY ts DESC LIMIT " + strconv.Itoa(limit)
}

// ClampHot는 s가 max rune을 넘으면 max로 절단하고 접미사를 붙인다.
func ClampHot(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + truncatedSuffix
}
