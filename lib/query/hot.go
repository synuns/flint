package query

import (
	"strconv"
	"unicode/utf8"
)

// truncatedSuffix — ClampHot 절단 시 붙는 접미사.
const truncatedSuffix = "…[truncated]"

// HotSQL는 hot_view 조회 SQL을 반환한다(활성 프로젝트+pending+최근 결정 7일).
// sessionID는 v0.1 hot_view에 session_id 컬럼이 없어 서명 호환용으로만 받는다.
// tie-break ", id DESC"(CI 39713526850 룰링): 동률 ts에서 플래너 임의 순서로는
// 주입 집합과 증분 집합(hot.go hotKnowledgeIDsSQL)이 어긋날 수 있다 — hot_view
// 말미의 id로 완전 순서를 만들어 양쪽이 같은 집합을 고르게 한다.
func HotSQL(sessionID string) string {
	_ = sessionID
	limit := ClampLimit(0) // 토큰 예산: 기본 10
	return "SELECT kind, topic, summary, ts FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(limit)
}

// hotHeadlineLimit — 헤드라인(카운트) 경로 행 상한. 주입 예산(ClampLimit
// 기본 10)과 달리 카운트가 목적이라 넉넉히(v0.20.1 패치 루프 1차 F3 —
// 상한 10에서 카운트를 내며 활성 20이 '활성 10'으로, 상위 10행에 밀린
// pending이 '대기 0'으로 왜곡되던 결함).
const hotHeadlineLimit = 50

// HotHeadlineSQL — hot_view 조회 중 헤드라인 경로용(상한 hotHeadlineLimit).
// 주입(--full) 경로는 HotSQL(예산 상한)을 그대로 쓴다 — 출력 예산·생존
// 판정 계약 불변.
func HotHeadlineSQL(sessionID string) string {
	_ = sessionID
	return "SELECT kind, topic, summary, ts FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(hotHeadlineLimit)
}

// ClampHot는 s가 max rune을 넘으면 max로 절단하고 접미사를 붙인다.
func ClampHot(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + truncatedSuffix
}
