package store

import (
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		kind string
		conf float64
		want string
	}{
		{"knowledge", 0.79, "pending"},
		{"knowledge", 0.8, "active"}, // 경계: 0.8 미만일 때만 pending
		{"session", 0.1, "active"},
		{"knowledge", 0.9, "active"},
	}
	for _, c := range cases {
		if got := Classify(c.kind, c.conf); got != c.want {
			t.Errorf("Classify(%q, %v) = %q, want %q", c.kind, c.conf, got, c.want)
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

// TestMapExitWrapped — db 계층 별칭 심볼이 %w로 래핑돼도 동일 exit(계약: errors.Is 통과).
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
