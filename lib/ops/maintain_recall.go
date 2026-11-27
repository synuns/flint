package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type ReaffirmStore interface {
	Reaffirm(ctx context.Context, ids []int) (int64, error)
}

type RecheckConditionsStore interface {
	SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error)
}

func maintainRunRecallAudit(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainRecallAudit(ctx, r.ms, stdout, r.limit)
}

func maintainRunAgeAudit(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainAgeAudit(ctx, r.ms, stdout, r.limit)
}

func maintainRunReaffirm(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainReaffirm(ctx, r.ms, stdout, r.id, r.idList, r.yes)
}

func maintainRunRecheck(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ms, recheckID, conditionsVal, conditionsGiven, yes := r.ms, r.recheckID, r.conditionsVal, r.conditionsGiven, r.yes
	if recheckID == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --recheck requires a knowledge id"))))
		return 1
	}
	if conditionsGiven {
		return maintainRecheckConditions(ctx, ms, stdout, recheckID, conditionsVal, yes)
	}
	return maintainRecheck(ctx, ms, stdout, recheckID)
}

const recallAuditBadSQL = `(SELECT count(*) FROM knowledge b
			 WHERE b.topic = k.topic AND b.id <> k.id
			   AND (b.invalid_at IS NOT NULL OR b.superseded_by IS NOT NULL)
			   AND EXISTS (SELECT 1 FROM recall_events rb WHERE rb.knowledge_id = b.id))`

type recallAuditItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	BadRecall int    `json:"bad_recall"`
	Recommend string `json:"recommend"`
}

func recallAuditRecommend(bad, selfRecalls, floor int) string {
	if bad < floor {
		return "hold"
	}
	if selfRecalls > 0 {
		return "supersede"
	}
	return "expire"
}

func maintainRecallAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {
	minBad := store.DefaultThresholds().RecallAuditMinBad
	minRec := store.DefaultThresholds().RecallAuditRecommendMin
	if th, err := wire.LoadThresholds(ctx, ms); err == nil {
		if th.RecallAuditMinBad > 0 {
			minBad = th.RecallAuditMinBad
		}
		if th.RecallAuditRecommendMin > 0 {
			minRec = th.RecallAuditRecommendMin
		}
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, k.topic, `+recallAuditBadSQL+` AS bad_recall,
		       (SELECT count(*) FROM recall_events rk WHERE rk.knowledge_id = k.id) AS self_recalls
		FROM knowledge k
		WHERE k.status = 'active' AND k.invalid_at IS NULL
		  AND `+recallAuditBadSQL+` >= $1
		ORDER BY bad_recall DESC, k.id ASC
		LIMIT $2`, []any{minBad, limit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	items := []recallAuditItem{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		bad, _ := render.CellToInt(r[2])
		selfRecalls := 0
		if len(r) > 3 {
			selfRecalls, _ = render.CellToInt(r[3])
		}
		items = append(items, recallAuditItem{
			ID:        id,
			Topic:     clampAgeTopic(render.Text(topic)),
			BadRecall: bad,
			Recommend: recallAuditRecommend(bad, selfRecalls, minRec),
		})
	}
	if len(items) > limit {
		items = items[:limit]
	}

	knowledge.BumpCurationCounter(ctx, ms, "recall_audit_listed", len(items))
	out, mErr := json.Marshal(struct {
		Status  string            `json:"status"`
		MinBad  int               `json:"min_bad"`
		Count   int               `json:"count"`
		Items   []recallAuditItem `json:"items"`
		Summary string            `json:"summary"`
	}{Status: "recall-audit", MinBad: minBad, Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d bad-recall row(s) (min %d) — dispose via maintain --invalidate/--supersede/--reaffirm (approval required)",
			len(items), minBad)})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func recallAuditDisposed(ctx context.Context, ms wire.Store, ids []int) {
	if len(ids) == 0 {
		return
	}
	minBad := store.DefaultThresholds().RecallAuditMinBad
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.RecallAuditMinBad > 0 {
		minBad = th.RecallAuditMinBad
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, `+recallAuditBadSQL+` AS bad_recall
		FROM knowledge k
		WHERE k.id = ANY($1) AND k.status = 'active' AND k.invalid_at IS NULL`,
		[]any{ids})
	if err != nil {
		return
	}
	n := 0
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		if bad, ok := render.CellToInt(r[1]); ok && bad >= minBad {
			n++
		}
	}
	knowledge.BumpCurationCounter(ctx, ms, "recall_audit_disposed", n)
}

const (
	staleScoreWeightTTL  = 0.7
	staleScoreWeightCorr = 0.3
	staleCorrSat         = 3.0
)

func StaleScore(ttlDays, corrCount, ttlSatDays int) int {
	if ttlSatDays <= 0 {
		ttlSatDays = store.DefaultThresholds().StaleTTLDays
	}
	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	v := 100 * (staleScoreWeightTTL*clamp01(float64(ttlDays)/float64(ttlSatDays)) +
		staleScoreWeightCorr*clamp01(float64(corrCount)/staleCorrSat))
	return int(v + 0.5)
}

type staleScoredItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	Score     int    `json:"score"`
	TTLDays   int    `json:"ttl_days"`
	CorrCount int    `json:"correction_count"`
	BadRecall int    `json:"bad_recall"`
	Supersede int    `json:"supersede_count"`

	Tags string `json:"-"`
}

func listStaleScored(ctx context.Context, ms wire.Store, limit int) ([]staleScoredItem, error) {
	if limit <= 0 {
		limit = 20
	}

	ttlSat := store.DefaultThresholds().StaleTTLDays
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.StaleTTLDays > 0 {
		ttlSat = th.StaleTTLDays
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, k.topic, COALESCE(re.last_at, k.ingested_at),
		       (SELECT count(*) FROM knowledge c
		         WHERE c.topic = k.topic AND c.id <> k.id
		           AND strpos(c.tags, ':correction:') > 0),
		       (SELECT count(*) FROM knowledge b
		         WHERE b.topic = k.topic AND b.id <> k.id
		           AND (b.invalid_at IS NOT NULL OR b.superseded_by IS NOT NULL)
		           AND EXISTS (SELECT 1 FROM recall_events rb WHERE rb.knowledge_id = b.id)),
		       (SELECT count(*) FROM knowledge s
		         WHERE s.topic = k.topic AND s.id <> k.id AND s.superseded_by IS NOT NULL),
		       COALESCE(k.tags, '')
		FROM knowledge k
		LEFT JOIN (`+knowledge.RecallAggregateSQL+`) re ON re.knowledge_id = k.id
		WHERE k.status = 'active' AND k.invalid_at IS NULL
		  AND (k.tags IS NULL OR strpos(k.tags, ':vol:immutable:') = 0)
		ORDER BY COALESCE(re.last_at, k.ingested_at) ASC
		LIMIT $1`, []any{limit})
	if err != nil {
		return nil, err
	}
	now := decayNow()
	items := []staleScoredItem{}
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		ref, ok := AnyToTime(r[2])
		if !ok || ref.IsZero() {
			continue
		}
		corr, _ := render.CellToInt(r[3])
		bad, _ := render.CellToInt(r[4])
		sup, _ := render.CellToInt(r[5])
		tags, _ := r[6].(string)
		items = append(items, staleScoredItem{
			ID:        id,
			Topic:     render.Text(topic),
			TTLDays:   int(now.Sub(ref).Hours() / 24),
			CorrCount: corr,
			BadRecall: bad,
			Supersede: sup,
			Tags:      tags,
		})
	}
	for i := range items {
		items[i].Score = StaleScore(items[i].TTLDays, items[i].CorrCount, ttlSat)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	return items, nil
}

const ageAuditTopicMax = 40

type ageAuditItem struct {
	ID            int    `json:"id"`
	Topic         string `json:"topic"`
	Score         int    `json:"score"`
	TTLDays       int    `json:"ttl_days"`
	CorrCount     int    `json:"correction_count"`
	BadRecall     int    `json:"bad_recall"`
	SupersedeCnt  int    `json:"supersede_count"`
	OverThreshold bool   `json:"over_threshold"`

	SuggestedVol string `json:"suggested_vol,omitempty"`
	VolTag       string `json:"vol_tag,omitempty"`
}

func clampAgeTopic(s string) string {
	r := []rune(s)
	if len(r) <= ageAuditTopicMax {
		return s
	}
	return string(r[:ageAuditTopicMax]) + "…"
}

func maintainAgeAudit(ctx context.Context, ms wire.Store, stdout io.Writer, limit int) int {

	threshold := store.DefaultThresholds().StaleScoreThreshold
	if th, err := wire.LoadThresholds(ctx, ms); err == nil && th.StaleScoreThreshold > 0 {
		threshold = th.StaleScoreThreshold
	}
	items, err := listStaleScored(ctx, ms, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	srcChanged := vaultSourceChanged(ctx, ms)
	out := make([]ageAuditItem, 0, len(items))
	over := 0
	for _, it := range items {
		marked := it.Score >= threshold
		if marked {
			over++
		}

		sug, existing := "", ExistingVolTag(it.Tags)
		if existing == "" {
			sug = SuggestVolClass(it.Topic, it.Tags)
		}
		out = append(out, ageAuditItem{
			ID:            it.ID,
			Topic:         clampAgeTopic(it.Topic),
			Score:         it.Score,
			TTLDays:       it.TTLDays,
			CorrCount:     it.CorrCount,
			BadRecall:     it.BadRecall,
			SupersedeCnt:  it.Supersede,
			OverThreshold: marked,
			SuggestedVol:  sug,
			VolTag:        existing,
		})
	}
	summary := fmt.Sprintf("%d stale candidates (%d over threshold %d) — dispose via maintain --invalidate (approval required)",
		len(out), over, threshold)
	if srcChanged > 0 {
		summary += fmt.Sprintf(" · %d vault source changed (recheck advised)", srcChanged)
	}
	b, mErr := json.Marshal(struct {
		Status       string         `json:"status"`
		Threshold    int            `json:"threshold"`
		Count        int            `json:"count"`
		SourceChange int            `json:"source_changed"`
		Items        []ageAuditItem `json:"items"`
		Summary      string         `json:"summary"`
	}{Status: "age-audit", Threshold: threshold, Count: len(out), SourceChange: srcChanged,
		Items: out, Summary: summary})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func vaultSourceChanged(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM vault_files WHERE content_changed_at IS NOT NULL`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n, _ := render.CellToInt(rows[0][0])
	return n
}
func maintainReaffirm(ctx context.Context, ms MaintStore, stdout io.Writer, id int, idList []int, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm requires --yes (confirmation)"))))
		return 1
	}
	if len(idList) > 0 && id != 0 {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm takes --id or --ids, not both"))))
		return 1
	}
	ids := idList
	if len(ids) == 0 {
		if id == 0 {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --reaffirm requires --id or --ids"))))
			return 1
		}
		ids = []int{id}
	}
	rs, ok := ms.(ReaffirmStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id, status FROM knowledge WHERE id = ANY($1) AND invalid_at IS NULL`, []any{ids})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	active := map[int]bool{}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		if rid, ok := render.CellToInt(r[0]); ok {
			if status, _ := r[1].(string); status == "active" {
				active[rid] = true
			}
		}
	}
	var missing []int
	for _, i := range ids {
		if !active[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --reaffirm: nonexistent or not active ids %v", missing)))
		return 1
	}

	recallAuditDisposed(ctx, ms, ids)
	if err := retryWrite(ctx, func() error {
		n, err := rs.Reaffirm(ctx, ids)
		if err != nil {
			return err
		}
		if int(n) != len(ids) {
			return fmt.Errorf("reaffirm: updated %d of %d rows (race — retry)", n, len(ids))
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"reaffirm: %d rows valid_at reset (reaffirm path) [session:0]", len(ids)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "reaffirmed", Count: len(ids), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

var recheckVolDict = []string{"vol:immutable", "vol:volatile", "vol:conditional"}

func recheckVolTags(tags string) []string {
	var out []string
	for _, v := range recheckVolDict {
		if strings.Contains(tags, ":"+v+":") {
			out = append(out, v)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

type RecheckSourceClearStore interface {
	ClearVaultSourceChanged(ctx context.Context, path string) (int64, error)
}

func clearRecheckVaultSource(ctx context.Context, ms wire.Store, stdout io.Writer, id int) (int64, int) {
	cs, ok := ms.(RecheckSourceClearStore)
	if !ok {
		return 0, 0
	}
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT source_path FROM knowledge WHERE id = $1`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0, store.MapExit(err)
	}
	sourcePath := ""
	if len(rows) > 0 && len(rows[0]) > 0 {
		sourcePath, _ = rows[0][0].(string)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return 0, 0
	}
	n, err := cs.ClearVaultSourceChanged(ctx, sourcePath)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0, store.MapExit(err)
	}
	if n > 0 {
		if _, cerr := ms.Commit(ctx, fmt.Sprintf(
			"recheck: vault source changed cleared path=%s (%d file(s)) [session:0]",
			render.Text(sourcePath), n)); cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return 0, store.MapExit(cerr)
		}
	}
	return n, 0
}

func maintainRecheck(ctx context.Context, ms wire.Store, stdout io.Writer, id int) int {
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT topic, tags, conditions, valid_at, source_path FROM knowledge WHERE id = $1`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(rows) == 0 {
		return adminErrLine(stdout, fmt.Errorf("%w: recheck: id=%d not found", db.ErrNotFound, id))
	}
	cell := func(i int) any {
		if i < len(rows[0]) {
			return rows[0][i]
		}
		return nil
	}
	topic, _ := cell(0).(string)
	tags, _ := cell(1).(string)
	conditions, _ := cell(2).(string)
	empty := strings.TrimSpace(conditions) == ""
	volTags := recheckVolTags(tags)
	ageDays := 0
	validAt := ""
	if va, ok := AnyToTime(cell(3)); ok && !va.IsZero() {
		validAt = va.UTC().Format(time.RFC3339)
		ageDays = int(time.Since(va).Hours() / 24)
	}
	sourcePath, _ := cell(4).(string)

	cleared, code := clearRecheckVaultSource(ctx, ms, stdout, id)
	if code != 0 {
		return code
	}
	summary := fmt.Sprintf("recheck target id=%d — valid %dd ago", id, ageDays)
	if empty {
		summary += " · no conditions recorded (set via maintain --recheck --conditions)"
	} else {
		summary += " · conditions recorded — re-verify them against the source"
	}
	if cleared > 0 {
		summary += fmt.Sprintf(" · %d vault source change(s) cleared", cleared)
	}
	out, mErr := json.Marshal(struct {
		Status          string   `json:"status"`
		ID              int      `json:"id"`
		Topic           string   `json:"topic"`
		Conditions      string   `json:"conditions"`
		ConditionsEmpty bool     `json:"conditions_empty"`
		VolTags         []string `json:"vol_tags"`
		ValidAt         string   `json:"valid_at"`
		AgeDays         int      `json:"age_days"`
		SourcePath      string   `json:"source_path"`
		SourceCleared   int      `json:"source_cleared"`
		Summary         string   `json:"summary"`
	}{Status: "recheck", ID: id, Topic: clampAgeTopic(render.Text(topic)),
		Conditions: render.Text(conditions), ConditionsEmpty: empty, VolTags: volTags,
		ValidAt: validAt, AgeDays: ageDays, SourcePath: render.Text(sourcePath),
		SourceCleared: int(cleared), Summary: summary})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func maintainRecheckConditions(ctx context.Context, ms MaintStore, stdout io.Writer, id int, conditions string, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --recheck --conditions requires --yes (confirmation)"))))
		return 1
	}
	rs, ok := ms.(RecheckConditionsStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	chk, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`, []any{id})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(chk) == 0 {
		return adminErrLine(stdout, fmt.Errorf("%w: recheck: id=%d not found or not active", db.ErrNotFound, id))
	}
	if err := retryWrite(ctx, func() error {
		n, err := rs.SetRecheckConditions(ctx, id, conditions)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("recheck conditions: updated %d rows for id=%d (race — retry)", n, id)
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"recheck: conditions set id=%d (recheck conditions path) [session:0]", id))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	cleared, code := clearRecheckVaultSource(ctx, ms, stdout, id)
	if code != 0 {
		return code
	}
	shown := render.Text(conditions)
	out, mErr := json.Marshal(struct {
		Status        string `json:"status"`
		ID            int    `json:"id"`
		Conditions    string `json:"conditions"`
		SourceCleared int    `json:"source_cleared"`
	}{Status: "recheck_conditions", ID: id, Conditions: shown, SourceCleared: int(cleared)})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
