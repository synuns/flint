package store

const (
	KindExpect   = "knowledge|session|project"
	statusExpect = "active|paused|done"
)

func Validate(kind string, p map[string]any) *SchemaError {
	switch kind {
	case "knowledge":
		for _, f := range []string{"topic", "summary"} {
			if se := requireText(p, f); se != nil {
				return se
			}
		}
		c, ok := p["confidence"]
		if !ok {
			return &SchemaError{Field: "confidence", Expect: "required, 0..1"}
		}
		if !inUnitRange(c) {
			return &SchemaError{Field: "confidence", Expect: "0..1"}
		}
		if tags, ok := p["tags"]; ok && tags != nil {
			if !isStringArray(tags) {
				return &SchemaError{Field: "tags", Expect: "array of strings"}
			}
		}
	case "session":
		for _, f := range []string{"agent", "summary"} {
			if se := requireText(p, f); se != nil {
				return se
			}
		}
	case "project":
		if se := requireText(p, "name"); se != nil {
			return se
		}
		st, _ := p["status"].(string)
		if st != "active" && st != "paused" && st != "done" {
			return &SchemaError{Field: "status", Expect: statusExpect}
		}
	default:
		return &SchemaError{Field: "kind", Expect: KindExpect}
	}
	return nil
}

func requireText(p map[string]any, field string) *SchemaError {
	if s, ok := p[field].(string); ok && s != "" {
		return nil
	}
	return &SchemaError{Field: field, Expect: "required non-empty string"}
}

func inUnitRange(v any) bool {
	switch n := v.(type) {
	case float64:
		return n >= 0 && n <= 1
	case int:
		return n >= 0 && n <= 1
	default:
		return false
	}
}

func isStringArray(v any) bool {
	switch t := v.(type) {
	case []string:
		return true
	case []any:
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}
