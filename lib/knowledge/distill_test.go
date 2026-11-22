// distill_test.go — the DistillRows selection query and FormatInsights
// markdown contract tests (v1.22.0 T6). Synthetic demo rows only (personal
// data forbidden). With SUPERLITE_REAL=1 + FLINT_TEST_DSN set, the
// selection/idempotence scenario re-runs against the real DB (the
// export_test seam convention).
package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// distillFixtureRows — synthetic cells in the SELECT column order
// (id, topic, summary, conditions, tags, recall score). Includes an
// unstamped active row, and (for the SQL-assertion test) the floor is
// applied by the database, not here.
func distillFixtureRows() [][]any {
	return [][]any{
		{int32(1), "demo-topic-a", "summary alpha", "when demo holds", ":demo:a:", int32(3)},
		{int32(2), "demo-topic-b", "summary beta", "", ":demo:", int32(1)},
	}
}

// RF1 — the selection SQL carries the full row contract: active single
// definition (status='active' AND invalid_at IS NULL), the idempotence
// filter (distilled_at IS NULL), the score floor (COALESCE(re.cnt,0) >=
// $1 — the derived aggregate), stable id ordering, and the floor rides
// as the single bound argument.
func TestDistillRowsSelectionContract(t *testing.T) {
	var gotArgs []any
	var gotSQL string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		gotSQL, gotArgs = sql, args
		return distillFixtureRows(), nil
	}}
	rows, err := DistillRows(context.Background(), st, 1.0)
	if err != nil {
		t.Fatalf("DistillRows err: %v", err)
	}
	for _, want := range []string{
		"status = 'active'",
		"invalid_at IS NULL",
		"distilled_at IS NULL",
		"COALESCE(re.cnt, 0) >= $1",
		"ORDER BY id",
	} {
		if !strings.Contains(gotSQL, want) {
			t.Errorf("selection SQL missing %q:\n%s", want, gotSQL)
		}
	}
	if len(gotArgs) != 1 || gotArgs[0] != 1.0 {
		t.Errorf("floor arg = %v, want [1.0]", gotArgs)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// RF1a — cell mapping: id/topic/summary/conditions/tags/score.
	if rows[0].ID != 1 || rows[0].Topic != "demo-topic-a" ||
		rows[0].Summary != "summary alpha" || rows[0].Conditions != "when demo holds" ||
		rows[0].RecallScore != 3 {
		t.Errorf("row[0] mapping mismatch: %+v", rows[0])
	}
	if len(rows[0].Tags) != 2 || rows[0].Tags[0] != "demo" || rows[0].Tags[1] != "a" {
		t.Errorf("row[0].Tags = %v, want [demo a]", rows[0].Tags)
	}
	if rows[1].RecallScore != 1 || len(rows[1].Tags) != 1 {
		t.Errorf("row[1] mapping mismatch: %+v", rows[1])
	}
}

// RF2 — FormatInsights: header (title, created, row count) + one section
// per row in the given (id-stable) order, each carrying id, topic,
// summary, conditions, tags, and recall_score.
func TestFormatInsightsFieldsAndOrder(t *testing.T) {
	rows := []DistillRow{
		{ID: 2, Topic: "demo-b", Summary: "beta body", Conditions: "", Tags: []string{"demo"}, RecallScore: 1},
		{ID: 1, Topic: "demo-a", Summary: "alpha body", Conditions: "when demo holds", Tags: []string{"demo", "a"}, RecallScore: 3},
	}
	// the caller sorts by id (the stable-order contract) — FormatInsights
	// preserves the input order verbatim.
	got := string(FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)))
	for _, want := range []string{
		"# flint distill insights",
		"created: 2026-11-23T09:41:00Z",
		"rows: 2",
		"## #2 demo-b",
		"## #1 demo-a",
		"recall_score: 3",
		"recall_score: 1",
		"conditions: when demo holds",
		"tags: demo|a",
		"alpha body",
		"beta body",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("insights markdown missing %q:\n%s", want, got)
		}
	}
	// preserved input order: the #2 section (input row 0) precedes #1.
	if strings.Index(got, "## #2") > strings.Index(got, "## #1") {
		t.Errorf("input order not preserved (id sections reordered):\n%s", got)
	}
}

// RF3 — sanitization: [flint] markers and control characters in every
// emitted string field (topic·summary·conditions·tags) are neutralized
// (render.Text — the output chokepoint).
func TestFormatInsightsSanitizes(t *testing.T) {
	rows := []DistillRow{{
		ID:          7,
		Topic:       "demo\x00top[flint]ic",
		Summary:     "bo[FLINT]dy\x1b",
		Conditions:  "when [flint] holds",
		Tags:        []string{"de[flint]mo"},
		RecallScore: 2,
	}}
	got := string(FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)))
	if strings.Contains(got, "[flint]") || strings.Contains(got, "[FLINT]") {
		t.Errorf("[flint] marker survived sanitization:\n%q", got)
	}
	if strings.ContainsAny(got, "\x00\x1b") {
		t.Errorf("control character survived sanitization:\n%q", got)
	}
	if !strings.Contains(got, "demotop ic") {
		t.Errorf("sanitized topic missing: %q", got)
	}
	if !strings.Contains(got, "de mo") {
		t.Errorf("sanitized tag missing: %q", got)
	}
}

// ── slow: real-DB re-run (SUPERLITE_REAL=1 + FLINT_TEST_DSN, synthetic
// data only, purge on entry/exit). ──

// RF4 — the floor, ordering, and idempotence against the real DB:
// below-floor rows excluded, output ordered by id, stamped rows never
// re-selected (re-run yields 0 rows).
func TestDistillRowsSlowRealDB(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "distill-t6-demo-"
	hotSynthCleanup(t, st, prefix)
	ctx := context.Background()

	// 3 synthetic active rows: recall counts 0, 1, 3 (score via
	// recall_events). Row C additionally carries conditions (set
	// post-insert — InsertBatch predates the v17 column).
	seeds := []struct {
		topic      string
		hash       string
		recalls    int
		conditions string
	}{
		{prefix + "a", "hash-d6a", 0, ""},
		{prefix + "b", "hash-d6b", 1, ""},
		{prefix + "c", "hash-d6c", 3, "when demo holds"},
	}
	recs := make([]map[string]any, len(seeds))
	for i, s := range seeds {
		recs[i] = distillRec(s.topic, s.hash)
	}
	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	byTopic := map[string]int{}
	for i, s := range seeds {
		byTopic[s.topic] = ids[i]
		for j := 0; j < s.recalls; j++ {
			if _, err := st.pool.Exec(ctx,
				`INSERT INTO recall_events (knowledge_id) VALUES ($1)`, ids[i]); err != nil {
				t.Fatalf("seed recall event: %v", err)
			}
		}
		if s.conditions != "" {
			if _, err := st.pool.Exec(ctx,
				`UPDATE knowledge SET conditions = $2 WHERE id = $1`, ids[i], s.conditions); err != nil {
				t.Fatalf("seed conditions: %v", err)
			}
		}
	}
	// floor 1.0 (the default): a (score 0) excluded, b·c selected in id order.
	rows, err := DistillRows(ctx, st, 1.0)
	if err != nil {
		t.Fatalf("DistillRows: %v", err)
	}
	var gotIDs []int
	for _, r := range rows {
		if strings.HasPrefix(r.Topic, prefix) {
			gotIDs = append(gotIDs, r.ID)
		}
	}
	if len(gotIDs) != 2 || gotIDs[0] != byTopic[prefix+"b"] || gotIDs[1] != byTopic[prefix+"c"] {
		t.Fatalf("floor/order mismatch: got %v, want [b=%d c=%d]", gotIDs, byTopic[prefix+"b"], byTopic[prefix+"c"])
	}
	// the conditions column round-trips into the selected row.
	for _, r := range rows {
		if r.ID == byTopic[prefix+"c"] && r.Conditions != "when demo holds" {
			t.Errorf("conditions round-trip: got %q", r.Conditions)
		}
	}
	// a higher floor (meta-override shape) narrows to c only.
	rows3, err := DistillRows(ctx, st, 3.0)
	if err != nil {
		t.Fatalf("DistillRows(3.0): %v", err)
	}
	for _, r := range rows3 {
		if strings.HasPrefix(r.Topic, prefix) && r.ID != byTopic[prefix+"c"] {
			t.Errorf("floor 3.0 leaked row %d", r.ID)
		}
	}
	// stamp the selected rows (the MarkDistilled semantics) — the re-run
	// must yield 0 unstamped rows above the floor.
	if _, err := st.pool.Exec(ctx,
		`UPDATE knowledge SET distilled_at = now()
		 WHERE id = ANY($1) AND distilled_at IS NULL AND status = 'active' AND invalid_at IS NULL`,
		gotIDs); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	again, err := DistillRows(ctx, st, 1.0)
	if err != nil {
		t.Fatalf("DistillRows re-run: %v", err)
	}
	for _, r := range again {
		if strings.HasPrefix(r.Topic, prefix) {
			t.Errorf("idempotence broken: stamped row %d re-selected", r.ID)
		}
	}
}

// distillRec — a synthetic active knowledge row for the distill slow
// test (the hotRec convention — only InsertBatch-known fields).
func distillRec(topic, hash string) map[string]any {
	return map[string]any{
		"kind":         "knowledge",
		"topic":        topic,
		"summary":      "demo distill summary " + hash,
		"tags":         ":t6demo:",
		"confidence":   0.9,
		"content_hash": hash,
		"status":       "active",
	}
}
