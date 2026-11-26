package knowledge

import (
	"context"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var claimVocabulary = []string{"decision", "procedure", "lesson", "event"}

var validClaims = func() map[string]bool {
	m := make(map[string]bool, len(claimVocabulary))
	for _, c := range claimVocabulary {
		m[c] = true
	}
	return m
}()

func normalizeClaim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func claimString(v any) string {
	s, _ := v.(string)
	return s
}

func EnforceClaimPending(rec map[string]any) map[string]any {
	if kind, _ := rec["kind"].(string); kind != "knowledge" {
		return rec
	}
	if validClaims[normalizeClaim(claimString(rec["claim"]))] {
		return rec
	}
	rec["status"] = "pending"
	return rec
}

func RejectSessionSummaryCopy(summary string, sessionSummary string) error {
	s := strings.TrimSpace(summary)
	ss := strings.TrimSpace(sessionSummary)
	if s == "" || ss == "" {
		return nil
	}
	if s == ss || strings.HasPrefix(ss, s) {
		return &store.SchemaError{Field: "summary",
			Expect: "not a copy of (or prefix of) the session summary"}
	}
	return nil
}

func sessionSummaryForAgent(ctx context.Context, st wire.Store, agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" || st == nil {
		return ""
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT summary FROM sessions WHERE agent = $1 ORDER BY ts DESC, id DESC LIMIT 1`,
		[]any{agent})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return ""
	}
	return claimString(rows[0][0])
}

func rejectSessionSummaryCopyForWrite(ctx context.Context, st wire.Store, summary, agent string) error {
	return RejectSessionSummaryCopy(summary, sessionSummaryForAgent(ctx, st, agent))
}

func knowledgeRowClaim(ctx context.Context, st wire.Store, id int) (string, bool) {
	if st == nil {
		return "", false
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT claim FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return "", false
	}
	return claimString(rows[0][0]), true
}

func approveExisting(ctx context.Context, st wire.Store, id int, claim string) error {
	if as, ok := st.(approveStore); ok {
		return as.Approve(ctx, id, claim)
	}
	return ApproveFn(ctx, id)
}

func normalizeWriteClaim(rec map[string]any) *store.SchemaError {
	raw, ok := rec["claim"]
	if !ok || raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return &store.SchemaError{Field: "claim",
			Expect: strings.Join(claimVocabulary, "|")}
	}
	c := normalizeClaim(s)
	if c == "" {
		return nil
	}
	if !validClaims[c] {
		return &store.SchemaError{Field: "claim",
			Expect: strings.Join(claimVocabulary, "|")}
	}
	rec["claim"] = c
	return nil
}

func ApproveWithClaim(ctx context.Context, st wire.Store, id int, claim string) (string, error) {
	c := normalizeClaim(claim)
	if c != "" && !validClaims[c] {
		return "", approveUsageErr("--claim %q is invalid — must be one of %s",
			claim, strings.Join(claimVocabulary, "|"))
	}
	rowClaim, found := knowledgeRowClaim(ctx, st, id)
	if rc := normalizeClaim(rowClaim); found && c == "" && (rc == "" || rc == "event") {
		return "", approveUsageErr(
			"claim gate: knowledge row claim is unset (backfill=event) — re-run with --claim %s",
			strings.Join(claimVocabulary, "|"))
	}
	if err := approveExisting(ctx, st, id, c); err != nil {
		return "", err
	}
	return "promoted", nil
}
