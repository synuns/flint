package query

import (
	"strconv"
	"unicode/utf8"
)

const truncatedSuffix = "…[truncated]"

func HotSQL(sessionID string) string {
	_ = sessionID
	limit := ClampLimit(0)
	return "SELECT kind, topic, summary, ts, id, valid_at, ingested_at FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(limit)
}

const hotHeadlineLimit = 50

func HotHeadlineSQL(sessionID string) string {
	_ = sessionID

	return "SELECT kind, topic, summary, ts, id FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(hotHeadlineLimit)
}

func ClampHot(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + truncatedSuffix
}
