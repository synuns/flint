// Package store는 kind별 검증·정규화·해시를 제공한다.
package store

// kind 허용값과 각 kind의 필수 필드(검증 순서).
const (
	kindExpect   = "knowledge|session|project"
	statusExpect = "active|paused|done"
)

// Validate는 kind 입력값의 필수 필드를 검증한다. 위반 시 해당 필드의
// SchemaError를, 유효하면 nil을 반환한다.
//
// 반환값이 구체 타입(*SchemaError)이므로 호출자는 nil 검사를 반환값 직전에
// 해야 한다(인터페이스 변수에 담아 != nil을 검사하면 typed-nil 함정).
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
		return &SchemaError{Field: "kind", Expect: kindExpect}
	}
	return nil
}

// requireText — 필드가 비지 않은 문자열인지.
func requireText(p map[string]any, field string) *SchemaError {
	if s, ok := p[field].(string); ok && s != "" {
		return nil
	}
	return &SchemaError{Field: field, Expect: "required non-empty string"}
}

// inUnitRange — confidence 경계: 0과 1은 허용, 1.01·-0.1은 거부.
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

// isStringArray — JSON 디코딩 결과([]any)와 []string 모두 허용.
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
