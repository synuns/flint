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
		if strings.Contains(sql, "knowledge_reads") {
			return nil, nil // the survived merge lookup (v1.24.0 T2) — no repeated sessions
		}
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
// summary, conditions, tags, recall_score, and (v1.24.0 T2 — the
// survived minimum fulfillment) the survived_sessions count.
func TestFormatInsightsFieldsAndOrder(t *testing.T) {
	rows := []DistillRow{
		{ID: 2, Topic: "demo-b", Summary: "beta body", Conditions: "", Tags: []string{"demo"}, RecallScore: 1},
		{ID: 1, Topic: "demo-a", Summary: "alpha body", Conditions: "when demo holds", Tags: []string{"demo", "a"}, RecallScore: 3},
	}
	// the caller sorts by id (the stable-order contract) — FormatInsights
	// preserves the input order verbatim. maxRunes 0 disables the cap
	// (every row fits).
	got, emitted := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 0)
	if emitted != 2 {
		t.Fatalf("emitted = %d, want 2 (uncapped)", emitted)
	}
	s := string(got)
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
		if !strings.Contains(s, want) {
			t.Errorf("insights markdown missing %q:\n%s", want, s)
		}
	}
	// preserved input order: the #2 section (input row 0) precedes #1.
	if strings.Index(s, "## #2") > strings.Index(s, "## #1") {
		t.Errorf("input order not preserved (id sections reordered):\n%s", s)
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
	got, _ := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 0)
	s := string(got)
	if strings.Contains(s, "[flint]") || strings.Contains(s, "[FLINT]") {
		t.Errorf("[flint] marker survived sanitization:\n%q", s)
	}
	if strings.ContainsAny(s, "\x00\x1b") {
		t.Errorf("control character survived sanitization:\n%q", s)
	}
	if !strings.Contains(s, "demotop ic") {
		t.Errorf("sanitized topic missing: %q", s)
	}
	if !strings.Contains(s, "de mo") {
		t.Errorf("sanitized tag missing: %q", s)
	}
}

// RF3b — the v1.22.1 r97 file cap: rows are appended WHOLE while the
// total (header included) stays within maxRunes runes; the first row
// that would cross the cap and everything after it stay out, the
// returned emitted count names the rows in the file, and the header's
// rows: line matches it (the deferred remainder is the caller's to
// stamp-defer).
func TestFormatInsightsFileCap(t *testing.T) {
	mk := func(id int, body string) DistillRow {
		return DistillRow{ID: id, Topic: "demo-cap", Summary: body, Tags: []string{"demo"}, RecallScore: 1}
	}
	rows := []DistillRow{mk(1, "one"), mk(2, "two"), mk(3, "three"), mk(4, "four")}
	// measure one uncapped run to confirm all rows fit without a cap.
	_, all := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 0)
	if all != 4 {
		t.Fatalf("uncapped emitted = %d, want 4", all)
	}
	twoOnly, _ := FormatInsights(rows[:2], time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 0)
	cap2 := len([]rune(string(twoOnly)))
	got, emitted := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), cap2)
	if emitted != 2 {
		t.Fatalf("capped emitted = %d, want 2 (cap fits exactly rows 1-2)", emitted)
	}
	s := string(got)
	if !strings.Contains(s, "rows: 2") {
		t.Errorf("header rows line must name the emitted count:\n%s", s)
	}
	for _, want := range []string{"## #1 demo-cap", "## #2 demo-cap"} {
		if !strings.Contains(s, want) {
			t.Errorf("emitted section missing %q:\n%s", want, s)
		}
	}
	for _, absent := range []string{"## #3", "## #4"} {
		if strings.Contains(s, absent) {
			t.Errorf("deferred section leaked into the file: %q:\n%s", absent, s)
		}
	}
	if rn := len([]rune(s)); rn > cap2 {
		t.Errorf("file runes = %d, want <= cap %d", rn, cap2)
	}
	// a cap too small for even the first row: header-only file, 0
	// emitted (everything defers — the caller writes no stamps).
	tiny, emitted0 := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 10)
	if emitted0 != 0 || !strings.Contains(string(tiny), "rows: 0") || strings.Contains(string(tiny), "## #") {
		t.Fatalf("tiny cap: emitted=%d file=%q, want header-only rows: 0", emitted0, string(tiny))
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
	// Warm-up listing before the reads are seeded (the value is not
	// consumed yet; the post-seed listing below is the assertion target).
	if _, err := DistillRows(ctx, st, 1.0); err != nil {
		t.Fatalf("DistillRows warm-up: %v", err)
	}
	// v1.24.0 T2 — the survived merge on the real path: row b gets 2
	// knowledge_reads in one session (one survived point); its listing
	// row carries survived_sessions 1 (c stays 0). The reads are cleaned
	// up front (an interrupted prior run left none behind).
	bID := byTopic[prefix+"b"]
	if _, err := st.pool.Exec(ctx,
		`DELETE FROM knowledge_reads WHERE knowledge_id = ANY($1)`, ids); err != nil {
		t.Fatalf("reads 선행 정리: %v", err)
	}
	for j := 0; j < 2; j++ {
		if _, err := st.pool.Exec(ctx,
			`INSERT INTO knowledge_reads (knowledge_id, session, kind) VALUES ($1, 'v124-t2-demo-sess', 'read')`, bID); err != nil {
			t.Fatalf("seed knowledge_reads: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(),
			`DELETE FROM knowledge_reads WHERE knowledge_id = ANY($1)`, ids)
	})
	rows, err := DistillRows(ctx, st, 1.0)
	if err != nil {
		t.Fatalf("DistillRows(survived): %v", err)
	}
	for _, r := range rows {
		if r.ID == bID && r.SurvivedSessions != 1 {
			t.Fatalf("row b survived_sessions = %d, want 1(같은 세션 2회 회상)", r.SurvivedSessions)
		}
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

// RF5 — v1.24.0 T2 (the survived minimum fulfillment): DistillRows merges
// the SurvivedCounts signal (reused verbatim — the number of sessions
// with 2+ knowledge_reads of the row) into the listing; a lookup failure
// degrades quietly to 0 per row (§13 — a missing signal never breaks
// the distill run); FormatInsights carries the count on the output row's
// meta line.
func TestDistillRowsSurvivedMergeFake(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "knowledge_reads") {
			// id 1 has 2 survived sessions; id 2 absent (0).
			return [][]any{{int32(1), int32(2)}}, nil
		}
		return distillFixtureRows(), nil
	}}
	rows, err := DistillRows(context.Background(), st, 1.0)
	if err != nil {
		t.Fatalf("DistillRows err: %v", err)
	}
	if rows[0].SurvivedSessions != 2 {
		t.Fatalf("row[0] survived_sessions = %d, want 2", rows[0].SurvivedSessions)
	}
	if rows[1].SurvivedSessions != 0 {
		t.Fatalf("row[1] survived_sessions = %d, want 0(부재=0)", rows[1].SurvivedSessions)
	}
	// a lookup failure degrades quietly — the rows still come back.
	fail := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "knowledge_reads") {
			return nil, context.DeadlineExceeded
		}
		return distillFixtureRows(), nil
	}}
	rows, err = DistillRows(context.Background(), fail, 1.0)
	if err != nil || len(rows) != 2 || rows[0].SurvivedSessions != 0 {
		t.Fatalf("조용한 열화 위반: err=%v rows=%+v", err, rows)
	}
}

// TestFormatInsightsSurvivedLine — the output row's meta line carries
// the survived_sessions count after conditions (the v1.22.0 line shape
// stays a prefix — the field appends).
func TestFormatInsightsSurvivedLine(t *testing.T) {
	rows := []DistillRow{{ID: 3, Topic: "demo-c", Summary: "body", Tags: []string{"demo"},
		RecallScore: 2, SurvivedSessions: 4}}
	got, emitted := FormatInsights(rows, time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC), 0)
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1", emitted)
	}
	s := string(got)
	if !strings.Contains(s, "survived_sessions: 4") {
		t.Fatalf("meta 줄에 survived_sessions 부재:\n%s", s)
	}
	if i := strings.Index(s, "conditions:"); i < 0 || strings.Index(s, "survived_sessions:") < i {
		t.Fatalf("survived_sessions가 conditions 뒤에 와야 함:\n%s", s)
	}
}
