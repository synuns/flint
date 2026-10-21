package store

import "testing"

// TestValidate — 플랜 T3 예제 4종 + 경계/거부 케이스.
func TestValidate(t *testing.T) {
	validKnowledge := map[string]any{"topic": "x", "summary": "y", "confidence": 0.9}
	cases := []struct {
		name    string
		kind    string
		p       map[string]any
		wantNil bool
		field   string
	}{
		{"knowledge 유효", "knowledge", validKnowledge, true, ""},
		{"knowledge 태그 포함 유효", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.9, "tags": []any{"a"}}, true, ""},
		{"knowledge summary 누락", "knowledge", map[string]any{"topic": "x"}, false, "summary"},
		{"confidence 1.01 거부", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 1.01}, false, "confidence"},
		{"confidence -0.1 거부", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": -0.1}, false, "confidence"},
		{"confidence 0 허용", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.0}, true, ""},
		{"confidence 1 허용", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 1.0}, true, ""},
		{"tags 비문자열 거부", "knowledge", map[string]any{"topic": "x", "summary": "y", "confidence": 0.5, "tags": []any{1}}, false, "tags"},
		{"session 유효", "session", map[string]any{"agent": "a", "summary": "s"}, true, ""},
		{"session agent 누락", "session", map[string]any{"summary": "s"}, false, "agent"},
		{"project 유효", "project", map[string]any{"name": "n", "status": "active"}, true, ""},
		{"project status 불량", "project", map[string]any{"name": "n", "status": "zombie"}, false, "status"},
		{"알 수 없는 kind", "blob", map[string]any{}, false, "kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			se := Validate(c.kind, c.p)
			if c.wantNil {
				if se != nil {
					t.Fatalf("Validate(%s) = %v, want nil", c.kind, se)
				}
				return
			}
			if se == nil {
				t.Fatalf("Validate(%s) = nil, want SchemaError{Field:%s}", c.kind, c.field)
			}
			if se.Field != c.field {
				t.Errorf("SchemaError.Field = %q, want %q", se.Field, c.field)
			}
			if se.Expect == "" {
				t.Errorf("SchemaError.Expect가 비어 있음 (exit 3 자가수정용)")
			}
		})
	}
}
