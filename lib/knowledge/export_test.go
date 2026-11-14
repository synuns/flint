// export_test.go — v1.3.0 T2: ExportRows 원천 조회 계약 테스트.
// Review Focus(플랜): 합성 3행(active 2·invalidated 1) — active 필터 2행·
// all 3행·tag 필터·Since 컷오프(updated_at 기준). 픽스처는 합성 데모
// 데이터(개인 데이터 금지). 기본 fakeStore(queryFn이 조회 원천 seam),
// SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시 실DB 경유로 동일 시나리오를
// 재실행한다(metaKVStore seam 관례 승계).
package knowledge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// exportTOld/TMid/TNew — fixture 시각(증분 컷오프 검증 3단계).
var (
	exportTOld = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	exportTMid = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	exportTNew = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
)

// exportHashPrefix — 실DB 시딩 행의 content_hash 접두사(스코프·정리 키).
const exportHashPrefix = "expfix-"

// exportFixtureRow — 합성 데모 행(SELECT 열 순서와 동일한 셀 생성).
type exportFixtureRow struct {
	id       int
	topic    string
	summary  string
	tags     string
	entity   any // nil 허용(열 NULL)
	domain   string
	conf     float64
	origin   string
	status   string
	hash     string
	sup      any // nil|int
	ingested time.Time
	invalid  any // nil|time.Time
}

// updated — 파생 updated_at(COALESCE(invalid_at, ingested_at)와 동일 정의).
func (f exportFixtureRow) updated() time.Time {
	if t, ok := f.invalid.(time.Time); ok {
		return t
	}
	return f.ingested
}

// cells — SELECT 열 순서(id..COALESCE) 셀 튜플.
func (f exportFixtureRow) cells() []any {
	return []any{
		int32(f.id), f.topic, f.summary, f.tags, f.entity, f.domain,
		f.conf, f.origin, f.status, f.hash, f.sup, f.ingested, f.updated(),
	}
}

// exportFixture — 합성 3행: active 2(배포 관례·회고 형식) + invalidated 1
// (구 배포 정책 — invalid_at=tNew·superseded_by=88).
var exportFixture = []exportFixtureRow{
	{1, "배포 관례", "main 브랜치는 항상 green이어야 한다", ":deploy:ops:",
		"main-branch", "demo", 0.9, "agent", "active", "expfix-a1", nil, exportTOld, nil},
	{7, "회고 형식", "회고는 성공/실패/시도 순서로 기록한다", ":meeting:",
		nil, "demo", 0.5, "human", "active", "expfix-b2", nil, exportTOld, nil},
	{12, "구 배포 정책", "폐기된 이전 정책 — 신규 항목으로 대체됨", ":deploy:",
		nil, "demo", 0.4, "agent", "invalidated", "expfix-c3", 88, exportTOld, exportTNew},
}

// exportEngine — fakeStore queryFn용 조회 원천 seam: 발행된 SQL의 조건
// 마컈와 placeholder args로 fixture 행을 실제 규칙과 동일하게 걸러 돌려준다
// (active 단일 정의·':t:' strpos 태그·COALESCE 컷오프).
func exportEngine(sql string, args []any) ([][]any, error) {
	rows := make([][]any, 0, len(exportFixture))
	for _, f := range exportFixture {
		rows = append(rows, f.cells())
	}
	if strings.Contains(sql, "invalid_at IS NULL") { // 활성 단일 정의 절
		out := rows[:0]
		for _, r := range rows {
			if r[8] == "active" {
				out = append(out, r)
			}
		}
		rows = out
	}
	if strings.Contains(sql, "strpos(") { // tag 필터 — args[0]=tag(조립 순서 계약)
		tag, _ := args[0].(string)
		out := rows[:0]
		for _, r := range rows {
			if strings.Contains(r[3].(string), ":"+tag+":") { // 열 3 = tags
				out = append(out, r)
			}
		}
		rows = out
	}
	if strings.Contains(sql, "COALESCE(invalid_at, ingested_at) >=") {
		since, _ := args[len(args)-1].(time.Time) // 말미 인자=Since
		out := rows[:0]
		for _, r := range rows {
			if t, ok := r[12].(time.Time); ok && !t.Before(since) {
				out = append(out, r)
			}
		}
		rows = out
	}
	return rows, nil
}

// assertExportTopics — 결과 토픽 순서(지정 순)와 id 오름차순 정렬 단언.
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

// exportScopedRows — 실DB 경로에서 이번 시딩 행(prefix)만 스코프(공유
// flint_test DB의 외래 행 제외 — fake 경로는 전수 그대로).
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

// exportTestStore — 시나리오용 저장소: 기본 fake, SUPERLITE_REAL=1 +
// FLINT_TEST_DSN 시 실DB(시딩 동반). 반환 scope는 실DB 행 스코프 접두사.
func exportTestStore(t *testing.T) (st wire.Store, scope string) {
	t.Helper()
	if real := exportRealStore(t); real != nil {
		return real, exportHashPrefix
	}
	return &fakeStore{queryFn: exportEngine}, ""
}

// exportPoolStore — 실DB wire.Store 최소 구현(Query만 실동작 — 조회
// 시나리오에 필요한 최소한, meta_kv_test poolStore 관례 승계).
type exportPoolStore struct {
	pool *pgxpool.Pool
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

// exportRealStore — SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만 실DB 접속
// 허용(운영 폴백 차단 — seamRealDSN N1 계약 승계). 미기동 시 스킵.
func exportRealStore(t *testing.T) wire.Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return nil
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return nil
	}
	pool, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	seedExportFixture(t, pool)
	return &exportPoolStore{pool: pool}
}

// seedExportFixture — 합성 fixture 3행 시딩 + 정리(prefix 키 — 이전 런
// 잔존도 선행 DELETE로 흡수).
func seedExportFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	del := func() {
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
			f.status, f.hash, f.sup, f.ingested, f.ingested, f.invalid); err != nil {
			t.Skipf("fixture 시딩 실패(스키마 미구비 의심): %v", err)
		}
	}
}

// TestExportRowsActiveFilter — Review Focus: active 필터는 invalidated를
// 제외한 2행. Status 0값("")도 active 기본.
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

// TestExportRowsAllIncludesInvalidated — Review Focus: Status "all"은
// invalidated 포함 3행.
func TestExportRowsAllIncludesInvalidated(t *testing.T) {
	st, scope := exportTestStore(t)
	rows, err := ExportRows(context.Background(), st, ExportFilter{Status: "all"})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope),
		[]string{"배포 관례", "회고 형식", "구 배포 정책"})
}

// TestExportRowsTagFilter — Review Focus: tag 필터(':t:' 정확 매칭).
// active 기본 + deploy → 1행, all + deploy → invalidated 포함 2행.
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

// TestExportRowsSinceCutoff — Review Focus: Since는 updated_at 컷오프
// (COALESCE(invalid_at, ingested_at) 파생 — 0값은 전체). tMid 컷은
// invalid_at=tNew인 행만 남긴다.
func TestExportRowsSinceCutoff(t *testing.T) {
	ctx := context.Background()
	st, scope := exportTestStore(t)
	rows, err := ExportRows(ctx, st, ExportFilter{Status: "all", Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope), []string{"구 배포 정책"})

	// active + 컷오프 — 두 active 행의 updated_at(=ingested_at)은 tOld라 0행.
	st2, scope2 := exportTestStore(t)
	rows2, err := ExportRows(ctx, st2, ExportFilter{Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows(active+since) err = %v", err)
	}
	if got := exportScopedRows(rows2, scope2); len(got) != 0 {
		t.Errorf("active+Since 컷오프 = %d행, want 0행(tOld 행 제외)", len(got))
	}
}

// TestExportRowFieldMapping — 전 필드 매핑 단억(fake — 결정적 id).
// content←summary·tags ':a:b:' 파싱·entities←entity열·kind 원천 판별자·
// source←origin열(왕복 해시 대칭 — 빈 값이면 origin≠agent 행이 재이식
// 중복 적립)·created_at←ingested_at·updated_at 파생·superseded_by nil/int.
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

// TestExportRowsInvalidStatusError — Status는 active|all만 허용(무효값은
// SchemaError — 호출부 exit 3 매핑 원천).
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

// TestExportRowsQueryErrorPropagation — 조회 오류는 그대로 전파(호출부
// MapExit exit 2 매핑 원천).
func TestExportRowsQueryErrorPropagation(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ExportRows(context.Background(),
		&fakeStore{queryErr: boom}, ExportFilter{Status: "all"}); !errors.Is(err, boom) {
		t.Errorf("오류 전파 = %v, want boom", err)
	}
}
