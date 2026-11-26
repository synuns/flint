package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type workStateStore interface {
	StaleOpenWorkStates(ctx context.Context, days int) []map[string]any
	ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error
}

func maintainRunStaleState(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ws, ok := any(r.ms).(workStateStore)
	if !ok {
		fmt.Fprintln(stdout, wire.ErrLine(errors.New("maintain: --stale-state: unsupported store")))
		return 1
	}
	return maintainStaleState(ctx, r.ms, ws, stdout, r.commit)
}

type staleStateItem struct {
	ID        int    `json:"id"`
	ProjectID int    `json:"project_id"`
	Task      string `json:"task"`
	UpdatedAt string `json:"updated_at"`
}

func maintainStaleState(ctx context.Context, ms MaintStore, ws workStateStore, stdout io.Writer, commit bool) int {
	th := store.DefaultThresholds()
	if loaded, lErr := wire.LoadThresholds(ctx, ms); lErr == nil {
		th = loaded
	}
	if th.StateStaleDays <= 0 {
		th.StateStaleDays = store.DefaultThresholds().StateStaleDays
	}
	rows := ws.StaleOpenWorkStates(ctx, th.StateStaleDays)
	items := []staleStateItem{}
	for _, m := range rows {
		id, ok := render.CellToInt(m["id"])
		if !ok {
			continue
		}
		proj, _ := render.CellToInt(m["project_id"])
		task, _ := m["task"].(string)
		updated := ""
		if t, ok := AnyToTime(m["updated_at"]); ok {
			updated = t.Format(time.RFC3339)
		}
		items = append(items, staleStateItem{ID: id, ProjectID: proj, Task: task, UpdatedAt: updated})
	}
	if !commit || len(items) == 0 {
		out, mErr := json.Marshal(struct {
			Status  string           `json:"status"`
			Days    int              `json:"days"`
			Count   int              `json:"count"`
			Items   []staleStateItem `json:"items"`
			Summary string           `json:"summary"`
		}{Status: "stale-state", Days: th.StateStaleDays, Count: len(items), Items: items,
			Summary: fmt.Sprintf("%d stale open — apply via maintain --stale-state --commit (approval required)", len(items))})
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	if err := retryWrite(ctx, func() error {
		for _, it := range items {
			if err := ws.ResolveWorkState(ctx, it.ID, 0); err != nil {
				return err
			}
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"stale-state: resolved %d rows (stale work state path) [session:0]", len(items)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status   string `json:"status"`
		Resolved int    `json:"resolved"`
		Summary  string `json:"summary"`
	}{Status: "stale-state", Resolved: len(items),
		Summary: fmt.Sprintf("%d stale open states resolved", len(items))})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
