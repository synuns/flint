package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"flint/lib/store"
)

// staleQueryFixture — meta 조회는 임계값 rows, knowledge 조회는 지정 rows를 반환.
func staleQueryFixture(t *testing.T, f *fakeMaint, metaRows, knolRows [][]any) {
	t.Helper()
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return metaRows, nil
		}
		return knolRows, nil
	}
}

func ago(d time.Duration) time.Time { return time.Now().Add(-d) }

// TestStaleBucketsAndStallWarnings — Review Focus(state):
// 미참조(read_count=0)/경과(last_read_at)/활성 3행 → 2건만·never_read 1·경고 배열.
func TestStaleBucketsAndStallWarnings(t *testing.T) {
	knol := [][]any{
		{1, "미참조 노트", nil, ago(100 * 24 * time.Hour), 0},
		{2, "읽었지만 오래됨", ago(95 * 24 * time.Hour), ago(100 * 24 * time.Hour), 5},
		{3, "활성", ago(time.Hour), ago(24 * time.Hour), 3},
	}
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	items, _ := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d건, want 2 (활성 제외): %s", len(items), out)
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != float64(1) || first["never_read"] != true {
		t.Errorf("never_read 버킷 구분 실패: %v", first)
	}
	second, _ := items[1].(map[string]any)
	if second["id"] != float64(2) || second["never_read"] != false {
		t.Errorf("read-stale 버킷: %v", second)
	}
	if first["topic"] == "" || first["stale_days"] == nil {
		t.Errorf("id/topic/경과 필드 누락: %v", first)
	}

	// --check stall 경고 — last_hot_at 73h(>48h grace) 경과 → warnings 배열·exit 0.
	hotOld := time.Now().Add(-73 * time.Hour).UTC().Format(time.RFC3339)
	writeFresh := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	fc := &fakeMaint{fakeStore: &fakeStore{}}
	fc.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_drill") {
			return nil, nil
		}
		if strings.Contains(sql, "last_hot_at") {
			return [][]any{{"last_hot_at", hotOld}, {"last_write_at", writeFresh}}, nil
		}
		return nil, nil
	}
	outC, codeC := runMaintainArgs(t, fc, "--check")
	if codeC != 0 {
		t.Fatalf("--check exit=%d, want 0; out=%s", codeC, outC)
	}
	var mc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(outC)), &mc); err != nil {
		t.Fatalf("JSON 아님: %q", outC)
	}
	warns, _ := mc["warnings"].([]any)
	if len(warns) != 1 || !strings.Contains(jsonStr(t, warns[0]), "last_hot_at") {
		t.Fatalf("hot stall 경고 1건이어야 함(write는 이내): %s", outC)
	}
}

func jsonStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestStaleDaysOverride — --days N 우선·meta stale_days 오버라이드(기본 90).
func TestStaleDaysOverride(t *testing.T) {
	knol := [][]any{
		{10, "40일 경과", nil, ago(40 * 24 * time.Hour), 0},
		{11, "20일 경과", nil, ago(20 * 24 * time.Hour), 0},
	}
	// 기본 90일 → 0건.
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"count":0`) {
		t.Fatalf("기본 90일에는 0건이어야 함: %s", out)
	}

	// --days 30 → 40일 행만.
	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f2, nil, knol)
	out2, _ := runMaintainArgs(t, f2, "--stale", "--days", "30")
	if !strings.Contains(out2, `"id":10`) || strings.Contains(out2, `"id":11`) {
		t.Fatalf("--days 30 미반영: %s", out2)
	}

	// meta stale_days=30 → 플래그 없이도 40일 행만.
	f3 := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f3, [][]any{{"stale_days", "30"}}, knol)
	out3, _ := runMaintainArgs(t, f3, "--stale")
	if !strings.Contains(out3, `"id":10`) || strings.Contains(out3, `"id":11`) {
		t.Fatalf("meta stale_days 미반영: %s", out3)
	}
}

// TestStaleDayBoundary — 경계는 미만: 91일 행 포함·89일 행 제외.
func TestStaleDayBoundary(t *testing.T) {
	knol := [][]any{
		{20, "91일", nil, ago(91 * 24 * time.Hour), 0},
		{21, "89일", nil, ago(89 * 24 * time.Hour), 0},
	}
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"id":20`) {
		t.Errorf("91일 행 미포함: %s", out)
	}
	if strings.Contains(out, `"id":21`) {
		t.Errorf("89일(경계 이내) 행 포함됨: %s", out)
	}
}

// TestStaleServerDown — 서버 다운 → MapExit 2.
func TestStaleServerDown(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryErr: store.ErrServerDown}}
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 JSON 아님: %q", out)
	}
}

// --limit 기본 20 — SQL LIMIT 인자로 전달된다.
func TestStaleLimitDefault(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, nil)
	runMaintainArgs(t, f, "--stale")
	found := false
	for _, q := range f.queries {
		if strings.Contains(q, "LIMIT") && strings.Contains(q, "knowledge") {
			found = true
		}
	}
	if !found {
		t.Fatalf("LIMIT 있는 stale 조회 미발생: %v", f.queries)
	}
}
