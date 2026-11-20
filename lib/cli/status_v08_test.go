// status_v08_test.go — status --as-of. Past-snapshotting of the count queries
// (QueryAsOf hash and table splicing), the as_of field at the end of the output, unchanged output when unspecified,
// and invalid-spec exit 3 verification.
package cli

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// statusAsofFake — canned contracts for the status as-of path: hash prefix and commit time via
// a LogEntries override; past pending counts via a QueryAsOf override
// (AsOfQuery captured). Current counts and meta are inherited from queryFn.
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
		return []wire.LogEntry{{Hash: f.fullHash}}, nil // single commit resolved
	case q.Hash == f.fullHash:
		return []wire.LogEntry{{Hash: f.fullHash, Date: statusCommitDate}}, nil
	}
	return nil, nil
}

func (f *statusAsofFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	f.asofQueries = append(f.asofQueries, q)
	return [][]any{{f.asofCount}}, nil
}

// statusAsOfHarness — sets up the as-of path on a fake Store. The hexPfx hash prefix
// resolves to the single commit fullHash, and past counts answer only via QueryAsOf
// (current counts come from curCount).
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

// TestStatusAsOfCounts — --as-of prefix -> commit-hash normalization -> the count query spliced
// with AS OF '<hash>', and the as_of field in the output (pending is the past-snapshot count).
func TestStatusAsOfCounts(t *testing.T) {
	f := statusAsOfHarness(t, "abc", "abcdef0123456789", 5, 0)
	out, code := mustRun(t, []string{"status", "--as-of", "abc"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	// as_of is the same {hash,commit_date} object as read --as-of (consistent with the e2e asOfHash).
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
	// the past pending count query spliced with the normalized hash and table, confirmed via contract capture.
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
	// schema_version stays as current meta (not an as-of target).
	if m["schema_version"] != "4" {
		t.Fatalf("schema_version=%v, want 4(현재 메타)", m["schema_version"])
	}
}

// TestStatusAsOfFieldOrder — statusOutput itself is unchanged (new fields go at the end of a separate
// struct). With --as-of, the output keys are the existing 10 keys followed by as_of.
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
	// key order is checked via the serialized result (struct embedding order).
	if !strings.HasPrefix(strings.TrimSpace(out), `{"status":`) {
		t.Fatalf("첫 키 불변 위반: %s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out),
		`,"as_of":{"hash":"abcdef0123456789","commit_date":"2026-10-20T09:30:00Z"}}`) {
		t.Fatalf("as_of가 말미 아님(객체 형태): %s", out)
	}
	// when unspecified, the as_of key is absent (output unchanged).
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

// TestStatusAsOfInvalidExit3 — an invalid spec (bad format) is ErrInvalidAsOf ->
// exit 3, one-line JSON error.
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

// TestStatusAsOfMissingValueExit3 — a --as-of with no value (the last argument) is also an
// invalid-spec exit 3 (no silent ignoring).
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

// TestStatusSchemaV5OK — on a v5 DB (EnsureSchemaV5 superseded_by promotion),
// status exits 0 and reports the version as-is. Regression of the defect where the allowlist
// hardcoded "1"~"4" and rejected v5 (fresh Doltgres schema error exit 3).
func TestStatusSchemaV5OK(t *testing.T) {
	m, code := statusWithVersion(t, "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v5 호환)", code)
	}
	if m["schema_version"] != "5" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=5/ok", m)
	}
}
