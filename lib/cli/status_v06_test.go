package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

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
