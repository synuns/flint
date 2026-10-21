// pattern_test.go — T36 패턴 탐지. 같은 topic의 성공 반복·실패→성공
// 전환을 순수 SQL 집계로 식별한다(failure 태그 = 실패 학습, CONTENT.md §4).
package db

import (
	"context"
	"strings"
	"testing"
)

// patRec는 테스트용 knowledge 레코드(고유 hash 필요).
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

// TestPatternRepeatSuccess — 동일 topic 성공(non-failure) 2건이면
// repeat-success 후보가 나오고, 증거에 레코드 id·summary가 담긴다.
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

// TestPatternFailThenSuccess — 같은 topic에서 failure 태그 기록 뒤 성공
// 기록이 오면 fail-then-success로 잡는다(실패가 성공보다 나중이면 제외).
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

	// 역순(성공 후 실패)은 후보가 아니다.
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

// TestPatternCapTwenty — 후보는 topic당 1건·전체 상한 20건이다.
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
