package knowledge

import (
	"context"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const DerivativeTag = "derivative:source"

func IsDerivativeOfSource(ctx context.Context, st wire.Store, topic, summary, sourcePath string) (bool, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return false, nil
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT content_hash FROM vault_files WHERE path = $1`, []any{sourcePath})
	if err != nil {
		return false, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return false, nil
	}
	srcHash, _ := rows[0][0].(string)
	if srcHash == "" {
		return false, nil
	}
	return store.ContentHash(summary) == srcHash, nil
}

func appendNormalizedTag(tags, tag string) string {
	if strings.Contains(tags, ":"+tag+":") {
		return tags
	}
	trimmed := strings.Trim(tags, ":")
	parts := []string{}
	if trimmed != "" {
		parts = strings.Split(trimmed, ":")
	}
	parts = append(parts, tag)
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
	deriv, err := IsDerivativeOfSource(r.ctx, r.st, topic, summary, sp)
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
