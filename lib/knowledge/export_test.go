// export_test.go — ExportRows 원천 조회 계약 테스트.
// 합성 3행(active 2·invalidated 1) — active 필터 2행·
// all 3행·tag 필터·Since 컷오프(updated_at 기준). 픽스처는 합성 데모
// 데이터(개인 데이터 금지). 기본 fakeStore(queryFn이 조회 원천 seam),
// SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시 실DB 경유로 동일 시나리오를
// 재실행한다(metaKVStore seam 관례 승계).
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

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// exportTOld/TMid/TNew — fixture 시각(증분 컷오프 검증 3단계).
var (
	exportTOld = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	exportTMid = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	exportTNew = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	// exportTValid — valid_at fixture 값(ingested_at과 의도적 상이 —
	// 왕복 보존 검증: 검증 시각 ≠ 수집 시각 행).
	exportTValid = time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	// exportTRecall — last_recalled_at fixture 값(스코어 실열
	// 매핑·jsonl 왕복 보존 검증).
	exportTRecall = time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC)
)

// exportHashPrefix — 실DB 시딩 행의 content_hash 접두사(스코프·정리 키).
const exportHashPrefix = "expfix-"

// exportFixtureRow — 합성 데모 행(SELECT 열 순서와 동일한 셀 생성).
// score·lastRec 추가(스코어 실열 — 말미 2셀).
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
	valid    time.Time
	ingested time.Time
	invalid  any // nil|time.Time
	score    int // v11 recall_score(기본 0)
	lastRec  any // nil|time.Time — v11 last_recalled_at
}

// updated — 파생 updated_at(COALESCE(invalid_at, ingested_at)와 동일 정의).
func (f exportFixtureRow) updated() time.Time {
	if t, ok := f.invalid.(time.Time); ok {
		return t
	}
	return f.ingested
}

// cells — SELECT 열 순서(id..COALESCE) 셀 튜플. valid_at은
// 추가 열(superseded_by 다음·ingested_at 이전). 스코어 2열은
// 말미 추가(기존 셀 인덱스 계약 불변 — exportEngine 열 참조 승계).
func (f exportFixtureRow) cells() []any {
	return []any{
		int32(f.id), f.topic, f.summary, f.tags, f.entity, f.domain,
		f.conf, f.origin, f.status, f.hash, f.sup, f.valid, f.ingested, f.updated(),
		int32(f.score), f.lastRec,
	}
}

// exportFixture — 합성 3행: active 2(배포 관례·회고 형식) + invalidated 1
// (구 배포 정책 — invalid_at=tNew·superseded_by=88). 배포 관례 행만
// 스코어(3·recall 시각)를 가진다 — 매핑·왕복 검증의 비영행.
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
		// 말미 인자=Since — 시각 비교 전 관용 변환(time.Time 직접 바인드
		// 폐지 후 RFC3339 문자열 인자 — 결과 불변 검증 동일 경로).
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

// exportRealStore — SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만 실DB 접속
// 허용(운영 폴백 차단 — seamRealDSN N1 계약 승계). 미기동 시 스킵.
// 접속 직후 게이트 v11(스코어 2열)을 보장한다 — ExportRows
// SELECT가 신규 열을 참조해 미승격 DB에서는 42703으로 실패한다.
func exportRealStore(t *testing.T) wire.Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return nil
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return nil
	}
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
			 content_hash, superseded_by, valid_at, ingested_at, invalid_at,
			 recall_score, last_recalled_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			f.topic, f.summary, f.tags, f.entity, f.domain, f.conf, f.origin,
			f.status, f.hash, f.sup, f.valid, f.ingested, f.invalid,
			f.score, f.lastRec); err != nil {
			t.Skipf("fixture 시딩 실패(스키마 미구비 의심): %v", err)
		}
	}
}

// TestExportRowsActiveFilter — active 필터는 invalidated를
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

// TestExportRowsAllIncludesInvalidated — Status "all"은
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

// TestExportRowsTagFilter — tag 필터(':t:' 정확 매칭).
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

// TestExportRowsSinceCutoff — Since는 updated_at 컷오프
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

// TestExportSinceBindsRFC3339String — Since는 time.Time을 직접 바인드하지
// 않고 RFC3339 문자열로 바인드한다(코드베이스 유일의 time.Time 파라미터
// 폐지 — Doltgres/PG 파라미터 이종 위험. import_jsonl valid_at 관례와
// 동일 형식). 문자열화 전후 쿼리 결과 불변도 같이 단언(컷오프 동일).
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
	// 결과 불변 — 문자열 Since로도 동일 컷오프(TestExportRowsSinceCutoff 기준선).
	st, scope := exportTestStore(t)
	rows, err := ExportRows(context.Background(), st, ExportFilter{Status: "all", Since: exportTMid})
	if err != nil {
		t.Fatalf("ExportRows err = %v", err)
	}
	assertExportTopics(t, exportScopedRows(rows, scope), []string{"구 배포 정책"})
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

// TestExportRowValidAtMapping — SELECT가 valid_at
// 열을 포함하고 ValidAt 필드로 매핑한다(왕복 보존의 원천 — 검증 시각이
// 수집 시각과 다른 행에서 CreatedAt 치환 없이 원값 노출).
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

// TestExportRowScoreColumnsMapping — SELECT는 recall_score·last_recalled_at 실열을 포함하고 ExportRow에
// 매핑한다(원천 파생 아님 — 값 있는 행 3·exportTRecall, 없는 행 0·nil).
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

// TestExportScoreJSONLRoundTrip — jsonl
// 왕복에서 스코어 2필드 보존. 레거시 라인(키 없음)은 폴백 0/NULL. 스코어
// 0/NULL 행은 신규 키를 발행하지 않는다(additive — 기존 골든 출력 불변).
func TestExportScoreJSONLRoundTrip(t *testing.T) {
	// 왕복 보존 — 비영(5·회상 시각) 행.
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

	// 폴백 — 레거시 jsonl(스코어 키 없음)은 0/NULL.
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

	// 골든 불변 — 0/NULL 행은 기존 15키만 발행(신규 키 미발행).
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

// TestRowHashRecScoreCarry — jsonl 재이식 rec 승계: 값 있을 때만
// recall_score·last_recalled_at rec 키를 포함하고 해시 입력에서는 제외
// (importHashSkip — 회상 이력은 내용 정체성이 아님). 0/NULL은 키 미설정
// → 컬럼 기본값 폴백.
func TestRowHashRecScoreCarry(t *testing.T) {
	scored := ExportRow{ID: 1, Topic: "demo score carry", Content: "합성 데모",
		Kind: "knowledge", Status: "active", Confidence: 0.9, Domain: "demo",
		Source: "agent", ContentHash: "scored-h", RecallScore: 4,
		LastRecalledAt: &exportTRecall}
	hashInput, rec, se := rowHashRec(scored)
	if se != nil {
		t.Fatalf("rowHashRec: %v", se)
	}
	if v, ok := rec["recall_score"].(int); !ok || v != 4 {
		t.Errorf("rec recall_score = %#v, want 4", rec["recall_score"])
	}
	at, ok := rec["last_recalled_at"].(time.Time)
	if !ok || !at.Equal(exportTRecall) {
		t.Errorf("rec last_recalled_at = %#v, want %v", rec["last_recalled_at"], exportTRecall)
	}
	for _, k := range []string{"recall_score", "last_recalled_at"} {
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
			t.Errorf("0/NULL 행 rec에 %q 키 설정 — 컬럼 기본값 폴백 위반", k)
		}
	}
}

// TestExportScoreColumnsReal — slow(실DB): 게이트 v11 적용 DB에서 스코어
// 실열 조회(원천 매핑 — 파생 아님)·jsonl 왕복 보존·미설정 행 폴백.
// SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시에만 실행(exportRealStore seam).
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
	// 왕복 — 실열에서 읽은 값이 jsonl을 경유해 보존된다.
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
