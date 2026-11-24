package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func maintainRunSupersede(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ms, old, by := r.ms, r.old, r.by
	if old == 0 || by == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --supersede requires OLD and --by NEW"))))
		return 1
	}
	return maintainSupersede(ctx, ms, stdout, old, by)
}

func maintainRunDecayAudit(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainDecayAudit(ctx, r.ms, stdout, r.limit)
}
func maintainSupersede(ctx context.Context, ms MaintStore, stdout io.Writer, oldID, newID int) int {
	if oldID == newID {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede OLD and --by NEW are the same (id=%d)", oldID)))
		return 3
	}
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT (SELECT status FROM knowledge WHERE id = $1),
		       (SELECT valid_at FROM knowledge WHERE id = $2)`,
		[]any{oldID, newID})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	var oldStatus string
	var newValidAt any
	if len(rows) > 0 && len(rows[0]) >= 2 {
		oldStatus, _ = rows[0][0].(string)
		newValidAt = rows[0][1]
	}
	if newValidAt == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: NEW id=%d not found", newID)))
		return 1
	}
	if oldStatus != "active" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: id=%d is not active (status %q)", oldID, oldStatus)))
		return 1
	}
	at, ok := AnyToTime(newValidAt)
	if !ok {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --supersede: cannot read NEW valid_at id=%d", newID)))
		return 1
	}

	cycRows, err := wire.QueryOnce(ctx, ms, db.SupersedeCycleSQL,
		[]any{oldID, newID, db.SupersedeCycleDepthLimit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(cycRows) > 0 {
		if cyc, _ := cycRows[0][0].(bool); cyc {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
				"maintain: --supersede: id=%d -> id=%d would create a supersede cycle", oldID, newID)))
			return 1
		}
		if truncated, _ := cycRows[0][1].(bool); truncated {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
				"maintain: --supersede: id=%d -> id=%d lineage walk hit the depth cap (%d) — refusing conservatively",
				oldID, newID, db.SupersedeCycleDepthLimit)))
			return 1
		}
	}

	recallAuditDisposed(ctx, ms, []int{oldID})
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Supersede(ctx, oldID, newID, at); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"supersede: old=%d by=%d (supersession path) [session:0]", oldID, newID))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	knowledge.BumpCurationCounter(ctx, ms, "maintain_supersede", 1)
	fmt.Fprintf(stdout, `{"status":"superseded","old":%d,"superseded_by":%d}`+"\n",
		oldID, newID)
	return 0
}

var decayNow = time.Now

type decayAuditItem struct {
	ID           int    `json:"id"`
	Score        int    `json:"score"`
	LastRecalled string `json:"last_recalled"`
	Effective    int    `json:"effective"`
	Recommend    string `json:"recommend"`
}

func maintainDecayAudit(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	th := store.DefaultThresholds()
	if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
		th = loaded
	}
	if th.DecayRate <= 0 {
		th.DecayRate = store.DefaultThresholds().DecayRate
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT k.id, re.cnt, re.last_at, k.tags
		FROM knowledge k
		LEFT JOIN (`+knowledge.RecallAggregateSQL+`) re ON re.knowledge_id = k.id
		WHERE k.status = 'active' AND k.invalid_at IS NULL AND re.last_at IS NOT NULL
		ORDER BY re.last_at ASC
		LIMIT $1`, []any{limit})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	now := decayNow()

	var survived map[int]int
	if th.SurvivedDecayRelief > 0 {
		ids := make([]int, 0, len(rows))
		for _, r := range rows {
			if len(r) < 4 {
				continue
			}
			if id, ok := render.CellToInt(r[0]); ok {
				ids = append(ids, id)
			}
		}
		survived, _ = knowledge.SurvivedCounts(ctx, ms, ids)
	}

	knowledge.SnapshotSurvivedSessions(ctx, ms, survived)
	items := []decayAuditItem{}
	for _, r := range rows {

		if len(r) < 4 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		score, _ := render.CellToInt(r[1])
		last, ok := AnyToTime(r[2])
		if !ok || last.IsZero() {
			continue
		}
		tags, _ := r[3].(string)
		eff := knowledge.EffectiveScoreFor(score, last, now, th.DecayRate, tags,
			survived[id], th.SurvivedDecayRelief)
		if eff >= score {
			continue
		}
		rec := ""
		if th.ArchiveBelow > 0 && float64(eff) < th.ArchiveBelow {
			rec = "archive"
		}
		items = append(items, decayAuditItem{
			ID:           id,
			Score:        score,
			LastRecalled: last.Format(time.RFC3339),
			Effective:    eff,
			Recommend:    rec,
		})
		if len(items) >= limit {
			break
		}
	}
	out, mErr := json.Marshal(struct {
		Status       string           `json:"status"`
		DecayRate    float64          `json:"decay_rate"`
		ArchiveBelow float64          `json:"archive_below"`
		Count        int              `json:"count"`
		Items        []decayAuditItem `json:"items"`
		Summary      string           `json:"summary"`
	}{Status: "decay-audit", DecayRate: th.DecayRate, ArchiveBelow: th.ArchiveBelow,
		Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d decayed — apply via maintain --invalidate (approval required)", len(items))})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
