package cli

import (
	"context"
	"errors"
	"github.com/synuns/flint/lib/wire"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatusSchemaV4OK(t *testing.T) {
	m, code := statusWithVersion(t, "4")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v4 호환)", code)
	}
	if m["schema_version"] != "4" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=4/ok", m)
	}
}

func windowedReadHarness(t *testing.T) *fakeStore {
	t.Helper()
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -40).Format("2006-01-02")
	daily := `{"read":10,"read_zero":4,"read_zero_near":1}`
	dailyY := `{"read":10,"read_zero":1}`
	dailyOld := `{"read":100,"read_zero":100}`
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "LIKE 'daily:%'") {
			return [][]any{
				{"daily:" + today, daily},
				{"daily:" + yesterday, dailyY},
				{"daily:" + old, dailyOld},
			}, nil
		}
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"5"}}, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	return f
}

func TestStatusWindowedRead(t *testing.T) {
	f := windowedReadHarness(t)
	read, zeroPct, nearPct := statusWindowedRead(context.Background(), f, 14)
	if read != 20 {
		t.Fatalf("read=%d, want 20(창 내 합산)", read)
	}
	if zeroPct != 25 {
		t.Fatalf("zeroPct=%d, want 25(5/20)", zeroPct)
	}
	if nearPct != 5 {
		t.Fatalf("nearPct=%d, want 5(1/20)", nearPct)
	}
}

func TestStatusWindowedKeys(t *testing.T) {
	windowedReadHarness(t)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if v, ok := m["read_calls_14d"].(float64); !ok || int(v) != 20 {
		t.Fatalf("read_calls_14d=%v, want 20", m["read_calls_14d"])
	}
	if v, ok := m["read_zero_pct_14d"].(float64); !ok || int(v) != 25 {
		t.Fatalf("read_zero_pct_14d=%v, want 25", m["read_zero_pct_14d"])
	}
	for _, legacy := range []string{"read_calls", "read_zero_pct", "pending", "schema_version"} {
		if _, ok := m[legacy]; !ok {
			t.Errorf("기존 키 %s 누락 — additive 원칙 위반", legacy)
		}
	}
}

func TestStatusZeroNearKey(t *testing.T) {
	windowedReadHarness(t)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	m := decode(t, out)
	if v, ok := m["read_zero_near_pct_14d"].(float64); !ok || int(v) != 5 {
		t.Fatalf("read_zero_near_pct_14d=%v, want 5(1/20)", m["read_zero_near_pct_14d"])
	}
}

func statusWithMeta(t *testing.T, kv map[string]string) (map[string]any, int) {
	t.Helper()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			key := "schema_version"
			if len(args) > 0 {
				if k, ok := args[0].(string); ok {
					key = k
				}
			} else if i := strings.Index(sql, "key = '"); i >= 0 {
				rest := sql[i+len("key = '"):]
				key = rest[:strings.Index(rest, "'")]
			}
			if v, ok := kv[key]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	var m map[string]any
	if code == 0 {
		m = decode(t, out)
	}
	return m, code
}

func TestStatusLastSeenFields(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	w := ts.Add(-2 * time.Hour).Format(time.RFC3339)
	h := ts.Add(-1 * time.Hour).Format(time.RFC3339)
	r := ts.Format(time.RFC3339)
	m, code := statusWithMeta(t, map[string]string{
		"schema_version": "3",
		"last_write_at":  w,
		"last_hot_at":    h,
		"last_read_at":   r,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if m["last_write_at"] != w {
		t.Fatalf("last_write_at=%v, want %s", m["last_write_at"], w)
	}
	if m["last_hot_at"] != h {
		t.Fatalf("last_hot_at=%v, want %s", m["last_hot_at"], h)
	}
	if m["last_read_at"] != r {
		t.Fatalf("last_read_at=%v, want %s", m["last_read_at"], r)
	}
}

func TestStatusLastSeenMissingOrNull(t *testing.T) {
	m, code := statusWithMeta(t, map[string]string{
		"schema_version": "3",
		"last_write_at":  "not-a-timestamp",
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	for _, k := range []string{"last_write_at", "last_hot_at", "last_read_at"} {
		v, ok := m[k]
		if !ok {
			t.Fatalf("필드 %s 누락: %v", k, m)
		}
		if v != nil {
			t.Fatalf("%s=%v, want null", k, v)
		}
	}
}

func TestStatusLastSeenFieldOrder(t *testing.T) {
	m, code := statusWithMeta(t, map[string]string{"schema_version": "3"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	want := []string{"status", "schema_version", "pending", "hot_calls",
		"hot_p95", "read_calls", "read_zero_pct", "last_write_at",
		"last_hot_at", "last_read_at"}

	var got []string
	typ := reflect.TypeOf(statusOutput{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		got = append(got, strings.Split(tag, ",")[0])
	}
	if len(got) != len(want) {
		t.Fatalf("필드 수=%d(%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("필드 순서[%d]=%s, want %s (전체 %v)", i, got[i], want[i], got)
		}
	}
	if m["status"] != "ok" || m["schema_version"] != "3" {
		t.Fatalf("기존 필드 회귀: %v", m)
	}
}

func TestStatusLastSeenEmptyAndQueryError(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		key := ""
		if len(args) > 0 {
			if k, ok := args[0].(string); ok {
				key = k
			}
		}
		if strings.Contains(sql, "'schema_version'") {

			return [][]any{{"3"}}, nil
		}
		if strings.HasPrefix(sql, "SELECT value FROM meta") {
			switch key {
			case "last_write_at":

				return [][]any{{""}}, nil
			case "last_hot_at", "last_read_at":

				return nil, errors.New("boom")
			}
			return nil, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (last_* 조회 오류는 status를 깨지 않음): %s", code, out)
	}
	m := decode(t, out)
	for _, k := range []string{"last_write_at", "last_hot_at", "last_read_at"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Fatalf("%s=%v(ok=%v), want null", k, v, ok)
		}
	}
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok", m["status"])
	}
}

type statusAsofFake struct {
	*fakeStore
	fullHash    string
	asofCount   int64
	asofQueries []wire.AsOfQuery
}

var statusCommitDate = time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)

func (f *statusAsofFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Prefix != "":
		return []wire.LogEntry{{Hash: f.fullHash}}, nil
	case q.Hash == f.fullHash:
		return []wire.LogEntry{{Hash: f.fullHash, Date: statusCommitDate}}, nil
	}
	return nil, nil
}

func (f *statusAsofFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	f.asofQueries = append(f.asofQueries, q)
	return [][]any{{f.asofCount}}, nil
}

func statusAsOfHarness(t *testing.T, _, fullHash string, curCount, asofCount int64) *statusAsofFake {
	t.Helper()
	f := &statusAsofFake{fakeStore: &fakeStore{}, fullHash: fullHash, asofCount: asofCount}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"4"}}, nil
		}
		return [][]any{{curCount}}, nil
	}
	withFakeStore(t, f)
	return f
}

func TestStatusAsOfCounts(t *testing.T) {
	f := statusAsOfHarness(t, "abc", "abcdef0123456789", 5, 0)
	out, code := mustRun(t, []string{"status", "--as-of", "abc"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)

	ao, ok := m["as_of"].(map[string]any)
	if !ok {
		t.Fatalf("as_of가 객체 아님: %v", m["as_of"])
	}
	if ao["hash"] != "abcdef0123456789" {
		t.Fatalf("as_of.hash=%v, want abcdef0123456789", ao["hash"])
	}
	if ao["commit_date"] != "2026-10-20T09:30:00Z" {
		t.Fatalf("as_of.commit_date=%v, want 2026-10-20T09:30:00Z", ao["commit_date"])
	}
	if m["pending"] != float64(0) {
		t.Fatalf("pending=%v, want 0(과거 스냅샷)", m["pending"])
	}

	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 카운트 질의 %d건, want 1: %+v", len(f.asofQueries), f.asofQueries)
	}
	aq := f.asofQueries[0]
	if aq.Hash != "abcdef0123456789" || aq.Table != "knowledge" {
		t.Fatalf("knowledge 카운트 질의 접합 불일치: %+v", aq)
	}
	if !strings.Contains(aq.Where, "status = 'pending'") {
		t.Fatalf("기존 pending 조건 유실: %s", aq.Where)
	}

	if m["schema_version"] != "4" {
		t.Fatalf("schema_version=%v, want 4(현재 메타)", m["schema_version"])
	}
}

func TestStatusAsOfFieldOrder(t *testing.T) {
	wantBase := []string{"status", "schema_version", "pending", "hot_calls",
		"hot_p95", "read_calls", "read_zero_pct", "last_write_at",
		"last_hot_at", "last_read_at"}
	var got []string
	typ := reflect.TypeOf(statusOutput{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		got = append(got, strings.Split(tag, ",")[0])
	}
	if len(got) != len(wantBase) || strings.Join(got, ",") != strings.Join(wantBase, ",") {
		t.Fatalf("statusOutput 필드 불변 위반: %v", got)
	}
	statusAsOfHarness(t, "abc", "abcdef0123456789", 1, 0)
	out, code := mustRun(t, []string{"status", "--as-of", "abc"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	want := append(append([]string{}, wantBase...), "as_of")
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("키 %s 누락: %v", k, m)
		}
	}
	if _, ok := m["__extra"]; ok {
		t.Fatalf("예상 외 필드: %v", m)
	}

	if !strings.HasPrefix(strings.TrimSpace(out), `{"status":`) {
		t.Fatalf("첫 키 불변 위반: %s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out),
		`,"as_of":{"hash":"abcdef0123456789","commit_date":"2026-10-20T09:30:00Z"}}`) {
		t.Fatalf("as_of가 말미 아님(객체 형태): %s", out)
	}

	f2 := statusAsOfHarness(t, "", "", 2, 0)
	_ = f2
	out2, code2 := mustRun(t, []string{"status"}, "")
	if code2 != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code2, out2)
	}
	m2 := decode(t, out2)
	if _, ok := m2["as_of"]; ok {
		t.Fatalf("미지정 시 as_of 출력 금지: %v", m2)
	}
}

func TestStatusAsOfInvalidExit3(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"4"}}, nil
		}
		return nil, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status", "--as-of", "2 days ago"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if m := decode(t, out); m["error"] == "" && m["message"] == "" {
		t.Fatalf("에러 한 줄 JSON 아님: %s", out)
	}
}

func TestStatusAsOfMissingValueExit3(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"4"}}, nil
		}
		return nil, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status", "--as-of"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)
}

func TestStatusSchemaV5OK(t *testing.T) {
	m, code := statusWithVersion(t, "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v5 호환)", code)
	}
	if m["schema_version"] != "5" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=5/ok", m)
	}
}

func TestStatusIncludesFallbackField(t *testing.T) {

	res, code := statusWithMeta(t, map[string]string{"schema_version": "3", "read_fallback_calls": "3"})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if res["read_fallback_calls"] != float64(3) {
		t.Errorf("read_fallback_calls = %v, want 3", res["read_fallback_calls"])
	}
}

func TestStatusFallbackFieldNull(t *testing.T) {
	res, code := statusWithMeta(t, map[string]string{"schema_version": "3"})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if res["read_fallback_calls"] != nil {
		t.Errorf("미설정 키 = %v, want nil", res["read_fallback_calls"])
	}
}
