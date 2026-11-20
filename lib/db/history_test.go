// history_test.go — LogEntries (the Doltgres implementation of the
// wire.LogQuery contract) slow real-DB regression. Seeds 3 commits
// (synthetic demo knowledge — 2 non-telemetry·1 telemetry) and measures
// the Hash·Prefix·BeforeOrAt·Earliest·LatestN (+MessageNotLike) modes.
// The ported queries preserve the originals (ported from asof.go·
// audit.go), so the column-filling scope also inherits them: Hash·
// LatestN fill 4 columns, Prefix·BeforeOrAt fill commit_hash only,
// Earliest fills date only.
package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// seedT3Commit — accrues one synthetic demo knowledge then makes a dolt
// commit with a marker message. Returns the commit hash (an empty hash
// = 0 rows staged).
func seedT3Commit(t *testing.T, r *Repo, marker string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t3-" + marker)}); err != nil {
		t.Fatalf("InsertBatch(%s): %v", marker, err)
	}
	// The tel marker commits with the 'telemetry: ' prefix — verifying
	// that the MessageNotLike exclusion actually matches (preventing a
	// vacuous assertion).
	msg := "flint v0180 t3 demo " + marker
	if marker == "tel" {
		msg = "telemetry: v0180 t3 demo tel"
	}
	h, err := r.Commit(ctx, msg)
	if err != nil {
		t.Fatalf("Commit(%s): %v", marker, err)
	}
	if h == "" {
		t.Fatalf("Commit(%s): 빈 해시(스테이징 0행)", marker)
	}
	return h
}

// TestLogEntriesSlowModes — measures the 5 modes. Waits 1.1s between
// commits to distinguish commit times (dolt_log dates truncate to
// seconds). The shared DB carries other tests' commits, but flock
// serialization makes this test's commits the newest, and the LatestN
// verification depends on the newest 2 non-telemetry commits being this
// test's.
func TestLogEntriesSlowModes(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	h1 := seedT3Commit(t, r, "one")
	time.Sleep(1100 * time.Millisecond)
	h2 := seedT3Commit(t, r, "two")
	time.Sleep(1100 * time.Millisecond)
	htel := seedT3Commit(t, r, "tel") // for verifying the LatestN default-filter escape (see msg above)

	// ── Hash exact match — 4 columns (hash·committer·date·message)
	// filled. ──
	e2, err := r.LogEntries(ctx, wire.LogQuery{Hash: h2})
	if err != nil {
		t.Fatalf("LogEntries(Hash): %v", err)
	}
	if len(e2) != 1 || e2[0].Hash != h2 {
		t.Fatalf("LogEntries(Hash) = %v, want 해시 %s 1행", e2, h2)
	}
	if e2[0].Committer == "" {
		t.Fatalf("LogEntries(Hash) committer 비어 있음: %+v", e2[0])
	}
	if e2[0].Message != "flint v0180 t3 demo two" {
		t.Fatalf("LogEntries(Hash) message = %q, want 커밋 메시지", e2[0].Message)
	}
	if e2[0].Date.IsZero() {
		t.Fatalf("LogEntries(Hash) date 제로값: %+v", e2[0])
	}
	e1, err := r.LogEntries(ctx, wire.LogQuery{Hash: h1})
	if err != nil {
		t.Fatalf("LogEntries(Hash h1): %v", err)
	}
	if len(e1) != 1 || e1[0].Date.IsZero() {
		t.Fatalf("LogEntries(Hash h1) = %v, want date 충원 1행", e1)
	}

	// ── Prefix — the full LIKE-match list (the uniqueness verdict is the
	// caller's). ──
	prefix := h2
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	ps, err := r.LogEntries(ctx, wire.LogQuery{Prefix: prefix})
	if err != nil {
		t.Fatalf("LogEntries(Prefix): %v", err)
	}
	found := false
	for _, e := range ps {
		if !strings.HasPrefix(e.Hash, prefix) {
			t.Fatalf("LogEntries(Prefix) 비접두 해시 %q (prefix %q)", e.Hash, prefix)
		}
		if e.Hash == h2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("LogEntries(Prefix %q) 목록에 %s 없음: %v", prefix, h2, ps)
	}
	zs, err := r.LogEntries(ctx, wire.LogQuery{Prefix: "qqqqqqv0180t3"})
	if err != nil {
		t.Fatalf("LogEntries(Prefix 미발견): %v", err)
	}
	if len(zs) != 0 {
		t.Fatalf("LogEntries(미발견 prefix) = %v, want 0행", zs)
	}

	// ── BeforeOrAt — the newest commit at or before the time, 1 row. ──
	bs, err := r.LogEntries(ctx, wire.LogQuery{BeforeOrAt: e2[0].Date})
	if err != nil {
		t.Fatalf("LogEntries(BeforeOrAt): %v", err)
	}
	if len(bs) != 1 || bs[0].Hash != h2 {
		t.Fatalf("LogEntries(BeforeOrAt) = %v, want h2 1행(tel 커밋은 1.1s 뒤라 제외)", bs)
	}

	// ── Earliest — the first commit time (no commit_hash column —
	// inheriting the original query). ──
	ee, err := r.LogEntries(ctx, wire.LogQuery{Earliest: true})
	if err != nil {
		t.Fatalf("LogEntries(Earliest): %v", err)
	}
	if len(ee) != 1 || ee[0].Date.IsZero() {
		t.Fatalf("LogEntries(Earliest) = %v, want date 1행", ee)
	}
	if ee[0].Date.After(e1[0].Date) {
		t.Fatalf("LogEntries(Earliest) date %v > 첫 데모 커밋 %v", ee[0].Date, e1[0].Date)
	}

	// ── LatestN + MessageNotLike — DESC·LIMIT·telemetry excluded. ──
	ls, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 2, MessageNotLike: "telemetry: %"})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+NotLike): %v", err)
	}
	if len(ls) != 2 || ls[0].Hash != h2 || ls[1].Hash != h1 {
		t.Fatalf("LogEntries(LatestN 2) = %v, want [h2, h1] DESC", ls)
	}
	for _, e := range ls {
		if strings.HasPrefix(e.Message, "telemetry: ") {
			t.Fatalf("LogEntries(LatestN) telemetry 커밋 누출: %+v", e)
		}
	}
	// LatestN without the filter — the newest commit is the telemetry
	// marker commit.
	l1, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 1})
	if err != nil {
		t.Fatalf("LogEntries(LatestN 필터 없음): %v", err)
	}
	if len(l1) != 1 || l1[0].Hash != htel || l1[0].Message != "telemetry: v0180 t3 demo tel" {
		t.Fatalf("LogEntries(LatestN 1 무필터) = %v, want tel 커밋", l1)
	}

	// ── LatestN + MessageLike — the inclusion filter alone (for the
	// audit --session transfer). ──
	ms, err := r.LogEntries(ctx, wire.LogQuery{LatestN: 10, MessageLike: "%v0180 t3 demo%"})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+Like): %v", err)
	}
	likeHas := map[string]bool{}
	for _, e := range ms {
		if !strings.Contains(e.Message, "v0180 t3 demo") {
			t.Fatalf("LogEntries(Like) 마커 외 메시지 누출: %+v", e)
		}
		likeHas[e.Hash] = true
	}
	if !likeHas[htel] || !likeHas[h1] || !likeHas[h2] {
		t.Fatalf("LogEntries(Like) 데모 커밋 3종 미포함: %v", ms)
	}

	// ── LatestN + MessageNotLike + MessageLike — AND-joined (telemetry
	// excluded + inclusion). ──
	cs, err := r.LogEntries(ctx, wire.LogQuery{
		LatestN:        10,
		MessageNotLike: "telemetry: %",
		MessageLike:    "%v0180 t3 demo%",
	})
	if err != nil {
		t.Fatalf("LogEntries(LatestN+NotLike+Like): %v", err)
	}
	combHas := map[string]bool{}
	for _, e := range cs {
		if strings.HasPrefix(e.Message, "telemetry: ") {
			t.Fatalf("LogEntries(결합) telemetry 커밋 누출: %+v", e)
		}
		if !strings.Contains(e.Message, "v0180 t3 demo") {
			t.Fatalf("LogEntries(결합) 마커 외 메시지 누출: %+v", e)
		}
		combHas[e.Hash] = true
	}
	if !combHas[h1] || !combHas[h2] || combHas[htel] {
		t.Fatalf("LogEntries(결합) = h1·h2만 포함해야 함(htel 제외): %v", cs)
	}

	// ── Empty query — no mode specified is an error. ──
	if _, err := r.LogEntries(ctx, wire.LogQuery{}); err == nil {
		t.Fatal("LogEntries(빈 질의) 오류 없음, want 오류")
	}
}
