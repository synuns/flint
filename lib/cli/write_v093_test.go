// write_v093_test.go — v0.9.3 회귀: 단일 줄 duplicate 응답에 기존 행 id
// 노출(R3). {"status":"duplicate","id":N} 추가만 — JSONL 집계형·exit 0·
// id 0 duplicate의 맨몸 출력은 불변.
package cli

import (
	"context"
	"strings"
	"testing"

	"flint/lib/store"
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

// dupIDStore — DupIDByHash로 기존 행 id를 돌려주는 fake(재전송 시나리오).
type dupIDStore struct {
	*fakeStore
	id int
}

func (d *dupIDStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return d.id, true, nil
}

// withDupID — newStore seam을 dupIDStore로 교체.
func withDupID(t *testing.T, d *dupIDStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteDuplicateIncludesID — 재전송(단일 줄 duplicate): 출력에 기존 행
// id가 붙고 0건 커밋·exit 0(시딩 멱등 유지).
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

// TestWriteDuplicateMultiStaysBare — JSONL 다중 줄 전부 duplicate는 id 없는
// 맨몸 duplicate 한 줄(집계형·단일 줄 규칙 혼합 금지 — R3는 단일 줄 추가만).
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
