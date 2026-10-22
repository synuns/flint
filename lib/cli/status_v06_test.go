// status_v06_test.go — T63: status 한 줄 JSON에 last_write_at·last_hot_at·
// last_read_at 3필드 추가 검증. 키 부재/파손은 null, 기존 필드·순서 불변.
package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// statusWithMeta — meta 키 맵(kv)으로 status 실행 후 디코딩된 출력과 exit 반환.
// 스키마 버전은 3(호환), pending은 0으로 고정하고 나머지 meta 조회는 키 맵 참조.
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

// TestStatusLastSeenFields — 3키 심기 → RFC3339 값 그대로 3필드 보고.
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

// TestStatusLastSeenMissingOrNull — 미설정·파손(비-RFC3339) 키는 null.
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

// TestStatusLastSeenFieldOrder — 기존 필드 회귀: 순서·값 불변, 신규 필드는 말미.
func TestStatusLastSeenFieldOrder(t *testing.T) {
	m, code := statusWithMeta(t, map[string]string{"schema_version": "3"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	want := []string{"status", "schema_version", "pending", "hot_calls",
		"hot_p95", "read_calls", "read_zero_pct", "last_write_at",
		"last_hot_at", "last_read_at"}
	// statusOutput 구조체 태그 순서를 직접 확인한다.
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
