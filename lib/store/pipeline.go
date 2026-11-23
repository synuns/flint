package store

import (
	"encoding/json"
	"errors"
)

var (
	ErrServerDown = errors.New("flint: server down")

	ErrRetriesExhausted = errors.New("flint: retries exhausted")

	ErrDuplicate = errors.New("flint: duplicate")
)

type committedOutput struct {
	Status string `json:"status"`
	Hash   string `json:"hash"`
	State  string `json:"state"`
	ID     int    `json:"id"`
}

type duplicateOutput struct {
	Status string `json:"status"`
	ID     int    `json:"id,omitempty"`
	Topic  string `json:"topic,omitempty"`
	State  string `json:"state,omitempty"`
}

func Classify(kind string, conf, floor float64) string {
	if kind == "knowledge" && conf < floor {
		return "pending"
	}
	return "active"
}

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

func DuplicateMetaJSON(id int, topic, state string) string {

	b, err := json.Marshal(duplicateOutput{Status: "duplicate", ID: id, Topic: SanitizeText(topic), State: state})
	if err != nil {
		return `{"status":"duplicate"}`
	}
	return string(b)
}

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
