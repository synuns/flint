package store

// SchemaError represents a per-field validation failure.
// (e.g. Validate("knowledge", {"topic":"x"}) -> &SchemaError{Field:"summary"})
type SchemaError struct {
	Field  string
	Expect string
}

func (e *SchemaError) Error() string {
	if e.Expect == "" {
		return "schema error: field " + e.Field
	}
	return "schema error: field " + e.Field + ", expect " + e.Expect
}
