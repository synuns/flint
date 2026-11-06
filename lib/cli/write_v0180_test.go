// write_v0180_test.go — v0.18.0 R1: duplicate 응답 피드백. 단일 줄 재전송에서
// 기존 행 topic·state를 additive 노출해 라이터가 read/supersede/approve로
// 전환하게 한다(Anthropic already-exists 패턴의 규칙 기반 합성).
package cli

import (
	"context"
	"strings"
	"testing"
)

// dupMetaStore — DupMetaStore 확장 fake: 해시와 무관하게 고정 행 메타 반환.
type dupMetaStore struct {
	*fakeStore
	meta DupMeta
}

func (d *dupMetaStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	return d.meta, d.meta.ID > 0, nil
}

// withDupMeta — newStore seam을 dupMetaStore로 교체(write_v093 withDupID 패턴).
func withDupMeta(t *testing.T, d *dupMetaStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

// TestWriteDupFeedback — 단일 줄 재전송: {"status":"duplicate","id":7,
// "topic":...,"state":"active"} — 키는 additive, 기존 status·id는 불변.
func TestWriteDupFeedback(t *testing.T) {
	f := &dupMetaStore{fakeStore: &fakeStore{}, meta: DupMeta{ID: 7, Topic: "doltgres 포트 충돌", State: "active"}}
	withDupMeta(t, f)
	// fakeStore는 삽입 전 dedup 경로만 탄다(해시 존재) — payload는 최소 스키마.
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

// TestWriteDupMetaAbsent — 미구현 Store(레거시 fake) 폴백: 메타 없이
// {"status":"duplicate"} 바이트 불변(하위호환 — v06 exact-match 계약).
func TestWriteDupMetaAbsent(t *testing.T) {
	f := &fakeStore{exists: true} // ExistsHash 폴백 경로(해시 존재)
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
