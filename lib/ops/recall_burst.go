package ops

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type BurstItem struct {
	ID     int    `json:"id"`
	Topic  string `json:"topic"`
	Reason string `json:"reason"`
}

const (
	burstReasonStale  = "stale-condition"
	burstReasonVault  = "vault-changed"
	burstReasonBad    = "bad-recall"
	burstDefaultLimit = 5
	burstHoursPerDay  = 24.0
)

type burstCandidate struct {
	item BurstItem
	bad  int
	ref  time.Time
}

func cellBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case string:
		return t == "t" || t == "true" || t == "1"
	}
	return false
}

func RecallBurstItems(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
	if limit <= 0 {
		limit = burstDefaultLimit
	}
	ttlSat := store.DefaultThresholds().StaleTTLDays
	minBad := store.DefaultThresholds().RecallAuditMinBad
	if th, err := wire.LoadThresholds(ctx, st); err == nil {
		if th.StaleTTLDays > 0 {
			ttlSat = th.StaleTTLDays
		}
		if th.RecallAuditMinBad > 0 {
			minBad = th.RecallAuditMinBad
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT k.id, k.topic, `+recallAuditBadSQL+` AS bad_recall,
		       COALESCE(re.last_at, k.ingested_at) AS last_ref,
		       COALESCE(k.conditions, ''),
		       (vf.content_changed_at IS NOT NULL) AS src_changed
		FROM knowledge k
		LEFT JOIN (`+knowledge.RecallAggregateSQL+`) re ON re.knowledge_id = k.id
		LEFT JOIN vault_files vf ON vf.path = k.source_path AND k.source_path <> ''
		WHERE k.status = 'active' AND k.invalid_at IS NULL
		  AND (
		    (k.conditions <> ''
		     AND COALESCE(re.last_at, k.ingested_at) <= now() - interval '`+fmt.Sprint(ttlSat)+` days')
		    OR vf.content_changed_at IS NOT NULL
		    OR `+recallAuditBadSQL+` >= `+fmt.Sprint(minBad)+`
		  )
		ORDER BY bad_recall DESC, COALESCE(re.last_at, k.ingested_at) ASC
		LIMIT `+fmt.Sprint(limit), nil)
	if err != nil {
		return nil, err
	}
	now := decayNow()
	items := make([]burstCandidate, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		bad, _ := render.CellToInt(r[2])
		ref, _ := AnyToTime(r[3])
		cond, _ := r[4].(string)
		changed := cellBool(r[5])
		stale := !ref.IsZero() && now.Sub(ref).Hours() >= float64(ttlSat)*burstHoursPerDay
		if !(cond != "" && stale) && !changed && bad < minBad {
			continue
		}
		reason := burstReasonBad
		if changed {
			reason = burstReasonVault
		}
		if cond != "" && stale {
			reason = burstReasonStale
		}
		items = append(items, burstCandidate{
			item: BurstItem{ID: id, Topic: clampAgeTopic(render.Text(topic)), Reason: reason},
			bad:  bad, ref: ref,
		})
	}
	if len(items) > limit {
		items = items[:limit]
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].bad != items[j].bad {
			return items[i].bad > items[j].bad
		}
		return items[i].ref.Before(items[j].ref)
	})
	out := make([]BurstItem, 0, len(items))
	for _, it := range items {
		out = append(out, it.item)
	}
	return out, nil
}
