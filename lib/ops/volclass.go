// volclass.go — the v1.22.0 T4 deterministic volatility-class suggestion:
// a read-only mapping (code constants, CONTENT §4) that proposes one of the
// 3 dictionary vol:* tiers for an --age-audit row from its topic and tags
// alone. Proposal-only by ruling: granting the :vol:immutable: decay
// exemption automatically would collide with the approval regime, so the
// suggestion is report material — adoption goes through the existing write
// approval path (a vol:* tag is dictionary-registered, hence the colon guard
// is orthogonal). origin is NOT an input (independent of the provenance
// trust layer).
package ops

import "strings"

// The 3 dictionary volatility tiers (store.CheckVocabulary's registered
// colon tokens — the single vocabulary; T4 adds no entries).
const (
	VolClassImmutable   = "vol:immutable"
	VolClassVolatile    = "vol:volatile"
	VolClassConditional = "vol:conditional"
)

// volImmutableKeywords — the finalized-decision class: an ADR, a settled
// decision, or project history (immutable-grade knowledge — the decay
// exemption candidates).
var volImmutableKeywords = []string{
	"adr", "decision", "decided", "history", "immutable",
	"결정", "역사",
}

// volVolatileKeywords — the tool-state class: versions, paths, commands,
// and tool state (facts that rot with the environment).
var volVolatileKeywords = []string{
	"version", "path", "command", "tool",
	"버전", "경로", "명령", "도구",
}

// volKeywordMatch — case-insensitive substring match of any keyword against
// the topic and tags cell (a suggestion-grade heuristic — exact tokenization
// is not claimed; the fixed priority below resolves overlaps).
func volKeywordMatch(topic, tags string, keywords []string) bool {
	hay := strings.ToLower(topic + " " + tags)
	for _, k := range keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

// SuggestVolClass — the deterministic mapping: immutable keywords →
// vol:immutable, else volatile keywords → vol:volatile, else
// vol:conditional (the unmatched default). Multi-match resolves by the fixed
// priority immutable > volatile > conditional (an immutable-graded row is
// never downgraded by a coincidental volatile keyword). Rows already
// carrying an approved vol:* tag are handled at the report level
// (ExistingVolTag — no re-proposal over an approved tier).
func SuggestVolClass(topic, tags string) string {
	switch {
	case volKeywordMatch(topic, tags, volImmutableKeywords):
		return VolClassImmutable
	case volKeywordMatch(topic, tags, volVolatileKeywords):
		return VolClassVolatile
	default:
		return VolClassConditional
	}
}

// ExistingVolTag — the vol:* dictionary tag present in a tags cell, "" when
// none (":adr::vol:conditional:" → "vol:conditional"). The dict order is the
// recheckVolDict order; a cell never carries two tiers in practice (one
// volatility grade per row), the first wins if it ever did.
func ExistingVolTag(tags string) string {
	for _, v := range recheckVolDict {
		if strings.Contains(tags, ":"+v+":") {
			return v
		}
	}
	return ""
}
