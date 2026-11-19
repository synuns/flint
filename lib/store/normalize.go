package store

import (
	"encoding/json"
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
var hashVolatile = map[string]bool{"valid_at": true, "ts": true}

// HashCanonical returns the canonical JSON excluding the volatile fields
// (valid_at, ts). ContentHash input must always go through this path.
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
