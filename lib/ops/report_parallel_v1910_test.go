package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func dispatchReportQuery(sql string) ([][]any, error) {
	now := time.Now().UTC()
	switch {
	case strings.Contains(sql, "LIKE 'daily:%'"):
		rows := [][]any{}
		for i := 0; i < 5; i++ {
			d := now.AddDate(0, 0, -i).Format("2006-01-02")
			rows = append(rows,
				[]any{"daily:write:" + d, "12"},
				[]any{"daily:read:" + d, "20"},
				[]any{"daily:dup:" + d, "3"},
				[]any{"daily:dup_hook:" + d, "2"},
				[]any{"daily:dup_manual:" + d, "1"},
				[]any{"daily:approve_done:" + d, "4"},
				[]any{"daily:import_added:" + d, "2"},
			)
		}

		return append(rows, []any{"daily:write:" + now.AddDate(0, 0, -30).Format("2006-01-02"), "999"}), nil
	case strings.Contains(sql, "near FROM read_events"):
		return [][]any{{now, false, false}, {now, true, true}, {now, false, false}}, nil
	case strings.Contains(sql, "fallback, ids"):
		return [][]any{
			{now, false, false, "1,2", 1, 400, "", 2},
			{now, true, true, "", 0, 0, "unowned", 0},
			{now, false, true, "3", 2, 250, "", 1},
		}, nil
	case strings.Contains(sql, "status = 'pending'"):
		return [][]any{{int64(5), int64(50), int64(2)}}, nil
	case strings.Contains(sql, "/* stale */"):
		return [][]any{{int64(3), int64(1)}}, nil
	case strings.Contains(sql, "invalid_at >= $1"):
		return [][]any{{int64(1)}}, nil
	case strings.Contains(sql, "read_count > 0"):
		return [][]any{{int64(50), int64(15)}}, nil
	case strings.Contains(sql, "/* dead */"):
		return [][]any{{int64(1)}, {int64(2)}, {int64(7)}}, nil
	case strings.Contains(sql, "id, topic FROM knowledge"):
		return [][]any{{int64(1), "demo-topic-001"}, {int64(2), "demo-topic-002"}, {int64(3), "demo-topic-003"}}, nil
	case strings.Contains(sql, "id, invalid_at, superseded_by"):
		return [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}}, nil
	case strings.Contains(sql, "length(summary)"):
		return [][]any{{int64(1), int64(200)}, {int64(2), int64(180)}, {int64(3), int64(220)}}, nil
	case strings.Contains(sql, "FROM hot_events"):
		return [][]any{{"1,2"}, {"3"}}, nil
	case strings.Contains(sql, "FROM knowledge_reads"):
		return [][]any{{int64(1), now, "read"}, {int64(2), now, "hot"}, {int64(3), now, "read"}}, nil
	case strings.Contains(sql, "FROM session_starts"):
		return [][]any{{now.Add(-2 * time.Hour)}, {now.Add(-1 * time.Hour)}}, nil
	case strings.Contains(sql, "ingested_at >="):
		return [][]any{{now.Add(-30 * time.Minute), "demo-topic-001"}}, nil
	case strings.Contains(sql, "FROM meta"):
		return nil, nil
	}
	return [][]any{{int64(0), int64(0), int64(0)}}, nil
}

func parallelReportFake(errs map[string]error) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		for pat, msg := range errs {
			if strings.Contains(sql, pat) {
				return nil, errors.New(msg.Error())
			}
		}
		return dispatchReportQuery(sql)
	}}
}

func reportSequentialJSON(ctx context.Context, st wire.Store, days int) (string, int) {
	th := knowledge.WriteThresholds(ctx, st)
	if max := th.DailyRetentionD; max > 0 && days > max {
		days = max
	}
	out := ReportOutput{Days: make([]ReportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	collectCuration(ctx, st, days, &out)
	if err := collectStats(ctx, st, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	if err := collectCounts(ctx, st, days, th, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	if err := collectEffect(ctx, st, days, th, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	out.DupPct = DupPct(out.Days)
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}
	out.Diagnosis = Diagnose(out, th, knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	b, err := json.Marshal(out)
	if err != nil {
		return wire.ErrLine(err), 1
	}
	return string(b) + "\n", 0
}

func TestReportParallelConcurrent(t *testing.T) {
	const want = 4
	dailyCalls := 0
	var mu sync.Mutex
	entered := 0
	joined := map[string]bool{}
	release := make(chan struct{})

	join := func(pat string) error {
		mu.Lock()
		if !joined[pat] {
			joined[pat] = true
			entered++
			if entered == want {
				close(release)
			}
		}
		mu.Unlock()
		select {
		case <-release:
			return nil
		case <-time.After(3 * time.Second):
			return errors.New("concurrency barrier timeout — 수집기 4종이 동시에 겹치지 않음(순차 실행)")
		}
	}
	slot := func(sql string) string {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			mu.Lock()
			dailyCalls++
			second := dailyCalls == 2
			mu.Unlock()
			if second {
				return "curation"
			}
		case strings.Contains(sql, "FROM knowledge_reads"):
			return "stats"
		case strings.Contains(sql, "status = 'pending'"):
			return "counts"
		case strings.Contains(sql, "read_count > 0"):
			return "effect"
		}
		return ""
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if pat := slot(sql); pat != "" {
			if err := join(pat); err != nil {
				return nil, err
			}
		}
		return dispatchReportQuery(sql)
	}}
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "3"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d(배리어 미통과 — 수집이 병렬로 겹치지 않음): %s", code, out.String())
	}
}

func TestReportParallelJSONByteParity(t *testing.T) {
	ctx := context.Background()
	f := parallelReportFake(nil)
	var par strings.Builder
	if code := RunReport(ctx, f, []string{"--days", "3"}, nil, &par); code != 0 {
		t.Fatalf("report exit=%d: %s", code, par.String())
	}
	seq, seqExit := reportSequentialJSON(ctx, f, 3)
	if seqExit != 0 {
		t.Fatalf("순차 오라클 exit=%d", seqExit)
	}
	if par.String() != seq {
		t.Errorf("병렬 JSON과 순차 산물 불일치:\n par=%s\nseq=%s", par.String(), seq)
	}
}

func TestReportParallelErrorPriority(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		errs map[string]error
	}{
		{
			name: "복수 실패 — counts가 effect보다 우선",
			errs: map[string]error{
				"status = 'pending'": errors.New("counts query failed"),
				"read_count > 0":     errors.New("effect query failed"),
			},
		},
		{
			name: "effect 단독 실패",
			errs: map[string]error{"read_count > 0": errors.New("effect query failed")},
		},
		{
			name: "daily 실패 — 병렬 구간 진입 전",
			errs: map[string]error{"LIKE 'daily:%'": errors.New("daily query failed")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var par strings.Builder
			code := RunReport(ctx, parallelReportFake(tc.errs), []string{"--days", "3"}, nil, &par)
			seq, seqExit := reportSequentialJSON(ctx, parallelReportFake(tc.errs), 3)
			if code != seqExit {
				t.Errorf("exit=%d, want %d(순차) — out=%s", code, seqExit, par.String())
			}
			if strings.TrimSpace(par.String()) != strings.TrimSpace(seq) {
				t.Errorf("ErrLine 불일치:\n par=%s\nseq=%s", par.String(), seq)
			}
			if code == 0 {
				t.Fatal("오류 주입 케이스인데 exit 0")
			}
		})
	}
}

type reportSlowStore struct {
	wire.HistoryStubs
	repo *db.Repo
	pool *pgxpool.Pool
}

func (s *reportSlowStore) Migrate(ctx context.Context) error { return s.repo.Migrate(ctx) }
func (s *reportSlowStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return s.repo.InsertBatch(ctx, r)
}
func (s *reportSlowStore) ExistsHash(ctx context.Context, h string) (bool, error) {
	return s.repo.ExistsHash(ctx, h)
}
func (s *reportSlowStore) ExistsProject(ctx context.Context, n string) (bool, error) {
	return s.repo.ExistsProject(ctx, n)
}
func (s *reportSlowStore) Commit(ctx context.Context, msg string) (string, error) {
	return s.repo.Commit(ctx, msg)
}
func (s *reportSlowStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *reportSlowStore) Close()                         { s.repo.Close(); s.pool.Close() }
func (s *reportSlowStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func seedReportSlow(ctx context.Context, s *reportSlowStore, now time.Time) error {
	var kb strings.Builder
	kb.WriteString(`INSERT INTO knowledge (topic, summary, tags, domain, confidence, origin, status, content_hash, valid_at, ingested_at, read_count, last_read_at) VALUES `)
	for i := 0; i < 500; i++ {
		if i > 0 {
			kb.WriteByte(',')
		}
		status := "active"
		if i%25 == 0 {
			status = "pending"
		}
		ing := now.Add(-time.Duration(i%20*24) * time.Hour)
		fmt.Fprintf(&kb, "('demo-topic-%03d','%s',':demo:','main',0.9,'agent','%s','demohash%03d','%s','%s',%d,'%s')",
			i, strings.Repeat("d", 80), status, i,
			now.UTC().Format("2006-01-02 15:04:05+00"), ing.UTC().Format("2006-01-02 15:04:05+00"),
			i%4, now.Add(-time.Duration(i%10*24)*time.Hour).UTC().Format("2006-01-02 15:04:05+00"))
	}
	if _, err := s.pool.Exec(ctx, kb.String()); err != nil {
		return fmt.Errorf("knowledge 시드: %w", err)
	}
	var rb strings.Builder
	rb.WriteString(`INSERT INTO read_events (ts, hits, zero, near, fallback, session, ids, first_rank, bytes, zero_reason, source) VALUES `)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			rb.WriteByte(',')
		}
		ts := now.Add(-time.Duration(i%14*24*60+i%97) * time.Minute)
		zero := i%3 == 0
		reason := ""
		if zero {
			reason = "unowned"
			if i%6 == 0 {
				reason = "owned_miss"
			}
		}
		ids := ""
		if !zero {
			ids = fmt.Sprintf("%d,%d", i%500+1, (i*7)%500+1)
		}
		fmt.Fprintf(&rb, "('%s',%d,%t,%t,%t,'demo','%s',%d,%d,'%s','cli')",
			ts.UTC().Format("2006-01-02 15:04:05+00"), i%5, zero, i%8 == 1, i%10 == 3,
			ids, i%4, 100+i%800, reason)
	}
	if _, err := s.pool.Exec(ctx, rb.String()); err != nil {
		return fmt.Errorf("read_events 시드: %w", err)
	}
	var kr strings.Builder
	kr.WriteString(`INSERT INTO knowledge_reads (knowledge_id, ts, session, kind) VALUES `)
	for i := 0; i < 800; i++ {
		if i > 0 {
			kr.WriteByte(',')
		}
		kind := "read"
		if i%5 == 0 {
			kind = "hot"
		}
		fmt.Fprintf(&kr, "(%d,'%s','demo','%s')", i%500+1,
			now.Add(-time.Duration(i%13*24*60+i%89)*time.Minute).UTC().Format("2006-01-02 15:04:05+00"), kind)
	}
	if _, err := s.pool.Exec(ctx, kr.String()); err != nil {
		return fmt.Errorf("knowledge_reads 시드: %w", err)
	}
	var hb strings.Builder
	hb.WriteString(`INSERT INTO hot_events (ts, ids) VALUES `)
	for i := 0; i < 40; i++ {
		if i > 0 {
			hb.WriteByte(',')
		}
		fmt.Fprintf(&hb, "('%s','%d,%d,%d')",
			now.Add(-time.Duration(i)*6*time.Hour).UTC().Format("2006-01-02 15:04:05+00"), i%500+1, (i*3)%500+1, (i*11)%500+1)
	}
	if _, err := s.pool.Exec(ctx, hb.String()); err != nil {
		return fmt.Errorf("hot_events 시드: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(`INSERT INTO session_starts (ts, session) VALUES `)
	for i := 0; i < 60; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "('%s','demo')",
			now.Add(-time.Duration(i)*5*time.Hour).UTC().Format("2006-01-02 15:04:05+00"))
	}
	if _, err := s.pool.Exec(ctx, sb.String()); err != nil {
		return fmt.Errorf("session_starts 시드: %w", err)
	}
	var mb strings.Builder
	mb.WriteString(`INSERT INTO meta (key, value) VALUES `)
	first := true
	for i := 12; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		for kind, v := range map[string]int{"write": 15, "read": 30, "dup": 4, "hot": 6, "brief": 3, "read_bytes": 9000, "hot_bytes": 4000, "brief_bytes": 1200, "approve_done": 2, "import_added": 1} {
			if !first {
				mb.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&mb, "('daily:%s:%s','%d')", kind, d, v)
		}
	}
	if _, err := s.pool.Exec(ctx, mb.String()); err != nil {
		return fmt.Errorf("meta daily 시드: %w", err)
	}
	return nil
}

func TestReportParallelWallClockRealSlow(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" || os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	dsn := os.Getenv("FLINT_TEST_DSN")
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer base.Close()

	name := fmt.Sprintf("flint_v1910rpt_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name

	repo, err := db.NewRepo(ctx, u.String())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	st := &reportSlowStore{repo: repo, pool: pool}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	if err := seedReportSlow(ctx, st, now); err != nil {
		t.Fatalf("시드: %v", err)
	}

	median := func(xs []float64) float64 {
		sort.Float64s(xs)
		return xs[1]
	}
	seqMs, parMs := make([]float64, 3), make([]float64, 3)
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		seq, seqExit := reportSequentialJSON(ctx, st, 7)
		seqMs[i] = float64(time.Since(t0).Milliseconds())
		var par strings.Builder
		t1 := time.Now()
		code := RunReport(ctx, st, []string{"--days", "7"}, nil, &par)
		parMs[i] = float64(time.Since(t1).Milliseconds())
		if seqExit != 0 || code != 0 {
			t.Fatalf("측정 런 실패: seq exit=%d par exit=%d — %s / %s", seqExit, code, seq, par.String())
		}
		if par.String() != seq {
			t.Errorf("실DB 병렬/순차 JSON 불일치(런 %d):\n par=%s\nseq=%s", i, par.String(), seq)
		}
	}
	sm, pm := median(seqMs), median(parMs)
	t.Logf("wall-clock 3회 중앙값(합성 시드: knowledge 500·read_events 2000·reads 800): 순차 %.0fms(%v) → 병렬 %.0fms(%v)",
		sm, seqMs, pm, parMs)
	if pm > sm*1.5 {
		t.Errorf("병렬 경로 실측 회귀: 병렬 중앙값 %.0fms > 순차 중앙값 %.0fms×1.5", pm, sm)
	}
}
