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

// TestClassifyUsesConfFloor — meta conf_floor 주입 시 Classify가
// Thresholds.ConfFloor를 소비(회귀: 판정이 0.8 하드코딩이면 conf 0.7은 pending).
func TestClassifyUsesConfFloor(t *testing.T) {
	t.Cleanup(func() { SetClassifyThresholds(DefaultThresholds()) })
	SetClassifyThresholds(Thresholds{ConfFloor: 0.6})
	cases := []struct {
		conf float64
		want string
	}{
		{0.7, "active"}, // 0.8 하드코딩이면 pending — conf_floor 0.6 소비 증명
		{0.6, "active"}, // 경계: floor 미만일 때만 pending
		{0.59, "pending"},
	}
	for _, c := range cases {
		if got := Classify("knowledge", c.conf); got != c.want {
			t.Errorf("conf_floor=0.6 Classify(knowledge, %v) = %q, want %q", c.conf, got, c.want)
		}
	}
}

// TestClassifyDefaultThreshold — thresholds 미주입·무효값은 기본 0.8 승계
// (하위호환: meta 부재·조회 실패 경로와 동일 취급).
func TestClassifyDefaultThreshold(t *testing.T) {
	t.Cleanup(func() { SetClassifyThresholds(DefaultThresholds()) })
	SetClassifyThresholds(DefaultThresholds())
	// 무효값(0) 주입은 기본을 덮지 않는다.
	SetClassifyThresholds(Thresholds{ConfFloor: 0})
	if got := Classify("knowledge", 0.7); got != "pending" {
		t.Errorf("기본 0.8에서 Classify(knowledge, 0.7) = %q, want pending", got)
	}
	if got := Classify("knowledge", 0.8); got != "active" {
		t.Errorf("기본 0.8 경계 Classify(knowledge, 0.8) = %q, want active", got)
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
