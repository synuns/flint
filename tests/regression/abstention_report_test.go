package regression

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

const abstTopicPrefix = "reg-abst-"

func abstUnknownTopics() []string {
	return []string{
		abstTopicPrefix + "absent-alpha",
		abstTopicPrefix + "absent-bravo",
		abstTopicPrefix + "absent-charlie",
		abstTopicPrefix + "absent-delta",
		abstTopicPrefix + "absent-echo",
	}
}

type abstPgxStore struct {
	pool *pgxpool.Pool
}

func (s *abstPgxStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([][]any, 0, 8)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func (s *abstPgxStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *abstPgxStore) Close()                         { s.pool.Close() }

func (s *abstPgxStore) Migrate(ctx context.Context) error { return nil }
func (s *abstPgxStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (s *abstPgxStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (s *abstPgxStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (s *abstPgxStore) Commit(ctx context.Context, msg string) (string, error) {
	return "", nil
}
func (s *abstPgxStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	return nil, nil
}
func (s *abstPgxStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return nil, nil
}
func (s *abstPgxStore) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return nil, nil
}
func (s *abstPgxStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return nil, nil
}
func (s *abstPgxStore) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return nil, nil
}
func (s *abstPgxStore) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	return 0, "", nil
}
func (s *abstPgxStore) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	return nil
}
func (s *abstPgxStore) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	return nil
}
func (s *abstPgxStore) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	return nil
}

var _ wire.Store = (*abstPgxStore)(nil)

func abstClearTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM read_events`,
		`DELETE FROM knowledge`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("픽스처 청소 실패(%q): %v", q, err)
		}
	}
}

func abstInsertReadEvent(t *testing.T, pool *pgxpool.Pool, hits int, zero bool, reason string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank, bytes, zero_reason, source, near_score)
		 VALUES ($1, $2, false, false, 'reg-abst-session', '', 0, 0, $3, 'cli', 0)`,
		hits, zero, reason); err != nil {
		t.Fatalf("read_events 픽스처 삽입: %v", err)
	}
}

func abstCountRows(t *testing.T, pool *pgxpool.Pool, where string) int {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE `+where).Scan(&n); err != nil {
		t.Fatalf("read_events 집계(%s): %v", where, err)
	}
	return n
}

func TestAbstentionScored(t *testing.T) {
	if dsn := regTestDSN(); dsn == groundOpsGuardDSN {
		t.Fatalf("abstention DSN이 운영 DSN과 일치 — 엔진 격리 위반: %s", dsn)
	}
	pool := regDrillDB(t)
	defer pool.Close()
	abstClearTables(t, pool)

	abstained := 0
	topics := abstUnknownTopics()
	for _, topic := range topics {
		out, code := regDrillRun("read", "--json", "--fields", "topic", "--why", topic)
		if code != 0 {
			t.Fatalf("[%s] read exit=%d, want 0; out=%s", topic, code, out)
		}
		if strings.Contains(out, `"topic"`) {
			t.Errorf("[%s] 소유하지 않은 질의에 행 도달: %s", topic, out)
			continue
		}
		if !strings.Contains(out, `"zero_reason":"unowned"`) {
			t.Errorf("[%s] why 판정이 unowned 아님: %s", topic, out)
			continue
		}
		abstained++
	}
	if abstained != len(topics) {
		t.Fatalf("abstention 판정 %d/%d — 미판정 질의 존재", abstained, len(topics))
	}

	unowned := abstCountRows(t, pool, `zero = true AND zero_reason = 'unowned'`)
	if unowned != abstained {
		t.Fatalf("unowned zero-hit 행 %d, want %d — 판정과 read_events 적립 불일치", unowned, abstained)
	}
	zeroRows := abstCountRows(t, pool, `zero = true`)
	if zeroRows != unowned {
		t.Fatalf("zero-hit 분모 %d != unowned %d — zero_hit_unowned_pct 분모와 판정 불일치", zeroRows, unowned)
	}

	pct, err := ops.ZeroHitUnownedPct(context.Background(), &abstPgxStore{pool: pool}, 1)
	if err != nil {
		t.Fatalf("ZeroHitUnownedPct: %v", err)
	}
	if pct != 100 {
		t.Fatalf("zero_hit_unowned_pct = %v, want 100 (모든 zero-hit이 unowned)", pct)
	}
	t.Logf("abstention_scoring: verdicts=%d unowned_rows=%d zero_rows=%d zero_hit_unowned_pct=%.1f",
		abstained, unowned, zeroRows, pct)
}

func TestRegressionExposure(t *testing.T) {
	if dsn := regTestDSN(); dsn == groundOpsGuardDSN {
		t.Fatalf("exposure DSN이 운영 DSN과 일치 — 엔진 격리 위반: %s", dsn)
	}
	pool := regDrillDB(t)
	defer pool.Close()
	abstClearTables(t, pool)

	abstInsertReadEvent(t, pool, 0, true, "unowned")
	abstInsertReadEvent(t, pool, 0, true, "owned_miss")
	abstInsertReadEvent(t, pool, 2, false, "")

	var wantPct float64 = 50

	textOut, code := regDrillRun("report", "--text", "--days", "1")
	if code != 0 {
		t.Fatalf("report --text exit=%d, want 0; out=%s", code, textOut)
	}
	row := ""
	for _, ln := range strings.Split(textOut, "\n") {
		if strings.Contains(ln, "zero_hit_unowned_pct") {
			row = ln
			break
		}
	}
	if row == "" {
		t.Fatalf("report 텍스트에 zero_hit_unowned_pct 행 없음:\n%s", textOut)
	}
	if !strings.Contains(row, strconv.FormatFloat(wantPct, 'f', 1, 64)+"%") {
		t.Errorf("report 텍스트 abstention 행 %q, want %.1f%% — 단일 소스 불일치", row, wantPct)
	}

	jsonOut, code := regDrillRun("report", "--days", "1")
	if code != 0 {
		t.Fatalf("report exit=%d, want 0; out=%s", code, jsonOut)
	}
	var rep ops.ReportOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &rep); err != nil {
		t.Fatalf("report JSON ReportOutput 언마샬 실패: %v; out=%s", err, jsonOut)
	}
	if rep.Effect.ZeroHitUnownedPct != wantPct {
		t.Errorf("report JSON zero_hit_unowned_pct = %v, want %.1f", rep.Effect.ZeroHitUnownedPct, wantPct)
	}

	storeFn := func(ctx context.Context) (wire.Store, error) {
		p, err := db.Connect(ctx, regTestDSN())
		if err != nil {
			return nil, err
		}
		return &abstPgxStore{pool: p}, nil
	}
	srv := httptest.NewServer(ops.HTMLHandler(storeFn, 1))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("serve HTML 요청: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("serve HTML 읽기: %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serve HTML status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "zero-hit unowned pct") {
		t.Errorf("serve HTML에 zero-hit unowned pct 행 없음")
	}
	if !strings.Contains(body, strconv.FormatFloat(wantPct, 'f', 1, 64)+"%") {
		t.Errorf("serve HTML abstention 값 %.1f%% 미노출", wantPct)
	}

	if got, err := ops.ZeroHitUnownedPct(context.Background(), &abstPgxStore{pool: pool}, 1); err != nil || got != wantPct {
		t.Errorf("단일 소스 ZeroHitUnownedPct = (%v, %v), want (50.0, nil)", got, err)
	}
}
