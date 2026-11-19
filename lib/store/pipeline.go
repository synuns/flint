package store

import (
	"encoding/json"
	"errors"
)

// the error symbols for the exit verdicts. The db layer (pool, repo) uses these
// values directly (passing through errors.Is) or aliases its own symbols to them.
var (
	// ErrServerDown — immediate failure, exit 2.
	ErrServerDown = errors.New("flint: server down")
	// ErrRetriesExhausted — retries exhausted, exit 1.
	ErrRetriesExhausted = errors.New("flint: retries exhausted")
	// ErrDuplicate — a content_hash duplicate, exit 0 (idempotent).
	ErrDuplicate = errors.New("flint: duplicate")
)

// the committed output body. One line of JSON, the key order fixed as status, hash, state, id.
type committedOutput struct {
	Status string `json:"status"`
	Hash   string `json:"hash"`
	State  string `json:"state"`
	ID     int    `json:"id"`
}

// duplicateOutput — exposes the id field only when id > 0 (the existing row's
// id. The topic and state are exposed additively — already-exists feedback. An
// empty meta keeps the existing bytes via omitempty. id 0 (an unsupported Store or multi-line) yields the bare {"status":"duplicate"}).
type duplicateOutput struct {
	Status string `json:"status"`
	ID     int    `json:"id,omitempty"`
	Topic  string `json:"topic,omitempty"`
	State  string `json:"state,omitempty"`
}

// Classify is the write verdict rule: kind == knowledge && confidence < floor
// -> pending; everything else (including the boundary) is active. The floor is
// read from meta conf_floor by the caller and passed as a value (no global
// injection — the doneThresholds convention). The range check has a single source in the parser (ParseThresholds, accepting 0..1).
func Classify(kind string, conf, floor float64) string {
	if kind == "knowledge" && conf < floor {
		return "pending"
	}
	return "active"
}

// OutputJSON serializes the pipeline output as one line of JSON.
// If status == "duplicate", it outputs only status and (when id > 0) id, discarding the other fields.
func OutputJSON(status, hash, state string, id int) string {
	if status == "duplicate" {
		b, err := json.Marshal(duplicateOutput{Status: status, ID: id})
		if err != nil {
			return `{"status":"duplicate"}`
		}
		return string(b)
	}
	b, err := json.Marshal(committedOutput{Status: status, Hash: hash, State: state, ID: id})
	if err != nil {
		return `{"status":"` + status + `"}`
	}
	return string(b)
}

// DuplicateMetaJSON — the duplicate output of a single-line resend: exposes the
// existing row's topic and state additively so the writer can switch to
// read/supersede/approve (the already-exists feedback loop). id 0 or empty meta matches the existing bytes (backward compatible).
func DuplicateMetaJSON(id int, topic, state string) string {
	// the existing row's topic echo is re-sanitized — even if the stored body predates
	// sanitization or came from direct SQL, the output boundary neutralizes markers.
	b, err := json.Marshal(duplicateOutput{Status: "duplicate", ID: id, Topic: SanitizeText(topic), State: state})
	if err != nil {
		return `{"status":"duplicate"}`
	}
	return string(b)
}

// MapExit maps a pipeline error to an exit code.
// nil/duplicate -> 0, retries exhausted -> 1, server down -> 2, validation failure -> 3, else -> 1.
func MapExit(err error) int {
	var se *SchemaError
	switch {
	case err == nil, errors.Is(err, ErrDuplicate):
		return 0
	case errors.Is(err, ErrRetriesExhausted):
		return 1
	case errors.Is(err, ErrServerDown):
		return 2
	case errors.As(err, &se):
		return 3
	default:
		return 1
	}
}

// MultiCommitOutput — the JSONL multi-line (2+) commit output body. Instead of
// per-line mixed hash/state/id, it keeps only the aggregates (inserted,
// duplicates) and 1 batch dolt commit hash (the output contract is owned by the store layer — next to committedOutput).
func MultiCommitOutput(inserted, duplicates int, commitHash string) string {
	b, err := json.Marshal(map[string]any{
		"status":     "committed",
		"inserted":   inserted,
		"duplicates": duplicates,
		"hash":       commitHash,
	})
	if err != nil {
		return `{"status":"committed"}`
	}
	return string(b)
}
