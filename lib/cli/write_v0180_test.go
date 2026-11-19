// write_v0180_test.go — duplicate-response feedback. A single-line resend additively
// exposes the existing row's topic and state so the writer can switch to read/supersede/approve
// (a rules-based synthesis of the Anthropic already-exists pattern).
package cli

import (
	"context"
	"strings"
	"testing"
)

// dupMetaStore — a DupMetaStore-extension fake: returns a fixed row's meta regardless of hash.
type dupMetaStore struct {
	*fakeStore
	meta DupMeta
}

func (d *dupMetaStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	return d.meta, d.meta.ID > 0, nil
}

// withDupMeta — swaps the newStore seam to dupMetaStore (the write_v093 withDupID pattern).
func withDupMeta(t *testing.T, d *dupMetaStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteDupFeedback — a single-line resend: {"status":"duplicate","id":7,
// "topic":...,"state":"active"} — keys are additive; the existing status and id are unchanged.
func TestWriteDupFeedback(t *testing.T) {
	f := &dupMetaStore{fakeStore: &fakeStore{}, meta: DupMeta{ID: 7, Topic: "doltgres 포트 충돌", State: "active"}}
	withDupMeta(t, f)
	// fakeStore takes only the pre-insert dedup path (hash exists) — the payload is the minimal schema.
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"kind":"knowledge","topic":"doltgres 포트 충돌","summary":"5434 점유 시 이동","tags":["doltgres"],"confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "duplicate" {
		t.Fatalf("status=%v, want duplicate", m["status"])
	}
	if v, _ := m["id"].(float64); int(v) != 7 {
		t.Fatalf("id=%v, want 7", m["id"])
	}
	if m["topic"] != "doltgres 포트 충돌" {
		t.Fatalf("topic=%v, want 기존 행 주제", m["topic"])
	}
	if m["state"] != "active" {
		t.Fatalf("state=%v, want active", m["state"])
	}
}

// TestWriteDupMetaAbsent — an unimplemented Store (legacy fake) fallback: without meta,
// the {"status":"duplicate"} bytes are unchanged (backward compatibility — the v06 exact-match contract).
func TestWriteDupMetaAbsent(t *testing.T) {
	f := &fakeStore{exists: true} // the ExistsHash fallback path (hash exists)
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"kind":"knowledge","topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("out=%s, want bare duplicate(폴백 바이트 불변)", out)
	}
}
