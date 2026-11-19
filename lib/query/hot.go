package query

import (
	"strconv"
	"unicode/utf8"
)

// truncatedSuffix — the suffix attached when ClampHot truncates.
const truncatedSuffix = "…[truncated]"

// HotSQL returns the hot_view query SQL (active projects + pending + recent
// decisions from the last 7 days). sessionID is accepted only for signature
// compatibility (hot_view has no session_id column).
// A single definition: the trailing id, valid_at, and ingested_at are selected too —
// the increment ids and the pending-waiting-days suffix are consumed from this one
// query, without a companion query. The tie-break ", id DESC" (CI 39713526850
// ruling) is kept: with planner-arbitrary order among tied ts, the injection order wobbles — hot_view's trailing id creates a total order so the output and increment see the same row set and order.
func HotSQL(sessionID string) string {
	_ = sessionID
	limit := ClampLimit(0) // token budget: default 10
	return "SELECT kind, topic, summary, ts, id, valid_at, ingested_at FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(limit)
}

// hotHeadlineLimit — the row cap for the headline (counts) path. Unlike the
// injection budget (ClampLimit default 10), counting is the purpose, so it is
// generous (the defect where, at a cap of 10, active 20 was reported as 'active
// 10' and pending pushed out of the top 10 rows was reported as 'pending 0').
const hotHeadlineLimit = 50

// HotHeadlineSQL — the hot_view query for the headline path (cap
// hotHeadlineLimit). The injection (--full) path uses HotSQL (the budget cap)
// as-is — the output-budget and survival-judgment contracts unchanged.
func HotHeadlineSQL(sessionID string) string {
	_ = sessionID
	// id exposure — the headline's full set of injected ids (the recording source of
	// knowledge_reads kind=hot). The same contract as --full's id exposure.
	return "SELECT kind, topic, summary, ts, id FROM hot_view ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(hotHeadlineLimit)
}

// ClampHot truncates s to max runes when exceeded and attaches the suffix.
func ClampHot(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + truncatedSuffix
}
