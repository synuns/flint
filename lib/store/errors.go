package store

// SchemaError는 필드 단위 검증 실패를 나타낸다.
// (T3 예제: Validate("knowledge", {"topic":"x"}) -> &SchemaError{Field:"summary"})
type SchemaError struct {
	Field string
}

func (e *SchemaError) Error() string {
	return "schema error: field " + e.Field
}
