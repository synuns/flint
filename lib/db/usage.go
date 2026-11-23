package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/lib/store"
)

type Stat struct {
	ID   int
	Uses int
	OK   int
	Rate float64
}

func (r *Repo) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_uses", id)); err != nil {
		return fmt.Errorf("log playbook use: uses: %w", err)
	}
	if ok {
		if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_ok", id)); err != nil {
			return fmt.Errorf("log playbook use: ok: %w", err)
		}
	}
	return nil
}

func bumpMeta(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = (meta.value::bigint + 1)::text`, key)
	return err
}

var usageRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second}

func (r *Repo) RecordUsage(ctx context.Context, kind, source string) error {
	return r.RecordUsageCount(ctx, kind, source, 1)
}

type MeteringKindGroup struct {
	Name  string
	Kinds []string
}

var MeteringKindRegistry = []MeteringKindGroup{
	{"usage", []string{"write", "hot", "read", "dup", "dup_hook", "dup_manual", "dup_mcp", "dup_correction", "dup_gap", "brief"}},
	{"curation", []string{"approve_done", "approve_not_found", "approve_supersede_err",
		"maintain_supersede", "maintain_expire", "maintain_prune_rows",
		"import_added", "import_skipped", "correction_approved", "correction_preference", "correction_fact",
		"gap_approved", "survived_sessions", "merge_audit_pairs",
		"recall_audit_listed", "recall_audit_disposed"}},
	{"bytes", []string{"read", "hot", "brief"}},
}

func MeteringKinds(group string) []string {
	for _, g := range MeteringKindRegistry {
		if g.Name == group {
			return append([]string(nil), g.Kinds...)
		}
	}
	return nil
}

func meteringKindAllowed(group, kind string) bool {
	for _, k := range MeteringKinds(group) {
		if k == kind {
			return true
		}
	}
	return false
}

func (r *Repo) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	if !meteringKindAllowed("usage", kind) {
		return fmt.Errorf("record usage: invalid kind %q", kind)
	}
	if count < 1 {
		return fmt.Errorf("record usage: invalid count %d", count)
	}

	if count > 1<<31 {
		return nil
	}
	delta := map[string]int{kind: count}
	if kind == "write" && source != "" {
		delta[source] = count
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {

		if !strings.HasPrefix(kind, "dup") {
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
				"last_"+kind+"_at", now); err != nil {
				return false, fmt.Errorf("record usage: last_%s_at: %w", kind, err)
			}
		}
		created := false
		for k, n := range delta {
			made, err := r.bumpDailyFlat(ctx, day, k, n)
			if err != nil {
				return false, fmt.Errorf("record usage: daily: %w", err)
			}
			created = created || made
		}
		return created, nil
	})
	return nil
}

func (r *Repo) RecordBytes(ctx context.Context, kind string, n int) error {

	if !meteringKindAllowed("bytes", kind) {
		return fmt.Errorf("record bytes: invalid kind %q", kind)
	}
	if n < 0 {
		return fmt.Errorf("record bytes: invalid n %d", n)
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {
		made, err := r.bumpDailyFlat(ctx, day, kind+"_bytes", n)
		if err != nil {
			return false, fmt.Errorf("record bytes: daily: %w", err)
		}
		return made, nil
	})
	return nil
}

type Querier interface {
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
}

func BumpDailyFlatKey(ctx context.Context, q Querier, kind, day string, n int) error {
	key := "daily:" + kind + ":" + day

	_, err := q.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value =
		   CASE WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + EXCLUDED.value::bigint)::text
		        ELSE EXCLUDED.value END`, key, strconv.Itoa(n))
	if err != nil {
		return fmt.Errorf("bump daily flat key: %w", err)
	}
	return nil
}

func StampKnowledgeKind(ctx context.Context, q Querier, ids []int, kind string) error {
	if len(ids) == 0 || kind == "" {
		return nil
	}
	_, err := q.Query(ctx,
		`UPDATE knowledge SET kind = $1 WHERE id = ANY($2)`, kind, ids)
	if err != nil {
		return fmt.Errorf("stamp knowledge kind: %w", err)
	}
	return nil
}

type EchoCandRow struct {
	CandidateID int
	Relation    string
	Score       float64
}

func InsertEchoCandidates(ctx context.Context, q Querier, echoID int, rows []EchoCandRow) error {
	if echoID <= 0 || len(rows) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO echo_candidates (candidate_id, echo_id, relation, score) VALUES ")
	args := make([]any, 0, len(rows)*4)
	for i, r := range rows {
		if i > 0 {
			sb.WriteString(",")
		}
		b := i * 4
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d)", b+1, b+2, b+3, b+4)
		args = append(args, r.CandidateID, echoID, r.Relation, r.Score)
	}
	if _, err := q.Query(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("insert echo candidates: %w", err)
	}
	return nil
}

type ReadEvent struct {
	Hits       int
	Zero       bool
	Near       bool
	Fallback   bool
	Session    string
	IDs        string
	FirstRank  int
	Bytes      int
	ZeroReason string
	Source     string
	NearScore  float64
}

func (r *Repo) RecordReadEvent(ctx context.Context, ev ReadEvent) error {
	score := clampNearScore(ev.NearScore)
	_, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank, bytes, zero_reason, source, near_score)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs, ev.FirstRank,
		ev.Bytes, ev.ZeroReason, ev.Source, score)
	if err != nil {
		return fmt.Errorf("record read event: %w", err)
	}
	r.maybeRollupWeekly(ctx)
	r.maybePruneAfterRead(ctx)
	r.maybeSnapshotThresholds(ctx)
	r.maybeShadowRound(ctx)
	return nil
}

func clampNearScore(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

const pruneReadGateKey = "last_events_prune_at"

func pruneGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return gateStampDue(t, now)
}

var pruneGateCheckedAt atomic.Int64

const pruneGateCacheTTL = time.Hour

func pruneGateProbe(now time.Time) bool {
	if last := pruneGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < pruneGateCacheTTL {
		return false
	}
	pruneGateCheckedAt.Store(now.Unix())
	return true
}

func (r *Repo) maybePruneAfterRead(ctx context.Context) {
	if !pruneGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, pruneReadGateKey).Scan(&last)
	if !pruneGateDue(last, time.Now().UTC()) {
		return
	}
	if err := r.PruneDaily(ctx); err != nil {
		return
	}
	if err := r.PruneReadEvents(ctx); err != nil {
		return
	}
	_, _ = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		pruneReadGateKey, time.Now().UTC().Format(time.RFC3339))
}

func (r *Repo) RecordSessionStart(ctx context.Context, session string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO session_starts (session) VALUES ($1)`, session)
	if err != nil {
		return fmt.Errorf("record session start: %w", err)
	}
	return nil
}

func (r *Repo) RecordHotEvent(ctx context.Context, ids string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO hot_events (ids) VALUES ($1)`, ids)
	if err != nil {
		return fmt.Errorf("record hot event: %w", err)
	}
	return nil
}

func (r *Repo) thresholdsForPrune(ctx context.Context, keys ...string) (store.Thresholds, error) {
	def := store.DefaultThresholds()
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key = ANY($1)`, keys)
	if err != nil {
		return def, nil
	}
	defer rows.Close()
	var kv [][2]any
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		kv = append(kv, [2]any{k, v})
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return def, nil
	}
	if th.EventsRetentionD == 0 {
		th.EventsRetentionD = def.EventsRetentionD
	}
	if th.HotRetentionD == 0 {
		th.HotRetentionD = def.HotRetentionD
	}
	if th.DailyRetentionD == 0 {
		th.DailyRetentionD = def.DailyRetentionD
	}
	if th.KreadsRetentionD == 0 {
		th.KreadsRetentionD = def.KreadsRetentionD
	}
	return th, nil
}

func (r *Repo) PruneReadEvents(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx,
		"events_retention_days", "hot_events_retention_days",
		"knowledge_reads_retention_days")
	retention := map[string]int{
		"read_events":     th.EventsRetentionD,
		"session_starts":  th.EventsRetentionD,
		"hot_events":      th.HotRetentionD,
		"knowledge_reads": th.KreadsRetentionD,
	}
	now := time.Now().UTC()
	def := store.DefaultThresholds()
	for table, days := range retention {
		cutoff := retentionCutoffDays(days, defForRetention(def, table), now)
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE ts < $1`, cutoff); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) recordWithRetry(ctx context.Context, fn func(ctx context.Context, day string) (created bool, err error)) {
	day := time.Now().UTC().Format("2006-01-02")
	created := false
	_ = Retry(ctx, func() error {
		made, err := fn(ctx, day)
		if err != nil {
			return err
		}
		created = created || made
		return nil
	}, usageRetryBackoff)
	if created {
		_ = r.PruneDaily(ctx)
		_ = r.PruneReadEvents(ctx)
	}
}

var bumpRetryBackoff = []time.Duration{0, 2 * time.Millisecond, 8 * time.Millisecond}

func retryBump40001(ctx context.Context, fn func() error) error {
	var last error
	for i, d := range bumpRetryBackoff {
		if i > 0 || d > 0 {
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			return err
		}
		last = err
	}
	return last
}

func (r *Repo) bumpDailyFlat(ctx context.Context, day, kind string, n int) (created bool, err error) {
	key := "daily:" + kind + ":" + day
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM meta WHERE key = $1)`, key).Scan(&created); err != nil {
		return false, fmt.Errorf("bump daily flat: exists: %w", err)
	}
	created = !created

	if err := retryBump40001(ctx, func() error {
		_, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value =
			   CASE WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + EXCLUDED.value::bigint)::text
			        ELSE EXCLUDED.value END`, key, strconv.Itoa(n))
		return err
	}); err != nil {
		return false, fmt.Errorf("bump daily flat: upsert: %w", err)
	}
	return created, nil
}

func (r *Repo) readDailyFlat(ctx context.Context, kind, day string) (int, error) {
	var val string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, "daily:"+kind+":"+day).Scan(&val)
	switch {
	case err == nil:
		n, perr := strconv.Atoi(val)
		if perr != nil {
			return 0, nil
		}
		return n, nil
	case errors.Is(err, pgx.ErrNoRows):
		var legacy string
		lerr := r.pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, "daily:"+day).Scan(&legacy)
		if lerr != nil {
			if errors.Is(lerr, pgx.ErrNoRows) {
				return 0, nil
			}
			return 0, fmt.Errorf("read daily flat: legacy: %w", lerr)
		}
		m := map[string]int{}
		if json.Unmarshal([]byte(legacy), &m) != nil || m == nil {
			return 0, nil
		}
		return m[kind], nil
	default:
		return 0, fmt.Errorf("read daily flat: %w", err)
	}
}

const pruneDailyKeyPattern = `^daily:([a-z0-9_]+:)*[0-9]{4}-[0-9]{2}-[0-9]{2}$`

func (r *Repo) PruneDaily(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "daily_retention_days")
	days := th.DailyRetentionD

	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	_, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key ~ '`+pruneDailyKeyPattern+`'
		   AND substring(key from '[0-9]{4}-[0-9]{2}-[0-9]{2}$')::date <= $1::date`, cutoff)
	return err
}

func (r *Repo) PlaybookStats(ctx context.Context) ([]Stat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key LIKE 'pb\_%'`)
	if err != nil {
		return nil, fmt.Errorf("playbook stats: query: %w", err)
	}
	defer rows.Close()
	byID := map[int]*Stat{}
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return nil, fmt.Errorf("playbook stats: scan: %w", err)
		}
		idStr := strings.TrimPrefix(key, "pb_")
		isOK := false
		switch {
		case strings.HasSuffix(idStr, "_uses"):
			idStr = strings.TrimSuffix(idStr, "_uses")
		case strings.HasSuffix(idStr, "_ok"):
			idStr = strings.TrimSuffix(idStr, "_ok")
			isOK = true
		default:
			continue
		}
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(val)
		s := byID[id]
		if s == nil {
			s = &Stat{ID: id}
			byID[id] = s
		}
		if isOK {
			s.OK += n
		} else {
			s.Uses += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("playbook stats: rows: %w", err)
	}
	out := make([]Stat, 0, len(byID))
	for _, s := range byID {
		if s.Uses > 0 {
			s.Rate = math.Round(float64(s.OK)/float64(s.Uses)*1000) / 1000
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
