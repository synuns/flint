package knowledge

import (
	"context"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const DerivativeTag = "derivative:source"

func IsDerivativeOfSource(ctx context.Context, st wire.Store, topic, summary, sourcePath string, summaryRunes int) (bool, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return false, nil
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT summary FROM vault_files WHERE path = $1`, []any{sourcePath})
	if err != nil {
		return false, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return false, nil
	}
	vaultSummary, _ := rows[0][0].(string)
	if strings.TrimSpace(vaultSummary) == "" || strings.TrimSpace(summary) == "" {
		return false, nil
	}
	if summaryRunes <= 0 {
		summaryRunes = store.DefaultThresholds().VaultSummaryMaxRunes
	}
	return store.ContentHash(render.TruncateRunes(summary, summaryRunes)) == store.ContentHash(vaultSummary), nil
}

func appendNormalizedTag(tags, tag string) string {
	present := map[string]bool{}
	for _, p := range strings.Split(strings.Trim(tags, ":"), ":") {
		if p != "" {
			present[p] = true
		}
	}
	need := false
	want := strings.Split(strings.Trim(tag, ":"), ":")
	for _, p := range want {
		if !present[p] {
			need = true
			break
		}
	}
	if !need {
		return tags
	}
	parts := make([]string, 0, len(present)+len(want))
	for p := range present {
		parts = append(parts, p)
	}
	for _, p := range want {
		if !present[p] {
			parts = append(parts, p)
			present[p] = true
		}
	}
	sort.Strings(parts)
	return ":" + strings.Join(parts, ":") + ":"
}

func (r *writeState) markDerivative(n map[string]any) {
	sp, _ := n["source_path"].(string)
	if strings.TrimSpace(sp) == "" {
		return
	}
	summary, _ := n["summary"].(string)
	topic, _ := n["topic"].(string)
	deriv, err := IsDerivativeOfSource(r.ctx, r.st, topic, summary, sp, r.th.VaultSummaryMaxRunes)
	if err != nil {
		r.warnings = append(r.warnings, "derivative check skipped: "+err.Error())
		return
	}
	if !deriv {
		return
	}
	if tags, ok := n["tags"].(string); ok && tags != "" {
		n["tags"] = appendNormalizedTag(tags, DerivativeTag)
		return
	}
	n["tags"] = ":" + DerivativeTag + ":"
}
