package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Normalize returns a new map with the kind input values normalized to the
// storage format. tags []string -> a single ":a:b:" string after sorting;
// timestamps to UTC; fills knowledge's domain and origin defaults and now when valid_at is unspecified.
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

// normalizeTags — sorted, then in ":a:b:" form. Empty tags yield the empty string.
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

// toUTC — normalizes an RFC3339 string to UTC. Unparsable values pass through as-is.
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

// CanonicalJSON serializes with sorted keys, guaranteeing hash stability.
// e.g. CanonicalJSON({"b":1,"a":2}) -> `{"a":2,"b":1}`
func CanonicalJSON(p map[string]any) string {
	b, err := json.Marshal(p) // encoding/json sorts map keys
	if err != nil {
		return ""
	}
	return string(b)
}

// hashVolatile — the volatile fields excluded from hash computation. valid_at is
// injected as now by Normalize when unspecified, and ts is the time itself;
// including them in the hash would keep a resend of the same payload (seeding idempotence) from being judged duplicate.
// v1.20.0 (schema v14): relations joined the exclusion — links are metadata,
// not content, so the same body with·without a relations array hashes
// identically (dedup preservation; the write path accrues relations onto the
// duplicate row's id).
// v1.22.0 (schema v17): conditions joined the exclusion — the manual recheck
// tag is metadata about review scope, not content, so the same body
// with·without (or with a changed) conditions string hashes identically (the
// relations precedent; a duplicate resend never rewrites the stored column).
// v1.24.0 (schema v19): knowledge_kind·source_path joined the exclusion —
// the classification and the vault provenance path are metadata, not
// content (the conditions precedent; both columns stay untouched on a
// duplicate resend).
var hashVolatile = map[string]bool{"valid_at": true, "ts": true, "relations": true, "conditions": true,
	"knowledge_kind": true, "source_path": true}

// knowledgeRelations — the schema v14 (v1.20.0) relation vocabulary: the
// 4 registered knowledge_links.relation values. supersedes is NOT a member —
// lineage stays in knowledge.superseded_by (the dual-source-of-truth
// avoidance; PAM export derives supersedes separately).
var knowledgeRelations = map[string]bool{
	"refines":     true,
	"supports":    true,
	"contradicts": true,
	"duplicates":  true,
}

// RelationVocabulary — the registered relation values in sorted order
// (rejection messages·consumer docs — a stable display form).
func RelationVocabulary() []string {
	out := make([]string, 0, len(knowledgeRelations))
	for k := range knowledgeRelations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidRelation — whether the value is in the registered relation
// vocabulary (case-insensitive·whitespace-trimmed — the normalizeVocab
// convention).
func ValidRelation(rel string) bool {
	return knowledgeRelations[normalizeVocab(rel)]
}

// CheckRelations — validates relation values, returning 1 rejection
// message per unregistered value (empty slice = pass; nil·empty input
// passes — the optional-field convention). The distinct
// "rejected relation:" class follows the colon-token rejection
// precedent (policy.go): an out-of-vocabulary value is refused, not
// merely warned — the write path maps this class to exit 3. v1.20.1
// (r87): the echoed value is capped at 200 runes — a rejection echo
// must never re-print an unbounded input string in full.
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

// HashCanonical returns the canonical JSON excluding the volatile fields
// (valid_at, ts, relations, conditions, knowledge_kind, source_path).
// ContentHash input must always go through this path.
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
