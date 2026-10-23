// status_v08_test.go — T85: status --as-of. 카운트 질의에 AS OF 접합·
// 출력 말미 as_of 필드·미지정 시 출력 불변·무효 스펙 exit 3 검증.
package cli

import (
	"reflect"
	"strings"
	"testing"
)

// statusAsOfHarness — fake Store에 as-of 경로를 세팅한다. hexPfx 해시
// 접두어는 단일 커밋 fullHash로 확정되고, pendingCount는 AS OF 접합된
// 카운트 질의에만 응답한다(비접합 질의는 curCount).
func statusAsOfHarness(t *testing.T, _, fullHash string, curCount, asofCount int64) *fakeStore {
	t.Helper()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM dolt_log") {
			if strings.Contains(sql, "LIKE") {
				return [][]any{{fullHash}}, nil
			}
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"4"}}, nil
		}
		if strings.Contains(sql, "AS OF") {
			return [][]any{{asofCount}}, nil
		}
		return [][]any{{curCount}}, nil
	}
	withFake(t, f)
	return f
}

// TestStatusAsOfCounts — --as-of 접두어 → 커밋 해시 정규화 → 카운트 질의에
// AS OF '<해시>' 접합, 출력에 as_of 필드(pending은 과거 스냅샷 수).
func TestStatusAsOfCounts(t *testing.T) {
	f := statusAsOfHarness(t, "abc", "abcdef0123456789", 5, 0)
	out, code := mustRun(t, []string{"status", "--as-of", "abc"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["as_of"] != "abcdef0123456789" {
		t.Fatalf("as_of=%v, want abcdef0123456789", m["as_of"])
	}
	if m["pending"] != float64(0) {
		t.Fatalf("pending=%v, want 0(과거 스냅샷)", m["pending"])
	}
	// 카운트 질의가 정규화된 해시로 AS OF 접합됐는지 SQL로 확인.
	spliced := false
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			if !strings.Contains(q, "AS OF 'abcdef0123456789'") {
				t.Fatalf("knowledge 질의에 AS OF 미접합: %s", q)
			}
			if !strings.Contains(q, "status = 'pending'") {
				t.Fatalf("기존 pending 조건 유실: %s", q)
			}
			spliced = true
		}
	}
	if !spliced {
		t.Fatalf("knowledge 카운트 질의 미실행: %v", f.queries)
	}
	// schema_version은 현재 메타 그대로(as-of 대상 아님).
	if m["schema_version"] != "4" {
		t.Fatalf("schema_version=%v, want 4(현재 메타)", m["schema_version"])
	}
}

// TestStatusAsOfFieldOrder — statusOutput 자체는 불변(신규 필드는 별도
// 구조체 말미). as_of 지정 출력의 키는 기존 10키 뒤에 as_of.
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
	// 키 순서는 직렬화 결과로 확인한다(구조체 임베딩 순서).
	if !strings.HasPrefix(strings.TrimSpace(out), `{"status":`) {
		t.Fatalf("첫 키 불변 위반: %s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), `,"as_of":"abcdef0123456789"}`) {
		t.Fatalf("as_of가 말미 아님: %s", out)
	}
	// 미지정 시 as_of 키 부재(출력 불변).
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

// TestStatusAsOfInvalidExit3 — 무효 스펙(형식 오류)은 ErrInvalidAsOf →
// exit 3, 한 줄 JSON 에러.
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

// TestStatusAsOfMissingValueExit3 — 값 없는 --as-of(마지막 인자)도 무효
// 스펙으로 exit 3(조용한 무시 금지).
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
