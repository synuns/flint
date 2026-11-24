package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func maintainRunStale(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainStale(ctx, r.ms, stdout, r.days, r.limit)
}

func maintainRunInvalidateStale(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainInvalidateStale(ctx, r.ms, stdout, r.days, r.limit, r.yes)
}

func maintainRunInvalidate(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	ms, id, reason, yes, idList := r.ms, r.id, r.reason, r.yes, r.idList
	if len(idList) > 0 {
		if !yes {
			fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate --ids requires --yes"))))
			return 1
		}
		return maintainInvalidateIDs(ctx, ms, stdout, idList)
	}
	if id == 0 || reason == "" {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --invalidate requires --id and --reason"))))
		return 1
	}

	chk, err := wire.QueryOnce(ctx, ms,
		`SELECT invalid_at FROM knowledge WHERE id = $1`, []any{id})
	if err != nil {
		return adminErrLine(stdout, err)
	}
	if len(chk) == 0 {
		return adminErrLine(stdout, fmt.Errorf("%w: invalidate: id=%d not found", db.ErrNotFound, id))
	}
	if chk[0][0] == nil {

		recallAuditDisposed(ctx, ms, []int{id})
		if _, err := ms.Invalidate(ctx, []int{id}, reason); err != nil {
			return adminErrLine(stdout, err)
		}
		if _, err := ms.Commit(ctx, fmt.Sprintf(
			"invalidate: id=%d reason: %s (invalidation path) [session:0]", id, reason)); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	fmt.Fprintf(stdout, `{"status":"invalidated","id":%d}`+"\n", id)
	return 0
}

func maintainRunExpirePending(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainExpirePending(ctx, r.ms, stdout, r.expireDays, r.commit)
}

func maintainRunPruneInvalidated(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainPruneInvalidated(ctx, r.ms, stdout, r.expireDays, r.commit)
}

type staleItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	NeverRead bool   `json:"never_read"`
	Reason    string `json:"reason"`
	StaleDays int    `json:"stale_days"`
}

func maintainStale(ctx context.Context, ms MaintStore, stdout io.Writer, daysOverride, limit int) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := staleReport(items, days)
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, out)
	return 0
}

func listStale(ctx context.Context, ms MaintStore, daysOverride, limit int) ([]staleItem, int, error) {
	th, err := wire.LoadThresholds(ctx, ms)
	if err != nil {
		return nil, 0, err
	}
	days := th.StaleDays
	if daysOverride > 0 {
		days = daysOverride
	}

	cutoff := db.DaysCutoff(days, time.Now())
	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT id, topic, last_read_at, ingested_at, read_count
		FROM knowledge
		WHERE status = 'active' AND invalid_at IS NULL
		  AND ((last_read_at IS NOT NULL AND last_read_at < $1)
		   OR (read_count = 0 AND ingested_at < $1))
		ORDER BY COALESCE(last_read_at, ingested_at) ASC
		LIMIT $2`, []any{cutoff, limit})
	if err != nil {
		return nil, days, err
	}
	items := []staleItem{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		readCount, _ := render.CellToInt(r[4])
		ref, ok := AnyToTime(r[2])
		if !ok || ref.IsZero() {
			ref, ok = AnyToTime(r[3])
		}
		if !ok || !ref.Before(cutoff) {
			continue
		}
		reason := "stale"
		if readCount == 0 {
			reason = "never_read"
		}
		items = append(items, staleItem{
			ID:        id,
			Topic:     render.Text(topic),
			NeverRead: readCount == 0,
			Reason:    reason,
			StaleDays: int(time.Since(ref).Hours() / 24),
		})
	}
	return items, days, nil
}

func staleReport(items []staleItem, days int) (string, error) {
	out, err := json.Marshal(struct {
		Status  string      `json:"status"`
		Days    int         `json:"days"`
		Count   int         `json:"count"`
		Items   []staleItem `json:"items"`
		Summary string      `json:"summary"`
	}{Status: "ok", Days: days, Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d to invalidate", len(items))})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func maintainInvalidateStale(ctx context.Context, ms MaintStore, stdout io.Writer,
	daysOverride, limit int, apply bool) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if !apply || len(items) == 0 {
		out, mErr := staleReport(items, days)
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, out)
		return 0
	}
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	code := invalidateIDs(ctx, ms, stdout, ids, "invalidate-stale")
	if code != 0 {
		return code
	}
	return 0
}

func maintainInvalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int) int {
	return invalidateIDs(ctx, ms, stdout, ids, "invalidate --ids")
}

func invalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int, op string) int {
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE id = ANY($1)`, []any{ids})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	found := map[int]bool{}
	for _, r := range rows {
		if id, ok := render.CellToInt(r[0]); ok {
			found[id] = true
		}
	}
	var missing []int
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: %s: nonexistent ids %v", op, missing)))
		return 1
	}

	recallAuditDisposed(ctx, ms, ids)
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Invalidate(ctx, ids, op); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"%s: invalidated %d rows (batch invalidation path) [session:0]", op, len(ids)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "invalidated", Count: len(ids), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

type expireItem struct {
	ID      int    `json:"id"`
	Topic   string `json:"topic"`
	Summary string `json:"summary"`
	AgeDays int    `json:"age_days"`
}

const expireListLimit = 20

func listExpiredPending(ctx context.Context, ms MaintStore, days int) ([]expireItem, time.Time, error) {

	cutoff := db.DaysCutoff(days, time.Now())

	rows, err := wire.QueryOnce(ctx, ms, `
		SELECT id, topic, summary, valid_at
		FROM knowledge
		WHERE status = 'pending' AND invalid_at IS NULL AND valid_at < $1
		ORDER BY valid_at ASC
		LIMIT $2`, []any{cutoff, expireListLimit})
	if err != nil {
		return nil, cutoff, err
	}
	items := []expireItem{}
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		summary, _ := r[2].(string)
		va, ok := AnyToTime(r[3])
		if !ok || !va.Before(cutoff) {
			continue
		}
		items = append(items, expireItem{
			ID:      id,
			Topic:   render.Text(topic),
			Summary: ConflictSummaryPrefix(render.Text(summary)),
			AgeDays: int(time.Since(va).Hours() / 24),
		})
	}
	return items, cutoff, nil
}

func maintainExpirePending(ctx context.Context, ms MaintStore, stdout io.Writer, days int, apply bool) int {
	items, _, err := listExpiredPending(ctx, ms, days)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if !apply || len(items) == 0 {
		out, mErr := json.Marshal(struct {
			Status  string       `json:"status"`
			Days    int          `json:"days"`
			Count   int          `json:"count"`
			Items   []expireItem `json:"items"`
			Summary string       `json:"summary"`
		}{Status: "expire-pending", Days: days, Count: len(items), Items: items,
			Summary: fmt.Sprintf("%d to invalidate", len(items))})
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}

	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Invalidate(ctx, ids, "expire-pending"); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"expire-pending: invalidated %d rows (pending expiry path) [session:0]", len(items)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	knowledge.BumpCurationCounter(ctx, ms, "maintain_expire", len(items))
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Days   int    `json:"days"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "invalidated", Days: days, Count: len(items), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
func maintainPruneInvalidated(ctx context.Context, ms MaintStore, stdout io.Writer, days int, commit bool) int {

	th := knowledge.WriteThresholds(ctx, ms)
	if days <= 0 {
		days = th.InvalidatedPruneDays
		if days <= 0 {
			days = 90
		}
	}

	retention := th.RecallEventRetentionD
	if retention <= 0 {
		retention = 90
	}
	eventCutoff := time.Now().UTC().AddDate(0, 0, -retention)
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := wire.QueryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE status = 'invalidated' AND invalid_at < $1 ORDER BY invalid_at`,
		[]any{cutoff})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if n, ok := render.CellToInt(r[0]); ok {
			ids = append(ids, n)
		}
	}

	expired := 0
	erows, err := wire.QueryOnce(ctx, ms,
		`SELECT count(*)::int FROM recall_events WHERE recalled_at < $1`,
		[]any{eventCutoff})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(erows) > 0 && len(erows[0]) > 0 {
		if n, ok := render.CellToInt(erows[0][0]); ok {
			expired = n
		}
	}
	if len(ids) == 0 && expired == 0 {
		fmt.Fprintln(stdout, `{"status":"prune-invalidated","deleted":0,"detail":"no targets"}`)
		return 0
	}
	if !commit {
		idStrs := make([]string, 0, len(ids))
		for _, id := range ids {
			idStrs = append(idStrs, strconv.Itoa(id))
		}
		list := strings.Join(idStrs, ",")
		if len(list) > 200 {
			list = list[:200] + "…"
		}
		fmt.Fprintf(stdout, `{"status":"dry-run","would_delete":%d,"older_than_days":%d,"ids":"%s","would_prune_recall_events":%d,"recall_retention_days":%d,"next":"run with --commit to apply"}`+"\n",
			len(ids), days, list, expired, retention)
		return 0
	}
	deleted := 0
	if len(ids) > 0 {
		if err := retryWrite(ctx, func() error {

			n, err := ms.PruneInvalidated(ctx, cutoff)
			if err != nil {
				return err
			}
			deleted = int(n)
			_, err = ms.Commit(ctx, fmt.Sprintf(
				"prune-invalidated: removed %d rows (%dd+ elapsed) (knowledge hygiene path) [session:0]", deleted, days))
			return err
		}); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}

		knowledge.BumpCurationCounter(ctx, ms, "maintain_prune_rows", deleted)
	}

	prunedEvents := 0
	if err := retryWrite(ctx, func() error {
		n, err := ms.PruneRecallEvents(ctx, eventCutoff)
		if err != nil {
			return err
		}
		prunedEvents = int(n)
		if prunedEvents == 0 {
			return nil
		}
		_, err = ms.Commit(ctx, fmt.Sprintf(
			"prune-recall-events: removed %d rows (%dd retention) (knowledge hygiene path) [session:0]", prunedEvents, retention))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	if tc, ok := ms.(knowledge.TelemetryCommitter); ok {
		_ = tc.CommitTelemetry(ctx, "prune-invalidated")
	}
	fmt.Fprintf(stdout, `{"status":"prune-invalidated","deleted":%d,"older_than_days":%d,"pruned_recall_events":%d,"recall_retention_days":%d}`+"\n",
		deleted, days, prunedEvents, retention)
	return 0
}
