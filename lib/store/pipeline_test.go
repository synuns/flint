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
		{"knowledge", 0.8, 0.8, "active"}, // 경계: floor 미만일 때만 pending
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

// TestClassifyUsesConfFloor — floor 인자(호출자가 meta에서 읽은 conf_floor)를
// 소비(회귀: 판정이 0.8 하드코딩이면 conf 0.7은 pending).
func TestClassifyUsesConfFloor(t *testing.T) {
	cases := []struct {
		conf float64
		want string
	}{
		{0.7, "active"}, // 0.8 하드코딩이면 pending — floor 0.6 소비 증명
		{0.6, "active"}, // 경계: floor 미만일 때만 pending
		{0.59, "pending"},
	}
	for _, c := range cases {
		if got := Classify("knowledge", c.conf, 0.6); got != c.want {
			t.Errorf("floor=0.6 Classify(knowledge, %v) = %q, want %q", c.conf, got, c.want)
		}
	}
}

// TestClassifyDefaultFloor — 기본 floor(DefaultThresholds) 0.8 승계
// (하위호환: meta 부재·조회 실패 경로와 동일 취급).
func TestClassifyDefaultFloor(t *testing.T) {
	floor := DefaultThresholds().ConfFloor
	if got := Classify("knowledge", 0.7, floor); got != "pending" {
		t.Errorf("기본 0.8에서 Classify(knowledge, 0.7) = %q, want pending", got)
	}
	if got := Classify("knowledge", 0.8, floor); got != "active" {
		t.Errorf("기본 0.8 경계 Classify(knowledge, 0.8) = %q, want active", got)
	}
}

// TestClassifyFloorZeroAccepted — floor 0은 파서·TUNING.md 수용 범위(0~1)의
// 하한이므로 거부되지 않는다: 모든 confidence가 active(회귀: 판정측 ≤0 거부로
// 파서와 경계가 어긋나면 pending).
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

// TestMultiCommitOutput — JSONL 다중 커밋 출력 계약(store 계층 소유):
// 집계(inserted·duplicates) + dolt 커밋 해시 1개, 줄별 hash·state·id 없음.
func TestMultiCommitOutput(t *testing.T) {
	got := MultiCommitOutput(2, 1, "wk42abc")
	want := `{"duplicates":1,"hash":"wk42abc","inserted":2,"status":"committed"}`
	if got != want {
		t.Errorf("MultiCommitOutput(2,1,wk42abc) = %s, want %s", got, want)
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
