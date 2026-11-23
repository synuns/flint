package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var (
	exportTOld = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	exportTMid = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	exportTNew = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

	exportTValid = time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)

	exportTRecall = time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC)
)

const exportHashPrefix = "expfix-"

type exportFixtureRow struct {
	id       int
	topic    string
	summary  string
	tags     string
	entity   any
	domain   string
	conf     float64
	origin   string
	status   string
	hash     string
	sup      any
	valid    time.Time
	ingested time.Time
	invalid  any
	score    int
	lastRec  any
}

func (f exportFixtureRow) updated() time.Time {
	if t, ok := f.invalid.(time.Time); ok {
		return t
	}
	return f.ingested
}

func (f exportFixtureRow) cells() []any {
	return []any{
		int32(f.id), f.topic, f.summary, f.tags, f.entity, f.domain,
		f.conf, f.origin, f.status, f.hash, f.sup, f.valid, f.ingested, f.updated(),
		int32(f.score), f.lastRec,
	}
}

var exportFixture = []exportFixtureRow{
	{1, "배포 관례", "main 브랜치는 항상 green이어야 한다", ":deploy:ops:",
		"main-branch", "demo", 0.9, "agent", "active", "expfix-a1", nil,
		exportTValid, exportTOld, nil, 3, exportTRecall},
	{7, "회고 형식", "회고는 성공/실패/시도 순서로 기록한다", ":meeting:",
		nil, "demo", 0.5, "human", "active", "expfix-b2", nil,
		exportTValid, exportTOld, nil, 0, nil},
	{12, "구 배포 정책", "폐기된 이전 정책 — 신규 항목으로 대체됨", ":deploy:",
		nil, "demo", 0.4, "agent", "invalidated", "expfix-c3", 88,
		exportTValid, exportTOld, exportTNew, 0, nil},
}

func exportEngine(sql string, args []any) ([][]any, error) {
	rows := make([][]any, 0, len(exportFixture))
	for _, f := range exportFixture {
		rows = append(rows, f.cells())
	}
	if strings.Contains(sql, "invalid_at IS NULL") {
		out := rows[:0]
		for _, r := range rows {
			if r[8] == "active" {
				out = append(out, r)
			}
		}
		rows = out
	}
	if strings.Contains(sql, "strpos(") {
		tag, _ := args[0].(string)
		out := rows[:0]
		for _, r := range rows {
			if strings.Contains(r[3].(string), ":"+tag+":") {
				out = append(out, r)
			}
		}
		rows = out
	}
	if strings.Contains(sql, "COALESCE(invalid_at, ingested_at) >=") {

		var since time.Time
		switch v := args[len(args)-1].(type) {
		case time.Time:
			since = v
		case string:
			if pt, perr := time.Parse(time.RFC3339, v); perr == nil {
				since = pt
			}
		}
		out := rows[:0]
		for _, r := range rows {
			if t, ok := r[13].(time.Time); ok && !t.Before(since) {
				out = append(out, r)
			}
		}
		rows = out
	}
	return rows, nil
}

func assertExportTopics(t *testing.T, rows []ExportRow, want []string) {
	t.Helper()
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Topic
		if i > 0 && rows[i-1].ID >= rows[i].ID {
			t.Errorf("결과가 id 오름차순이 아님: %d 다음 %d", rows[i-1].ID, rows[i].ID)
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("토픽 순서 = %v, want %v", got, want)
	}
}

func exportScopedRows(rows []ExportRow, prefix string) []ExportRow {
	if prefix == "" {
		return rows
	}
	out := make([]ExportRow, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r.ContentHash, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func exportTestStore(t *testing.T) (st wire.Store, scope string) {
	t.Helper()
	if real := exportRealStore(t); real != nil {
		return real, exportHashPrefix
	}
	return &fakeStore{queryFn: exportEngine}, ""
}

type exportPoolStore struct {
	pool *pgxpool.Pool
	wire.HistoryStubs
}

func (s *exportPoolStore) Migrate(ctx context.Context) error { return nil }
func (s *exportPoolStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (s *exportPoolStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (s *exportPoolStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (s *exportPoolStore) Commit(ctx context.Context, msg string) (string, error) {
	return "", nil
}
func (s *exportPoolStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *exportPoolStore) Close()                         { s.pool.Close() }
func (s *exportPoolStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
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

func exportRealStore(t *testing.T) wire.Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return nil
	}
	dsn := testengine.ResolveDSN()
	if repo, rerr := db.NewRepo(context.Background(), dsn); rerr == nil {
		gerr := repo.EnsureSchemaV11(context.Background())
		repo.Close()
		if gerr != nil {
			t.Skipf("schema v11 gate unavailable: %v", gerr)
		}
	}
	pool, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	seedExportFixture(t, pool)
	return &exportPoolStore{pool: pool}
}

func seedExportFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	del := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM recall_events WHERE knowledge_id IN
			(SELECT id FROM knowledge WHERE content_hash LIKE $1)`, exportHashPrefix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM knowledge WHERE content_hash LIKE $1`, exportHashPrefix+"%")
	}
	del()
	t.Cleanup(del)
	for _, f := range exportFixture {
		if _, err := pool.Exec(ctx, `INSERT INTO knowledge
			(topic, summary, tags, entity, domain, confidence, origin, status,
			 content_hash, superseded_by, valid_at, ingested_at, invalid_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			f.topic, f.summary, f.tags, f.entity, f.domain, f.conf, f.origin,
			f.status, f.hash, f.sup, f.valid, f.ingested, f.invalid); err != nil {
			t.Skipf("fixture 시딩 실패(스키마 미구비 의심): %v", err)
		}

		if f.score > 0 {
			if _, err := pool.Exec(ctx,
				`INSERT INTO recall_events (knowledge_id, recalled_at)
				 SELECT k.id, $2 FROM knowledge k, generate_series(1, $3::int)
				 WHERE k.content_hash = $1`, f.hash, f.lastRec, f.score); err != nil {
				t.Skipf("회상 이벤트 시딩 실패(스키마 미구비 의심): %v", err)
			}
		}
	}
}

func TestExportRowsActiveFilter(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{"active", ""} {
		st, scope := exportTestStore(t)
		rows, err := ExportRows(ctx, st, ExportFilter{Status: status})
		if err != nil {
			t.Fatalf("Status %q ExportRows err = %v", status, err)
		}
		assertExportTopics(t, exportScopedRows(rows, scope),
			[]string{"배포 관례", "회고 형식"})
	}
}

func TestExportRowsAllIncludesInvalidated(t *testing.T) {
	st, scope := exportTestStore(t)
	rows, err := ExportRows(context.Background(), st, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope),
		[]string{"배포 관례", "회고 형식", "구 배포 정책"})
}

func TestExportRowsTagFilter(t *testing.T) {
	ctx := context.Background()
	st, scope := exportTestStore(t)
	rows, err := ExportRows(ctx, st, ExportFilter{Tag: "deploy"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope), []string{"배포 관례"})

	st2, scope2 := exportTestStore(t)
	rows2, err := ExportRows(ctx, st2, ExportFilter{Status: "all", Tag: "deploy"})
	if err != nil {
		t.Fatalf("ExportRows(all) err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows2, scope2),
		[]string{"배포 관례", "구 배포 정책"})
}

func TestExportRowsSinceCutoff(t *testing.T) {
	ctx := context.Background()
	st, scope := exportTestStore(t)
	rows, err := ExportRows(ctx, st, ExportFilter{Status: "all", Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope), []string{"구 배포 정책"})

	st2, scope2 := exportTestStore(t)
	rows2, err := ExportRows(ctx, st2, ExportFilter{Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows(active+since) err = %v", err)
	}
	if got := exportScopedRows(rows2, scope2); len(got) != 0 {
		t.Errorf("active+Since 컷오프 = %d행, want 0행(tOld 행 제외)", len(got))
	}
}

func TestExportSinceBindsRFC3339String(t *testing.T) {
	_, args := buildExportQuery(ExportFilter{Status: "all", Since: exportTMid})
	if len(args) != 1 {
		t.Fatalf("args = %v, want Since 1개", args)
	}
	s, ok := args[0].(string)
	if !ok {
		t.Fatalf("Since 바인드형 = %T, want string(RFC3339 — time.Time 직접 바인드 금지)", args[0])
	}
	pt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("Since 문자열 %q 파싱 실패: %v", s, err)
	}
	if !pt.Equal(exportTMid) {
		t.Errorf("Since 문자열 = %q(%v), want %v 원시각 보존", s, pt, exportTMid)
	}

	st, scope := exportTestStore(t)
	rows, err := ExportRows(context.Background(), st, ExportFilter{Status: "all", Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope), []string{"구 배포 정책"})
}

func TestExportRowFieldMapping(t *testing.T) {
	rows, err := ExportRows(context.Background(),
		&fakeStore{queryFn: exportEngine}, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("행 수 = %d, want 3", len(rows))
	}
	byTopic := map[string]ExportRow{}
	for _, r := range rows {
		byTopic[r.Topic] = r
	}
	a := byTopic["배포 관례"]
	if a.ID != 1 || a.Content != "main 브랜치는 항상 green이어야 한다" ||
		a.Status != "active" || a.Confidence != 0.9 || a.Domain != "demo" ||
		a.ContentHash != "expfix-a1" {
		t.Errorf("기본 필드 매핑 오류: %+v", a)
	}
	if strings.Join(a.Tags, ",") != "deploy,ops" {
		t.Errorf("tags 파싱 = %v, want [deploy ops]", a.Tags)
	}
	if strings.Join(a.Entities, ",") != "main-branch" {
		t.Errorf("entities = %v, want [main-branch]", a.Entities)
	}
	if a.Kind != "knowledge" {
		t.Errorf("kind = %q, want knowledge(원천 테이블 판별자)", a.Kind)
	}
	if a.Source != "agent" {
		t.Errorf("source = %q, want \"agent\"(origin 열 매핑)", a.Source)
	}
	if b := byTopic["회고 형식"]; b.Source != "human" {
		t.Errorf("회고 형식 source = %q, want \"human\"(origin 열 보존 — 왕복 dedup 대칭)", b.Source)
	}
	if !a.CreatedAt.Equal(exportTOld) || !a.UpdatedAt.Equal(exportTOld) {
		t.Errorf("active 행 시각: created=%v updated=%v, want 둘 다 %v",
			a.CreatedAt, a.UpdatedAt, exportTOld)
	}
	if a.SupersededBy != nil {
		t.Errorf("superseded_by = %v, want nil", *a.SupersededBy)
	}
	c := byTopic["구 배포 정책"]
	if c.Status != "invalidated" {
		t.Errorf("invalidated 행 status = %q", c.Status)
	}
	if c.SupersededBy == nil || *c.SupersededBy != 88 {
		t.Errorf("superseded_by = %v, want 88", c.SupersededBy)
	}
	if !c.CreatedAt.Equal(exportTOld) || !c.UpdatedAt.Equal(exportTNew) {
		t.Errorf("invalidated 행 시각: created=%v updated=%v, want created %v·updated %v(invalid_at)",
			c.CreatedAt, c.UpdatedAt, exportTOld, exportTNew)
	}
	if c.Entities != nil {
		t.Errorf("entity NULL 행 entities = %v, want nil", c.Entities)
	}
}

func TestExportRowValidAtMapping(t *testing.T) {
	rows, err := ExportRows(context.Background(),
		&fakeStore{queryFn: exportEngine}, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("행 수 = %d, want 3", len(rows))
	}
	for _, r := range rows {
		if !r.ValidAt.Equal(exportTValid) {
			t.Errorf("%s ValidAt = %v, want %v(valid_at 열 원값)", r.Topic, r.ValidAt, exportTValid)
		}
		if r.ValidAt.Equal(r.CreatedAt) {
			t.Errorf("%s ValidAt == CreatedAt — fixture는 의도적 상이(치환 폴백 의심)", r.Topic)
		}
	}
}

func TestExportRowsInvalidStatusError(t *testing.T) {
	_, err := ExportRows(context.Background(), &fakeStore{},
		ExportFilter{Status: "pending"})
	if err == nil {
		t.Fatal("무효 status 오류 = nil, want SchemaError")
	}
	var se *store.SchemaError
	if !errors.As(err, &se) || se.Field != "status" {
		t.Errorf("오류 = %v, want *store.SchemaError(field=status)", err)
	}
}

func TestExportRowsQueryErrorPropagation(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ExportRows(context.Background(),
		&fakeStore{queryErr: boom}, ExportFilter{Status: "all"}); !errors.Is(err, boom) {
		t.Errorf("오류 전파 = %v, want boom", err)
	}
}

func TestExportRowScoreColumnsMapping(t *testing.T) {
	rows, err := ExportRows(context.Background(),
		&fakeStore{queryFn: exportEngine}, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	byTopic := map[string]ExportRow{}
	for _, r := range rows {
		byTopic[r.Topic] = r
	}
	a := byTopic["배포 관례"]
	if a.RecallScore != 3 {
		t.Errorf("배포 관례 recall_score = %d, want 3(실열 매핑)", a.RecallScore)
	}
	if a.LastRecalledAt == nil || !a.LastRecalledAt.Equal(exportTRecall) {
		t.Errorf("배포 관례 last_recalled_at = %v, want %v(실열 매핑)", a.LastRecalledAt, exportTRecall)
	}
	b := byTopic["회고 형식"]
	if b.RecallScore != 0 || b.LastRecalledAt != nil {
		t.Errorf("회고 형식 스코어 열 = (%d,%v), want (0,nil — 기본값 행)", b.RecallScore, b.LastRecalledAt)
	}
}

func TestExportScoreJSONLRoundTrip(t *testing.T) {

	scored := []ExportRow{{
		ID: 21, Topic: "demo score roundtrip", Content: "합성 데모 — 스코어 왕복 보존.",
		Kind: "knowledge", Status: "active", Confidence: 0.9, Domain: "demo",
		Source: "agent", ContentHash: "score-rt", CreatedAt: exportTOld,
		UpdatedAt: exportTOld, ValidAt: exportTValid,
		RecallScore: 5, LastRecalledAt: &exportTRecall,
	}}
	data, err := FormatJSONL(scored)
	if err != nil {
		t.Fatalf("FormatJSONL: %v", err)
	}
	got, err := ParseJSONLRows(data)
	if err != nil {
		t.Fatalf("ParseJSONLRows: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].RecallScore != 5 {
		t.Errorf("왕복 recall_score = %d, want 5", got[0].RecallScore)
	}
	if got[0].LastRecalledAt == nil || !got[0].LastRecalledAt.Equal(exportTRecall) {
		t.Errorf("왕복 last_recalled_at = %v, want %v", got[0].LastRecalledAt, exportTRecall)
	}

	legacy := `{"id":22,"topic":"legacy demo","content":"합성 데모","kind":"knowledge","status":"active","confidence":0.5,"tags":null,"entities":null,"domain":"main","source":"agent","content_hash":"legacy-h","created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z","valid_at":"2026-11-01T00:00:00Z","superseded_by":null}` + "\n"
	lr, err := ParseJSONLRows([]byte(legacy))
	if err != nil {
		t.Fatalf("ParseJSONLRows(legacy): %v", err)
	}
	if len(lr) != 1 {
		t.Fatalf("legacy rows = %d, want 1", len(lr))
	}
	if lr[0].RecallScore != 0 || lr[0].LastRecalledAt != nil {
		t.Errorf("legacy 폴백 = (%d,%v), want (0,nil)", lr[0].RecallScore, lr[0].LastRecalledAt)
	}

	zero := scored[0]
	zero.ID, zero.ContentHash, zero.RecallScore, zero.LastRecalledAt = 23, "score-zero", 0, nil
	out, err := FormatJSONL([]ExportRow{zero})
	if err != nil {
		t.Fatalf("FormatJSONL(zero): %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(m) != 15 {
		t.Errorf("0/NULL 행 키 수 = %d, want 15(기존 골든 불변): %v", len(m), m)
	}
	for _, k := range []string{"recall_score", "last_recalled_at"} {
		if _, ok := m[k]; ok {
			t.Errorf("0/NULL 행에 신규 키 %q 발행 — additive 위반", k)
		}
	}
}

func TestRowHashRecScoreCarry(t *testing.T) {
	scored := ExportRow{ID: 1, Topic: "demo score carry", Content: "합성 데모",
		Kind: "knowledge", Status: "active", Confidence: 0.9, Domain: "demo",
		Source: "agent", ContentHash: "scored-h", RecallScore: 4,
		LastRecalledAt: &exportTRecall}
	hashInput, rec, se := rowHashRec(scored)
	if se != nil {
		t.Fatalf("rowHashRec: %v", se)
	}
	for _, k := range []string{"recall_score", "last_recalled_at"} {
		if _, ok := rec[k]; ok {
			t.Errorf("rec에 %q 키 설정 — R42-A: 폐기 컬럼 INSERT 조립 위반(재생 경로로 이관)", k)
		}
		if _, in := hashInput[k]; in {
			t.Errorf("해시 입력에 %q 포함 — 정체성 키 아님(importHashSkip 계약)", k)
		}
	}

	plain := scored
	plain.RecallScore, plain.LastRecalledAt = 0, nil
	_, rec2, se2 := rowHashRec(plain)
	if se2 != nil {
		t.Fatalf("rowHashRec(plain): %v", se2)
	}
	for _, k := range []string{"recall_score", "last_recalled_at"} {
		if _, ok := rec2[k]; ok {
			t.Errorf("0/NULL 행 rec에 %q 키 설정", k)
		}
	}
}

func TestExportScoreColumnsReal(t *testing.T) {
	st := exportRealStore(t)
	if st == nil {
		t.Skip("real store not configured: SUPERLITE_REAL=1 + FLINT_TEST_DSN")
	}
	ctx := context.Background()
	rows, err := ExportRows(ctx, st, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	scoped := exportScopedRows(rows, exportHashPrefix)
	if len(scoped) != 3 {
		t.Fatalf("시딩 행 = %d, want 3", len(scoped))
	}
	var scored ExportRow
	var plain *ExportRow
	for i, r := range scoped {
		if r.ContentHash == "expfix-a1" {
			scored = r
		}
		if r.ContentHash == "expfix-b2" {
			plain = &scoped[i]
		}
	}
	if scored.RecallScore != 3 {
		t.Errorf("실열 recall_score = %d, want 3", scored.RecallScore)
	}
	if scored.LastRecalledAt == nil || !scored.LastRecalledAt.Equal(exportTRecall) {
		t.Errorf("실열 last_recalled_at = %v, want %v", scored.LastRecalledAt, exportTRecall)
	}
	if plain == nil || plain.RecallScore != 0 || plain.LastRecalledAt != nil {
		t.Errorf("미설정 행 폴백 = %+v, want (0,nil)", plain)
	}

	data, err := FormatJSONL([]ExportRow{scored})
	if err != nil {
		t.Fatalf("FormatJSONL: %v", err)
	}
	got, err := ParseJSONLRows(data)
	if err != nil {
		t.Fatalf("ParseJSONLRows: %v", err)
	}
	if got[0].RecallScore != 3 || got[0].LastRecalledAt == nil ||
		!got[0].LastRecalledAt.Equal(exportTRecall) {
		t.Errorf("실DB 왕복 스코어 = (%d,%v), want (3,%v)",
			got[0].RecallScore, got[0].LastRecalledAt, exportTRecall)
	}
}
