package store

import (
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		kind  string
		conf  float64
		floor float64
		want  string
	}{
		{"knowledge", 0.79, 0.8, "pending"},
		{"knowledge", 0.8, 0.8, "active"}, // boundary: pending only when below the floor
		{"session", 0.1, 0.8, "active"},
		{"knowledge", 0.9, 0.8, "active"},
	}
	for _, c := range cases {
		if got := Classify(c.kind, c.conf, c.floor); got != c.want {
			t.Errorf("Classify(%q, %v, %v) = %q, want %q", c.kind, c.conf, c.floor, got, c.want)
		}
	}
}

func TestOutputJSON(t *testing.T) {
	got := OutputJSON("committed", "abc123", "pending", 3)
	want := `{"status":"committed","hash":"abc123","state":"pending","id":3}`
	if got != want {
		t.Errorf("OutputJSON committed = %s, want %s", got, want)
	}
}

func TestOutputJSONSingleLine(t *testing.T) {
	got := OutputJSON("committed", "h", "active", 1)
	for _, r := range got {
		if r == '\n' || r == '\r' {
			t.Fatalf("출력에 줄바꿈 포함: %q", got)
		}
	}
}

func TestOutputJSONDuplicate(t *testing.T) {
	got := OutputJSON("duplicate", "", "", 0)
	want := `{"status":"duplicate"}`
	if got != want {
		t.Errorf("OutputJSON duplicate = %s, want %s", got, want)
	}
}

func TestMapExit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"duplicate", ErrDuplicate, 0},
		{"retries exhausted", ErrRetriesExhausted, 1},
		{"server down", ErrServerDown, 2},
		{"schema error", &SchemaError{Field: "summary"}, 3},
	}
	for _, c := range cases {
		if got := MapExit(c.err); got != c.want {
			t.Errorf("MapExit(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestClassifyUsesConfFloor — consumes the floor argument (conf_floor read from
// meta by the caller) (regression: with a hardcoded 0.8 verdict, conf 0.7 is pending).
func TestClassifyUsesConfFloor(t *testing.T) {
	cases := []struct {
		conf float64
		want string
	}{
		{0.7, "active"}, // pending under a hardcoded 0.8 — proof the floor 0.6 is consumed
		{0.6, "active"}, // boundary: pending only when below the floor
		{0.59, "pending"},
	}
	for _, c := range cases {
		if got := Classify("knowledge", c.conf, 0.6); got != c.want {
			t.Errorf("floor=0.6 Classify(knowledge, %v) = %q, want %q", c.conf, got, c.want)
		}
	}
}

// TestClassifyDefaultFloor — inherits the default floor (DefaultThresholds) 0.8
// (backward compatible: the same treatment as the meta-absent and lookup-failure paths).
func TestClassifyDefaultFloor(t *testing.T) {
	floor := DefaultThresholds().ConfFloor
	if got := Classify("knowledge", 0.7, floor); got != "pending" {
		t.Errorf("기본 0.8에서 Classify(knowledge, 0.7) = %q, want pending", got)
	}
	if got := Classify("knowledge", 0.8, floor); got != "active" {
		t.Errorf("기본 0.8 경계 Classify(knowledge, 0.8) = %q, want active", got)
	}
}

// TestClassifyFloorZeroAccepted — floor 0 is the lower bound of the parser's and
// TUNING.md's accepted range (0..1), so it is not rejected: every confidence is
// active (regression: if the verdict side rejected <=0, disagreeing with the parser's boundary, pending).
func TestClassifyFloorZeroAccepted(t *testing.T) {
	cases := []struct {
		conf float64
		want string
	}{
		{0, "active"},
		{0.7, "active"},
	}
	for _, c := range cases {
		if got := Classify("knowledge", c.conf, 0); got != c.want {
			t.Errorf("floor=0 Classify(knowledge, %v) = %q, want %q", c.conf, got, c.want)
		}
	}
}

// TestMultiCommitOutput — the JSONL multi-commit output contract (owned by the
// store layer): the aggregates (inserted, duplicates) + 1 dolt commit hash, no per-line hash/state/id.
func TestMultiCommitOutput(t *testing.T) {
	got := MultiCommitOutput(2, 1, "wk42abc")
	want := `{"duplicates":1,"hash":"wk42abc","inserted":2,"status":"committed"}`
	if got != want {
		t.Errorf("MultiCommitOutput(2,1,wk42abc) = %s, want %s", got, want)
	}
}

// TestMapExitWrapped — db-layer aliased symbols wrapped in %w still get the same exit (contract: passing through errors.Is).
func TestMapExitWrapped(t *testing.T) {
	if got := MapExit(fmt.Errorf("dial: %w", ErrServerDown)); got != 2 {
		t.Errorf("MapExit(wrapped ErrServerDown) = %d, want 2", got)
	}
	if got := MapExit(fmt.Errorf("batch: %w", &SchemaError{Field: "summary"})); got != 3 {
		t.Errorf("MapExit(wrapped SchemaError) = %d, want 3", got)
	}
	if got := MapExit(fmt.Errorf("retry: %w", ErrRetriesExhausted)); got != 1 {
		t.Errorf("MapExit(wrapped ErrRetriesExhausted) = %d, want 1", got)
	}
}
