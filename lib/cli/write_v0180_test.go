package cli

import (
	"context"
	"strings"
	"testing"
)

type dupMetaStore struct {
	*fakeStore
	meta DupMeta
}

func (d *dupMetaStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	return d.meta, d.meta.ID > 0, nil
}

func withDupMeta(t *testing.T, d *dupMetaStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

func TestWriteDupFeedback(t *testing.T) {
	f := &dupMetaStore{fakeStore: &fakeStore{}, meta: DupMeta{ID: 7, Topic: "doltgres 포트 충돌", State: "active"}}
	withDupMeta(t, f)

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

func TestWriteDupMetaAbsent(t *testing.T) {
	f := &fakeStore{exists: true}
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
