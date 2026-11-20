// tuning_test.go — the tuning command contracts — list/get/set/reset,
// real-path value validation (rejecting fallback values), and unknown keys exiting 3.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// tuningFake — a fake recording meta IN lookups, upserts, and deletes.
type tuningFake struct {
	*fakeStore
	meta map[string]string
	sets []string
	dels []string
}

func newTuningFake() *tuningFake {
	return &tuningFake{fakeStore: &fakeStore{}, meta: map[string]string{}}
}

func (f *tuningFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	switch {
	case strings.Contains(sql, "key IN ("):
		rows := [][]any{}
		for _, k := range f.meta {
			_ = k
		}
		for k, v := range f.meta {
			if containsKey(args, k) {
				rows = append(rows, [][]any{{k, v}}[0])
			}
		}
		return rows, nil
	case strings.Contains(sql, "INSERT INTO meta"):
		f.sets = append(f.sets, fmt.Sprint(args[0], "=", args[1]))
		f.meta[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
		return nil, nil
	}
	// the loadThresholds meta IN lookup is handled by the key IN case above — reaching here
	// means a fixture-routing contract violation (Fatalf unavailable — identified via an error).
	return nil, fmt.Errorf("tuningFake 미매칭: %s", sql)
}

func containsKey(args []any, k string) bool {
	for _, a := range args {
		if fmt.Sprint(a) == k {
			return true
		}
	}
	return false
}

// MetaDelete — reset delegates to db.Repo.MetaDelete (write guard) —
// the fake records and applies the deleted keys.
func (f *tuningFake) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	for _, k := range keys {
		f.dels = append(f.dels, k)
		delete(f.meta, k)
	}
	return int64(len(keys)), nil
}

func TestTuningSetValidatesThroughRealParse(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	// valid value — stored.
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "45"}, nil, &out); code != 0 {
		t.Fatalf("set exit=%d: %s", code, out.String())
	}
	if len(f.sets) != 1 || f.meta["dead_days"] != "45" {
		t.Fatalf("sets=%v meta=%v", f.sets, f.meta)
	}
	// invalid value — parse-fallback detected and rejected (exit 3, not recorded).
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "many"}, nil, &out); code != 3 {
		t.Fatalf("무효 값 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.sets) != 1 {
		t.Fatalf("무효 값까지 기록: %v", f.sets)
	}
}

func TestTuningUnknownKeyAndList(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"get", "nonexistent_key"}, nil, &out); code != 3 {
		t.Fatalf("unknown key exit=%d, want 3", code)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"list"}, nil, &out); code != 0 {
		t.Fatalf("list exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"key":"dead_days"`) || !strings.Contains(out.String(), `"override":false`) {
		t.Errorf("list 출력 계약 위반: %s", out.String())
	}
	// reset — deletes after setting the override.
	runTuning(context.Background(), f, []string{"set", "stale_days", "60"}, nil, &out)
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"reset", "stale_days"}, nil, &out); code != 0 {
		t.Fatalf("reset exit=%d: %s", code, out.String())
	}
	if len(f.dels) != 1 || f.dels[0] != "stale_days" {
		t.Fatalf("dels=%v", f.dels)
	}
}

// TestTuningKeysMatchRegistry — the homogeneity of tuningKeys and
// thresholdsKeys (shared source) — defending against 3-way drift when adding new keys.
func TestTuningKeysMatchRegistry(t *testing.T) {
	reg := map[string]bool{}
	for _, k := range thresholdsKeys() {
		reg[k] = true
	}
	km := tuningKeyMap()
	if len(km) != len(reg) {
		t.Fatalf("키 수 불일치: tuning %d vs registry %d", len(km), len(reg))
	}
	for k := range km {
		if !reg[k] {
			t.Errorf("tuning 전용 키 %q — thresholdsKeys에 없음", k)
		}
	}
}

// TestTuningSetRejectsZero — a 0 on a fallback key means "unset" and is thus
// unrepresentable — it must be rejected (4 keys excluded).
func TestTuningSetRejectsZero(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "0"}, nil, &out); code != 3 {
		t.Fatalf("dead_days=0 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.sets) != 0 {
		t.Fatalf("0이 기록됨: %v", f.sets)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "pending_max", "0"}, nil, &out); code != 0 {
		t.Fatalf("pending_max=0(실값 유효) exit=%d: %s", code, out.String())
	}
}

// TestTuningMetaKeysSetReset — set validation for meta keys outside thresholds
// (enumeration, integer, list) and reset --all.
func TestTuningMetaKeysSetReset(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	// exposure_profile enumeration validation.
	if code := runTuning(context.Background(), f, []string{"set", "exposure_profile", "loud"}, nil, &out); code != 3 {
		t.Fatalf("무효 열거 exit=%d: %s", code, out.String())
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "exposure_profile", "minimal"}, nil, &out); code != 0 {
		t.Fatalf("minimal set exit=%d: %s", code, out.String())
	}
	// vocab list validation — empty elements rejected, valid lists allowed.
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "vocab_tags", "a,,b"}, nil, &out); code != 3 {
		t.Fatalf("빈 원소 exit=%d: %s", code, out.String())
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "vocab_tags", "ops,k8s"}, nil, &out); code != 0 {
		t.Fatalf("정상 목록 exit=%d: %s", code, out.String())
	}
	// reset --all — deletes everything.
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"reset", "--all"}, nil, &out); code != 0 {
		t.Fatalf("reset --all exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"reset-all"`) {
		t.Fatalf("reset --all 출력: %s", out.String())
	}
}

// TestTuningSetEquivalentFloatNotation —
// Notations like '0.30' that are numerically valid but differently formatted, previously rejected by
// string comparison, are now accepted by numeric comparison.
func TestTuningSetEquivalentFloatNotation(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"set", "conf_floor", "0.30"}, nil, &out); code != 0 {
		t.Fatalf("0.30 exit=%d(수치 유효): %s", code, out.String())
	}
	if f.meta["conf_floor"] != "0.30" {
		t.Fatalf("기록값 = %v", f.meta)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "conf_floor", "1.5"}, nil, &out); code != 3 {
		t.Fatalf("범위 밖 1.5 exit=%d, want 3: %s", code, out.String())
	}
}
