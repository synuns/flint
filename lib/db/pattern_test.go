// pattern_test.go — pattern detection. Identifies same-topic repeated
// successes·failure→success transitions with pure SQL aggregation (the
// failure tag = failure learning, CONTENT.md §4).
package db

import (
	"context"
	"strings"
	"testing"
)

// patRec is a knowledge record for tests (a unique hash required).
func patRec(topic, summary, tags, hash string) map[string]any {
	return map[string]any{
		"kind":         "knowledge",
		"topic":        topic,
		"summary":      summary,
		"tags":         tags,
		"confidence":   0.9,
		"content_hash": hash,
	}
}

// TestPatternRepeatSuccess — 2 successful (non-failure) records on the
// same topic yield a repeat-success candidate, with the record id·
// summary in the evidence.
func TestPatternRepeatSuccess(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{
		patRec("배포 절차", "docker compose로 배포한다", ":process:", "pat-rs-1"),
		patRec("배포 절차", "docker compose로 재배포한다", ":process:", "pat-rs-2"),
		patRec("다른 토픽", "한 건뿐이라 후보 아님", ":process:", "pat-rs-3"),
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	pats, err := r.PatternCandidates(ctx)
	if err != nil {
		t.Fatalf("PatternCandidates: %v", err)
	}
	if len(pats) != 1 {
		t.Fatalf("want 1 pattern, got %d (%+v)", len(pats), pats)
	}
	p := pats[0]
	if p.Topic != "배포 절차" || p.Kind != "repeat-success" {
		t.Fatalf("want 배포 절차/repeat-success, got %q/%q", p.Topic, p.Kind)
	}
	if len(p.Evidence) != 2 {
		t.Fatalf("want 2 evidence, got %d (%v)", len(p.Evidence), p.Evidence)
	}
	for _, ev := range p.Evidence {
		if !strings.Contains(ev, "docker compose") {
			t.Fatalf("evidence missing summary: %q", ev)
		}
		if !strings.Contains(ev, "id=") {
			t.Fatalf("evidence missing record id: %q", ev)
		}
	}
}

// TestPatternFailThenSuccess — when a failure-tagged record is followed
// by a successful record on the same topic, it is caught as
// fail-then-success (excluded when the failure is later than the
// success).
func TestPatternFailThenSuccess(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{
		patRec("충돌 재현", "TCP 프록시 차단으로 실패했다", ":failure:", "pat-fs-1"),
		patRec("충돌 재현", "TCP 프록시로 재현에 성공했다", ":env:", "pat-fs-2"),
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	pats, err := r.PatternCandidates(ctx)
	if err != nil {
		t.Fatalf("PatternCandidates: %v", err)
	}
	if len(pats) != 1 || pats[0].Kind != "fail-then-success" {
		t.Fatalf("want 1 fail-then-success, got %+v", pats)
	}
	if len(pats[0].Evidence) < 2 {
		t.Fatalf("want failure+success evidence, got %v", pats[0].Evidence)
	}

	// The reverse order (success then failure) is not a candidate.
	if _, err := r.pool.Exec(ctx, `DELETE FROM knowledge`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.InsertBatch(ctx, []map[string]any{
		patRec("역순 토픽", "먼저 성공", ":env:", "pat-fs-3"),
		patRec("역순 토픽", "나중에 실패 학습", ":failure:", "pat-fs-4"),
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	pats, err = r.PatternCandidates(ctx)
	if err != nil {
		t.Fatalf("PatternCandidates(reverse): %v", err)
	}
	if len(pats) != 0 {
		t.Fatalf("reverse order must not be a candidate, got %+v", pats)
	}
}

// TestPatternCapTwenty — candidates are 1 per topic·20 overall cap.
func TestPatternCapTwenty(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	recs := make([]map[string]any, 0, 50)
	for i := 0; i < 25; i++ {
		topic := "토픽-" + string(rune('a'+i))
		for j := 0; j < 2; j++ {
			recs = append(recs, patRec(topic, "성공 기록", ":process:",
				"pat-cap-"+string(rune('a'+i))+"-"+string(rune('0'+j))))
		}
	}
	if _, err := r.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	pats, err := r.PatternCandidates(ctx)
	if err != nil {
		t.Fatalf("PatternCandidates: %v", err)
	}
	if len(pats) != 20 {
		t.Fatalf("want cap 20, got %d", len(pats))
	}
	seen := map[string]bool{}
	for _, p := range pats {
		if seen[p.Topic] {
			t.Fatalf("duplicate topic %q in candidates", p.Topic)
		}
		seen[p.Topic] = true
	}
}
