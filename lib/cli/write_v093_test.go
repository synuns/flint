// write_v093_test.go — regression: expose the existing row's id in the single-line duplicate
// response. Only {"status":"duplicate","id":N} is added — the JSONL aggregate form, exit 0,
// and the bare output of an id-0 duplicate are unchanged.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func TestDuplicateSingleLineCarriesExistingID(t *testing.T) {
	if got := store.OutputJSON("duplicate", "", "", 109); got != `{"status":"duplicate","id":109}` {
		t.Errorf("duplicate id 노출 실패: %s", got)
	}
}

func TestDuplicateZeroIDStaysBare(t *testing.T) {
	if got := store.OutputJSON("duplicate", "", "", 0); got != `{"status":"duplicate"}` {
		t.Errorf("id 0 duplicate 출력 변형: %s", got)
	}
}

// dupIDStore — a fake returning the existing row's id via DupIDByHash (the resend scenario).
type dupIDStore struct {
	*fakeStore
	id int
}

func (d *dupIDStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return d.id, true, nil
}

// Query — multi-line pre-dedup moved from per-line capability to a batch lookup
// (content_hash = ANY($1), knowledge.lookupHashBatch).
// This fake also answers the same existence check (the existing row's id) via the Query seam, keeping the multi-line
// duplicate scenario (the single-shot path is still DupIDByHash).
func (d *dupIDStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if strings.Contains(sql, "content_hash = ANY($1)") {
		if hashes, ok := args[0].([]string); ok {
			rows := make([][]any, 0, len(hashes))
			for _, h := range hashes {
				rows = append(rows, []any{h, d.id, "", ""})
			}
			return rows, nil
		}
	}
	return d.fakeStore.Query(ctx, sql, args...)
}

// withDupID — swaps the newStore seam to dupIDStore.
func withDupID(t *testing.T, d *dupIDStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteDuplicateIncludesID — a resend (single-line duplicate): the output carries the existing
// row's id, 0 commits, exit 0 (seeding idempotence kept).
func TestWriteDuplicateIncludesID(t *testing.T) {
	ds := &dupIDStore{fakeStore: &fakeStore{}, id: 109}
	withDupID(t, ds)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"dup-id","summary":"재전송","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate","id":109}` {
		t.Fatalf("출력=%q, want 기존 id 노출", out)
	}
	if len(ds.inserted) != 0 || len(ds.commits) != 0 {
		t.Fatalf("재전송은 0건 커밋: 삽입 %d·커밋 %d", len(ds.inserted), len(ds.commits))
	}
}

// TestWriteDuplicateMultiStaysBare — all-duplicate JSONL multi-line input stays a single bare
// duplicate line without ids (no mixing aggregate and single-line rules — R3 adds only the single line).
func TestWriteDuplicateMultiStaysBare(t *testing.T) {
	ds := &dupIDStore{fakeStore: &fakeStore{}, id: 109}
	withDupID(t, ds)
	jsonl := `{"topic":"dup-m1","summary":"a","confidence":0.9}` + "\n" +
		`{"topic":"dup-m2","summary":"b","confidence":0.9}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want id 없는 duplicate 한 줄", out)
	}
}
