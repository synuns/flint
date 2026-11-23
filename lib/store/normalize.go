package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func Normalize(kind string, p map[string]any) map[string]any {
	out := make(map[string]any, len(p)+3)
	for k, v := range p {
		out[k] = v
	}
	if kind == "knowledge" {
		if tags, ok := out["tags"]; ok && tags != nil {
			out["tags"] = normalizeTags(tags)
		}
		if s, ok := out["domain"].(string); !ok || s == "" {
			out["domain"] = "main"
		}
		if s, ok := out["origin"].(string); !ok || s == "" {
			out["origin"] = "agent"
		}
		if v, ok := out["valid_at"]; !ok || v == nil {
			out["valid_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		} else {
			out["valid_at"] = toUTC(v)
		}
	}
	if v, ok := out["ts"]; ok {
		out["ts"] = toUTC(v)
	}
	return out
}

func normalizeTags(v any) string {
	var tags []string
	switch t := v.(type) {
	case []string:
		tags = t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				tags = append(tags, s)
			}
		}
	default:
		return ""
	}
	if len(tags) == 0 {
		return ""
	}
	sort.Strings(tags)
	return ":" + strings.Join(tags, ":") + ":"
}

func toUTC(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return v
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func CanonicalJSON(p map[string]any) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

var hashVolatile = map[string]bool{"valid_at": true, "ts": true, "relations": true, "conditions": true,
	"knowledge_kind": true, "source_path": true}

var knowledgeRelations = map[string]bool{
	"refines":     true,
	"supports":    true,
	"contradicts": true,
	"duplicates":  true,
}

func RelationVocabulary() []string {
	out := make([]string, 0, len(knowledgeRelations))
	for k := range knowledgeRelations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ValidRelation(rel string) bool {
	return knowledgeRelations[normalizeVocab(rel)]
}

func CheckRelations(rels []string) []string {
	var out []string
	for _, rel := range rels {
		n := normalizeVocab(rel)
		if knowledgeRelations[n] {
			continue
		}
		if r := []rune(n); len(r) > 200 {
			n = string(r[:200]) + "…"
		}
		out = append(out, fmt.Sprintf(
			"rejected relation: %s (relations vocabulary: %s)",
			n, strings.Join(RelationVocabulary(), ", ")))
	}
	return out
}

func HashCanonical(p map[string]any) string {
	m := make(map[string]any, len(p))
	for k, v := range p {
		if hashVolatile[k] {
			continue
		}
		m[k] = v
	}
	return CanonicalJSON(m)
}
