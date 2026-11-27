package db

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/synuns/flint/lib/sqlfs"
)

func slowEnsureSchemaV4Idempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	if err := r.EnsureSchemaV3(ctx); err != nil {
		t.Fatalf("EnsureSchemaV3: %v", err)
	}

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v4-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v4 row: %v", err)
	}
	old := ids[0]
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV4: %v", err)
	}
	var readCount int
	var lastRead *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, old).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("read v4 columns of existing row: %v", err)
	}
	if readCount != 0 {
		t.Errorf("existing row read_count = %d, want 0 (기본값)", readCount)
	}
	if lastRead != nil {
		t.Errorf("existing row last_read_at = %v, want null", lastRead)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "4" {
		t.Fatalf("schema_version = %q, want 4", version)
	}

	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV4 (want idempotent): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("re-read schema_version: %v", err)
	}
	if version != "4" {
		t.Fatalf("schema_version after second call = %q, want 4", version)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, old).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("re-read v4 columns: %v", err)
	}
	if readCount != 0 || lastRead != nil {
		t.Errorf("기존 행 값 변형: read_count=%d last_read_at=%v, want 0/null", readCount, lastRead)
	}
}

func slowEnsureSchemaV4FromV1(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v4-from1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("EnsureSchemaV4 from v1: %v", err)
	}
	var readCount int
	var lastRead *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("read v4 columns: %v", err)
	}
	if readCount != 0 || lastRead != nil {
		t.Errorf("v1 승격 행 read_count=%d last_read_at=%v, want 0/null", readCount, lastRead)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET read_count = read_count + 1, last_read_at = now()
		 WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("bump v4 columns: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count FROM knowledge WHERE id = $1`, ids[0]).Scan(&readCount); err != nil {
		t.Fatalf("re-read read_count: %v", err)
	}
	if readCount != 1 {
		t.Errorf("read_count after bump = %d, want 1", readCount)
	}
}

func slowEnsureSchemaGatesShortCircuitAbove(t *testing.T, r *Repo) {
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '3')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("버전 '3' 설정: %v", err)
	}
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("EnsureSchemaV2(상위 버전 DB): %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if version != "3" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 3(상위 버전에서 no-op)", version)
	}
}

func setVersion(t *testing.T, r *Repo, v string) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ('schema_version', $1)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
		t.Fatalf("버전 %q 설정: %v", v, err)
	}
}

func readVersion(t *testing.T, r *Repo) string {
	t.Helper()
	var v string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	return v
}

func slowV5PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v5-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v5 row: %v", err)
	}
	old := ids[0]
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV5: %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version = %q, want 5", v)
	}
	var sup *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, old).Scan(&sup); err != nil {
		t.Fatalf("read superseded_by of existing row: %v", err)
	}
	if sup != nil {
		t.Errorf("existing row superseded_by = %v, want null(기본값)", *sup)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET superseded_by = $2 WHERE id = $1`, old, old+1); err != nil {
		t.Fatalf("set superseded_by: %v", err)
	}

	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV5 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version after second call = %q, want 5", v)
	}
}

func slowV5GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func slowV4GateNoDowngradeFromV5(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5: %v", err)
	}
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("하위 게이트(v5 DB): %v", err)
		}
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("하위 게이트가 버전 되돌림: schema_version=%q, want 5", v)
	}
}

func slowFreshSchemaHasColumn(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx,
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 삭제: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DROP TABLE knowledge`); err != nil {
		t.Fatalf("knowledge 삭제: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("버전 삭제: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	if v := readVersion(t, r); v != "1" {
		t.Fatalf("신규 생성 후 schema_version = %q, want 1", v)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v5-fresh")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	var sup *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, ids[0]).Scan(&sup); err != nil {
		t.Fatalf("신규 설치 경로 superseded_by 조회: %v", err)
	}
	if sup != nil {
		t.Errorf("신규 행 superseded_by = %v, want null", *sup)
	}

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4, r.EnsureSchemaV5,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("게이트 체인: %v", err)
		}
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 5", v)
	}
}

func slowDupGroupsSameTopic(t *testing.T, r *Repo) {
	ctx := context.Background()
	a := knowledgeRec("dup-a")
	a["topic"] = "rv-e"
	b := knowledgeRec("dup-b")
	b["topic"] = "rv-e"
	c := knowledgeRec("dup-c")
	c["topic"] = "solo"
	ids, err := r.InsertBatch(ctx, []map[string]any{a, b, c})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	groups, err := r.DupGroups(ctx, 10)
	if err != nil {
		t.Fatalf("DupGroups: %v", err)
	}
	want := []DupGroup{{Topic: "rv-e", IDs: []int{ids[0], ids[1]}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("DupGroups = %+v, want %+v", groups, want)
	}

	if _, err := r.Invalidate(ctx, []int{ids[0]}, "테스트 무효화"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	groups, err = r.DupGroups(ctx, 10)
	if err != nil {
		t.Fatalf("DupGroups after invalidate: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("무효화 후 그룹 = %+v, want 빈", groups)
	}
}

func slowV6PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4, r.EnsureSchemaV5,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV6: %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version = %q, want 6", v)
	}

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 2, Session: "v6-probe"}); err != nil {
		t.Fatalf("RecordReadEvent on promoted table: %v", err)
	}
	var hits int
	var session string
	if err := r.pool.QueryRow(ctx,
		`SELECT hits, session FROM read_events WHERE session = $1`, "v6-probe").Scan(&hits, &session); err != nil {
		t.Fatalf("read_events probe row: %v", err)
	}
	if hits != 2 {
		t.Errorf("probe hits = %d, want 2", hits)
	}

	if _, err := r.pool.Exec(ctx, `DELETE FROM read_events WHERE session = $1`, "v6-probe"); err != nil {
		t.Fatalf("probe 정리: %v", err)
	}

	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV6 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version after second call = %q, want 6", v)
	}
}

func slowV6GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func slowV7PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1, Session: "v7-probe"}); err != nil {
		t.Fatalf("pre-v7 RecordReadEvent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM session_starts WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM hot_events WHERE ids = $1`, "v7-probe")
	})

	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV7: %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version = %q, want 7", v)
	}

	var ids string
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("기존 행 ids 가독: %v", err)
	}
	if ids != "" {
		t.Errorf("승격 전 행 ids = %q, want 빈 문자열(기본값)", ids)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 2, Session: "v7-probe", IDs: "7,9"}); err != nil {
		t.Fatalf("post-v7 RecordReadEvent(ids): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1 AND hits = 2`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("신규 행 ids 가독: %v", err)
	}
	if ids != "7,9" {
		t.Errorf("ids = %q, want 7,9", ids)
	}

	if err := r.RecordSessionStart(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordSessionStart: %v", err)
	}
	if err := r.RecordHotEvent(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordHotEvent: %v", err)
	}

	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV7 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version after second call = %q, want 7", v)
	}
}

func slowV7GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func slowInvalidateSetsStatus(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("inv-status-a"), knowledgeRec("inv-status-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	a, b := ids[0], ids[1]

	if _, err := r.Invalidate(ctx, []int{a}, "패치 루프 회귀"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	var status string
	var invalidAt *string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, invalid_at::text FROM knowledge WHERE id = $1`, a).
		Scan(&status, &invalidAt); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "invalidated" || invalidAt == nil {
		t.Fatalf("Invalidate 후 = {%s %v}, want {invalidated <시각>} — 배치 경로와 단일 법칙", status, invalidAt)
	}

	if err := r.Merge(ctx, b, a); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, b).Scan(&status); err != nil {
		t.Fatalf("Merge from 조회: %v", err)
	}
	if status != "invalidated" {
		t.Fatalf("Merge(from) 후 status = %q, want invalidated", status)
	}
}

func slowApproveClearsInvalidAt(t *testing.T, r *Repo) {
	ctx := context.Background()
	rec := knowledgeRec("approve-zombie")
	rec["status"] = "pending"
	ids, err := r.InsertBatch(ctx, []map[string]any{rec})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id := ids[0]

	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'invalidated', invalid_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("사전 무효화: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'pending' WHERE id = $1`, id); err != nil {
		t.Fatalf("pending 복귀(무효화 표시 잔존 상태 재현): %v", err)
	}

	if err := r.Approve(ctx, id, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	var status string
	var invalidAt *string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, invalid_at::text FROM knowledge WHERE id = $1`, id).
		Scan(&status, &invalidAt); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "active" || invalidAt != nil {
		t.Fatalf("승인 후 = {%s %v}, want {active <nil>} — 잔존 invalid_at 좀비", status, invalidAt)
	}

	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM active_knowledge WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("뷰 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("active 뷰 노출 %d건, want 1", n)
	}
}

var v9IndexNames = []string{
	"flint_read_events_ts_idx",
	"flint_hot_events_ts_idx",
	"flint_session_starts_ts_idx",
	"flint_knowledge_pending_idx",
}

func slowV9PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6, r.EnsureSchemaV7, r.EnsureSchemaV8,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v9-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v9 row: %v", err)
	}
	old := ids[0]
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1, Session: "v9-probe"}); err != nil {
		t.Fatalf("pre-v9 RecordReadEvent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, "v9-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM knowledge WHERE content_hash = $1`, "v9-old")
	})

	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV9: %v", err)
	}
	if v := readVersion(t, r); v != "9" {
		t.Fatalf("schema_version = %q, want 9", v)
	}

	for _, idx := range v9IndexNames {
		assertObject(t, r, ctx, idx)
	}

	assertPartialIndex(t, r, ctx, "flint_knowledge_pending_idx",
		[]string{"status = 'pending'", "invalid_at IS NULL"})

	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE id = $1`, old).Scan(&n); err != nil {
		t.Fatalf("기존 knowledge 행 조회: %v", err)
	}
	if n != 1 {
		t.Errorf("기존 행 유실: knowledge id=%d count = %d, want 1", old, n)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1`, "v9-probe").Scan(&n); err != nil {
		t.Fatalf("기존 read_events 행 조회: %v", err)
	}
	if n != 1 {
		t.Errorf("기존 행 유실: read_events count = %d, want 1", n)
	}

	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV9 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "9" {
		t.Fatalf("schema_version after second call = %q, want 9", v)
	}
}

func slowV9GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("EnsureSchemaV9(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func assertPartialIndex(t *testing.T, r *Repo, ctx context.Context, name string, conds []string) {
	t.Helper()
	var def string
	if err := r.pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`, name).Scan(&def); err != nil {
		t.Fatalf("인덱스 %s 정의 조회: %v", name, err)
	}
	parts := append([]string{"WHERE"}, conds...)
	for _, p := range parts {
		if !strings.Contains(def, p) {
			t.Errorf("인덱스 %s 정의에 %q 부재:\n got %q", name, p, def)
		}
	}
}

func columnExists(t *testing.T, r *Repo, table, column string) bool {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&n); err != nil {
		t.Fatalf("columns 조회 %s.%s: %v", table, column, err)
	}
	return n == 1
}

func slowV10GateAdditive(t *testing.T, r *Repo) {
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS knowledge_reads`); err != nil {
		t.Fatalf("v9 재현 knowledge_reads 삭제: %v", err)
	}
	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if columnExists(t, r, "read_events", col) {
			if _, err := r.pool.Exec(ctx,
				`ALTER TABLE read_events DROP COLUMN `+col); err != nil {
				t.Fatalf("v9 재현 read_events.%s 삭제: %v", col, err)
			}
		}
	}
	setVersion(t, r, "9")

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session)
		 VALUES (2, false, false, false, 'v10-pre')`); err != nil {
		t.Fatalf("seed pre-v10 read_event: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v10-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v10: %v", err)
	}

	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV10: %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("schema_version = %q, want 11", v)
	}

	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if !columnExists(t, r, "read_events", col) {
			t.Fatalf("read_events.%s 부재 — v10 additive 계약 위반", col)
		}
	}
	var bytes int
	var zeroReason, source string
	if err := r.pool.QueryRow(ctx,
		`SELECT bytes, zero_reason, source FROM read_events WHERE session = 'v10-pre'`).
		Scan(&bytes, &zeroReason, &source); err != nil {
		t.Fatalf("기존 read_event 행 조회: %v", err)
	}
	if bytes != 0 || zeroReason != "" || source != "" {
		t.Fatalf("기존 행 신규 열 = (%d,%q,%q), want (0,\"\",\"\",기본값)", bytes, zeroReason, source)
	}

	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'knowledge_reads'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("knowledge_reads 테이블 부재 — v10 계약 위반")
	}
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'knowledge_reads' AND index_name = 'flint_knowledge_reads_ts_idx'`).
		Scan(&idx); err != nil {
		t.Fatalf("knowledge_reads 인덱스 조회: %v", err)
	}
	if idx != 1 {
		t.Fatalf("flint_knowledge_reads_ts_idx 부재 — ts 인덱스 계약 위반")
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_reads (knowledge_id, session, kind) VALUES ($1, 'v10', 'read')`,
		ids[0]); err != nil {
		t.Fatalf("knowledge_reads 기록: %v", err)
	}

	var topic string
	if err := r.pool.QueryRow(ctx,
		`SELECT topic FROM knowledge WHERE id = $1`, ids[0]).Scan(&topic); err != nil {
		t.Fatalf("기존 knowledge 행 조회: %v", err)
	}
	if topic == "" {
		t.Fatalf("기존 knowledge 행 유실 — 무손상 계약 위반")
	}

	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV10 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("재호출 후 schema_version = %q, want 11", v)
	}
}

func slowV10GateNumeric(t *testing.T, r *Repo) {
	ctx := context.Background()
	setVersion(t, r, "99")
	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("EnsureSchemaV10(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}
}

func slowV10FreshSchemaHasArtifacts(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("신규 DB 재현 %q: %v", stmt, err)
		}
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	for _, gate := range SchemaGates() {
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	if v := readVersion(t, r); v != strconv.Itoa(LatestSchemaVersion) {
		t.Fatalf("게이트 체인 후 schema_version = %q, want %d(파생 — 상한 갱신 시 본 테스트도 따라간다)",
			v, LatestSchemaVersion)
	}
	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if !columnExists(t, r, "read_events", col) {
			t.Fatalf("신규 경로 read_events.%s 부재", col)
		}
	}
	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'knowledge_reads'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("신규 경로 knowledge_reads 부재")
	}
}

var backfillAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func slowV12GateRecallAppendOnly(t *testing.T, r *Repo) {
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS recall_events`); err != nil {
		t.Fatalf("v11 재현 recall_events 삭제: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS last_recalled_at timestamptz`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("v11 재현 컬럼 보증 %q: %v", stmt, err)
		}
	}
	setVersion(t, r, "11")

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v12-hot"), knowledgeRec("v12-cold"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET recall_score = 3, last_recalled_at = $2 WHERE id = $1`,
		ids[0], backfillAt); err != nil {
		t.Fatalf("hot 지식 스코어 설정: %v", err)
	}

	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV12: %v", err)
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("schema_version = %q, want 12", v)
	}

	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&n); err != nil {
		t.Fatalf("recall_events 카운트: %v", err)
	}
	if n != 3 {
		t.Fatalf("recall_events %d건, want 3(score수 합성 백필)", n)
	}
	var hotN, atN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, ids[0]).Scan(&hotN); err != nil {
		t.Fatalf("hot 지식 이벤트 카운트: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1 AND recalled_at = $2`,
		ids[0], backfillAt).Scan(&atN); err != nil {
		t.Fatalf("백필 시각 검증: %v", err)
	}
	if hotN != 3 || atN != 3 {
		t.Fatalf("hot 지식 이벤트 = %d건(시각 정합 %d), want 3(3) — last_recalled_at 원천 백필", hotN, atN)
	}

	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("knowledge.%s 잔존 — v12 컬럼 폐기 계약 위반", col)
		}
	}

	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'recall_events' AND index_name = 'idx_recall_events_id_time'`).
		Scan(&idx); err != nil {
		t.Fatalf("recall_events 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("idx_recall_events_id_time 부재 — 인덱스 계약 위반")
	}

	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV12 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("재호출 후 schema_version = %q, want 12", v)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&n); err != nil {
		t.Fatalf("재호출 후 recall_events 카운트: %v", err)
	}
	if n != 3 {
		t.Fatalf("재호출 후 recall_events %d건, want 3(멱등 — 중복 백필 없음)", n)
	}
}

func slowV12GateNumeric(t *testing.T, r *Repo) {
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("recall_score 보증: %v", err)
	}
	setVersion(t, r, "99")
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}

	if !columnExists(t, r, "knowledge", "recall_score") {
		t.Fatalf("상위 버전 DB에서 knowledge.recall_score 폐기 — no-op 계약 위반")
	}
}

func slowV12FreshSchemaHasRecallEvents(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("신규 DB 재현 %q: %v", stmt, err)
		}
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	for _, gate := range SchemaGates() {
		if gate.Version > 12 {

			break
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 12", v)
	}
	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'recall_events'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("신규 경로 recall_events 부재")
	}
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'recall_events' AND index_name = 'idx_recall_events_id_time'`).
		Scan(&idx); err != nil {
		t.Fatalf("신규 경로 recall_events 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("신규 경로 idx_recall_events_id_time 부재")
	}
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 경로 knowledge.%s 잔존 — v12 승격 후 폐기 필요", col)
		}
	}
}

func slowV11GateFreshDirectToV12(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("신규 DB 재현 %q: %v", stmt, err)
		}
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	for _, gate := range SchemaGates() {
		if gate.Version > 11 {
			break
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}

	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 DB v11 게이트 직후 knowledge.%s add — add→drop 왕복(R43 직행 위반)", col)
		}
	}
	var hasEvents int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'recall_events'`).
		Scan(&hasEvents); err != nil {
		t.Fatal(err)
	}
	if hasEvents != 1 {
		t.Fatalf("신규 DB v11 게이트 후 recall_events 부재 — 직행 판별 원천 훼손")
	}
	if v := readVersion(t, r); v != "11" {
		t.Fatalf("v11 게이트 후 schema_version = %q, want 11", v)
	}

	for _, gate := range SchemaGates() {
		if gate.Version != 12 {
			continue
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 v12: %v", err)
		}
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("v12 게이트 후 schema_version = %q, want 12", v)
	}
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 DB v12 게이트 후 knowledge.%s 잔존", col)
		}
	}
}

func TestSlowV15GateDedupeUniqueIndex(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx, `DROP INDEX IF EXISTS uq_knowledge_links_triple`); err != nil {
		t.Fatalf("v14 재현 인덱스 삭제: %v", err)
	}
	setVersion(t, r, "14")

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-seal-a"), knowledgeRec("v1201-seal-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	for _, rel := range []string{"refines", "refines", "supports"} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, $3)`,
			ids[0], ids[1], rel); err != nil {
			t.Fatalf("중복 링크 심기: %v", err)
		}
	}
	if err := r.EnsureSchemaV15(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV15: %v", err)
	}
	if v := readVersion(t, r); v != "15" {
		t.Fatalf("schema_version=%q want 15", v)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_links WHERE from_id = $1`, ids[0]).Scan(&n); err != nil {
		t.Fatalf("링크 수 조회: %v", err)
	}
	if n != 2 {
		t.Fatalf("링크 수=%d want 2 (duplicates collapsed)", n)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err == nil {
		t.Fatal("중복 triple INSERT 허용 — unique 봉인 미적용")
	}

	if err := r.EnsureSchemaV15(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV15 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "15" {
		t.Fatalf("재호출 후 schema_version=%q want 15", v)
	}
}

func isSequencingUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "XX000" {
		return false
	}
	named := pgErr.ConstraintName == "uq_knowledge_links_triple" ||
		strings.Contains(pgErr.Message, "uq_knowledge_links_triple")
	return named && strings.Contains(pgErr.Message, "transaction sequencing")
}

func retryParallelInsert(ctx context.Context, r *Repo, query string, args ...any) (int, error) {
	const attempts = 4
	var err error
	tries := 0
	for i := 0; i < attempts; i++ {
		tries++
		if _, err = r.pool.Exec(ctx, query, args...); err == nil || !isSequencingUniqueViolation(err) {
			return tries, err
		}
		time.Sleep(time.Duration(i+1) * 25 * time.Millisecond)
	}
	return tries, err
}

func runParallelIdempotentInsert(ctx context.Context, r *Repo, fromID, toID int) ([]error, int) {
	var wg sync.WaitGroup
	errs := make([]error, 5)
	var retries atomic.Int64
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t, err := retryParallelInsert(ctx, r,
				`INSERT INTO knowledge_links (from_id, to_id, relation)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (from_id, to_id, relation) DO NOTHING`,
				fromID, toID, "refines")
			errs[i] = err
			retries.Add(int64(t) - 1)
		}(i)
	}
	wg.Wait()
	return errs, int(retries.Load())
}

func TestSequencingViolationClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"커밋 시퀀싱 유니크 위반(XX000·uq triple·sequencing)은 재시도 대상", &pgconn.PgError{
			Code: "XX000",
			Message: "Committing this transaction resulted in a working set with constraint violations ... " +
				"Unique Key Constraint Violation, Name: uq_knowledge_links_triple. " +
				"This constraint violation may be the result of a previous merge or the result of transaction sequencing",
		}, true},
		{"ConstraintName 필드 매칭도 재시도 대상", &pgconn.PgError{
			Code:           "XX000",
			Message:        "constraint violations ... the result of transaction sequencing",
			ConstraintName: "uq_knowledge_links_triple",
		}, true},
		{"XX000이라도 uq triple이 아니면 전파", &pgconn.PgError{
			Code: "XX000", Message: "Unique Key Constraint Violation, Name: other_key — transaction sequencing",
		}, false},
		{"XX000이라도 sequencing 근거 없으면 전파(마스킹 금지)", &pgconn.PgError{
			Code:           "XX000",
			Message:        "Unique Key Constraint Violation, Name: uq_knowledge_links_triple (2 row(s))",
			ConstraintName: "uq_knowledge_links_triple",
		}, false},
		{"23505 일반 unique 위반은 전파", &pgconn.PgError{
			Code: "23505", Message: `duplicate key value violates unique constraint "uq_knowledge_links_triple"`,
		}, false},
		{"pgconn.PgError 외 오류는 전파", fmt.Errorf("plain error"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSequencingUniqueViolation(tc.err); got != tc.want {
				t.Fatalf("isSequencingUniqueViolation = %v, want %v (err=%v)", got, tc.want, tc.err)
			}
		})
	}
}

func TestSlowParallelLinkInsertSingleRowV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-par-link-a"), knowledgeRec("v1201-par-link-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	errs, retries := runParallelIdempotentInsert(ctx, r, ids[0], ids[1])
	for i, err := range errs {
		if err != nil {
			if isSequencingUniqueViolation(err) {
				t.Fatalf("병렬 INSERT[%d]: 커밋 시퀀싱 XX000 재시도 소진(4회): %v", i, err)
			}
			t.Fatalf("병렬 INSERT[%d]: %v", i, err)
		}
	}
	if retries > 0 {
		t.Logf("시퀀싱 경합 관측: 재시도 %d회 — 멱등 패자 재시도 계약 동작", retries)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
		ids[0], ids[1]).Scan(&n); err != nil {
		t.Fatalf("링크 수 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("동일 링크 행=%d want 1 (ON CONFLICT 병렬 멱등)", n)
	}
}

const slowLoopsDefault = 100
const slowLoopsCap = 1000

func slowLoopsFromEnv(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("FLINT_SLOW_LOOPS=%q: 양의 정수 필요(1~%d, 상한 초과분은 봉입)", v, slowLoopsCap)
	}
	if n > slowLoopsCap {
		return slowLoopsCap, nil
	}
	return n, nil
}

func TestSlowLoopsCapClamp(t *testing.T) {
	n, err := slowLoopsFromEnv("5000")
	if err != nil || n != slowLoopsCap {
		t.Fatalf("FLINT_SLOW_LOOPS=5000 → (%d, %v), want (%d, nil) — 상한 봉입 미적용", n, err, slowLoopsCap)
	}
	if n, err := slowLoopsFromEnv("1000"); err != nil || n != 1000 {
		t.Fatalf("FLINT_SLOW_LOOPS=1000 → (%d, %v), want (1000, nil)", n, err)
	}
	if n, err := slowLoopsFromEnv("1"); err != nil || n != 1 {
		t.Fatalf("FLINT_SLOW_LOOPS=1 → (%d, %v), want (1, nil)", n, err)
	}
	if _, err := slowLoopsFromEnv("0"); err == nil {
		t.Fatal("FLINT_SLOW_LOOPS=0 허용 — 양의 정수 계약 위반")
	}
	if _, err := slowLoopsFromEnv("abc"); err == nil {
		t.Fatal("FLINT_SLOW_LOOPS=abc 허용 — 양의 정수 계약 위반")
	}
}

func TestSlowSuiteSerialGreen(t *testing.T) {
	loops := slowLoopsDefault
	if v := os.Getenv("FLINT_SLOW_LOOPS"); v != "" {
		n, err := slowLoopsFromEnv(v)
		if err != nil {
			t.Fatalf("%v", err)
		}
		loops = n
	}
	r := newTestRepo(t)
	ctx := context.Background()
	rounds := make([]int, 0, loops)
	for round := 0; round < loops; round++ {
		ids, err := r.InsertBatch(ctx, []map[string]any{
			knowledgeRec(fmt.Sprintf("v1201-dist-a-%04d", round)),
			knowledgeRec(fmt.Sprintf("v1201-dist-b-%04d", round)),
		})
		if err != nil {
			t.Fatalf("round %d InsertBatch: %v", round, err)
		}
		errs, retries := runParallelIdempotentInsert(ctx, r, ids[0], ids[1])
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d 병렬 INSERT[%d]: %v (커밋 시퀀싱 재시도 소진 포함 — 100회 루프 분포 red)", round, i, err)
			}
		}
		rounds = append(rounds, retries)
		var n int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
			ids[0], ids[1]).Scan(&n); err != nil {
			t.Fatalf("round %d 링크 수 조회: %v", round, err)
		}
		if n != 1 {
			t.Fatalf("round %d 동일 링크 행=%d want 1 (병렬 멱등)", round, n)
		}
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
			ids[0], ids[1]); err != nil {
			t.Fatalf("round %d 링크 정리: %v", round, err)
		}
		if _, err := r.pool.Exec(ctx,
			fmt.Sprintf(`DELETE FROM knowledge WHERE id IN (%d, %d)`, ids[0], ids[1])); err != nil {
			t.Fatalf("round %d 지식 행 정리: %v", round, err)
		}
	}
	contentious := 0
	for _, retries := range rounds {
		if retries > 0 {
			contentious++
		}
	}
	t.Logf("분포 수집(%d회 루프): 시퀀싱 경합 라운드 %d/%d — red 0건", loops, contentious, loops)
}

func TestSlowSupersedeCrossRaceV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for round := 0; round < 20; round++ {
		ids, err := r.InsertBatch(ctx, []map[string]any{
			knowledgeRec(fmt.Sprintf("v1201-race-a-%02d", round)),
			knowledgeRec(fmt.Sprintf("v1201-race-b-%02d", round)),
		})
		if err != nil {
			t.Fatalf("round %d InsertBatch: %v", round, err)
		}
		a, b := ids[0], ids[1]
		res := make([]error, 2)
		var wg sync.WaitGroup
		for i, pair := range [][2]int{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i int, oldID, newID int) {
				defer wg.Done()
				_, res[i] = r.Supersede(ctx, oldID, newID, at)
			}(i, pair[0], pair[1])
		}
		wg.Wait()
		wins := 0
		for i, err := range res {
			if err == nil {
				wins++
				continue
			}
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("round %d loser[%d] err=%v want ErrNotFound steward", round, i, err)
			}
		}
		if wins != 1 {
			t.Fatalf("round %d winners=%d want exactly 1 (both=%d is a planted cycle; zero is a lost write)",
				round, wins, 2-wins)
		}

		var supA, supB *int
		if err := r.pool.QueryRow(ctx,
			`SELECT (SELECT superseded_by FROM knowledge WHERE id=$1),
			        (SELECT superseded_by FROM knowledge WHERE id=$2)`, a, b).Scan(&supA, &supB); err != nil {
			t.Fatalf("round %d lineage 조회: %v", round, err)
		}
		if supA != nil && supB != nil && *supA == b && *supB == a {
			t.Fatalf("round %d: 2-cycle 적립 (a→b && b→a)", round)
		}
	}
}

func TestSlowSupersedeChainDepthCapV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	at := time.Now().UTC()
	const hops = 500
	recs := make([]map[string]any, hops)
	for i := range recs {
		recs[i] = knowledgeRec(fmt.Sprintf("v1201-chain-%03d", i))
	}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	for i := 0; i+1 < hops; i++ {
		if _, err := r.Supersede(ctx, ids[i], ids[i+1], at); err != nil {
			t.Fatalf("chain hop %d: %v", i, err)
		}
	}

	_, err = r.WouldCreateCycle(ctx, ids[hops-1], ids[0])
	if err == nil {
		t.Fatal("500홉 체인 꼬리→헤드: no-cycle 판정 — 보수 거부 계약 위반")
	}
	if !strings.Contains(err.Error(), "depth cap") {
		t.Fatalf("err=%v want depth-cap refusal diagnostic", err)
	}

	if _, err := r.Supersede(ctx, ids[hops-1], ids[0], at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tail→head Supersede err=%v want ErrNotFound (refused)", err)
	}
}

func v20SetPre(t *testing.T, r *Repo) context.Context {
	t.Helper()
	ctx := context.Background()
	setVersion(t, r, "19")
	return ctx
}

func v20Gate(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	if err := r.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("EnsureSchemaV20: %v", err)
	}
	if v := readVersion(t, r); v != "20" {
		t.Fatalf("schema_version = %q, want 20", v)
	}
}

func TestV20BackfillClaimsAllExistingRowsAsEvent(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v20-claim-a"), knowledgeRec("v20-claim-b"), knowledgeRec("v20-claim-c"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("ids %d건, want 3", len(ids))
	}

	v20Gate(t, r, ctx)

	var unclassified int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = '' OR claim IS NULL`).Scan(&unclassified); err != nil {
		t.Fatalf("미분류 카운트: %v", err)
	}
	if unclassified != 0 {
		t.Fatalf("claim 미분류 %d건 — 기존 행 backfill 누락", unclassified)
	}
	var eventN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = 'event'`).Scan(&eventN); err != nil {
		t.Fatalf("event 카운트: %v", err)
	}
	if eventN != 3 {
		t.Fatalf("claim='event' %d건, want 3", eventN)
	}

	if err := r.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("재실행 EnsureSchemaV20(멱등): %v", err)
	}
}

func TestV20PartialUniqueRejectsSecondOpenSameHash(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)
	v20Gate(t, r, ctx)

	var projectID int
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ('v20-demo') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("project 생성: %v", err)
	}
	first := `INSERT INTO work_state (project_id, task, state_hash) VALUES ($1, 'v20 demo task', 'hash-v20')`
	if _, err := r.pool.Exec(ctx, first, projectID); err != nil {
		t.Fatalf("1차 open INSERT: %v", err)
	}
	if _, err := r.pool.Exec(ctx, first, projectID); !isPgCode(err, "23505") {
		t.Fatalf("2차 open INSERT err = %v, want unique violation(23505)", err)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash, status) VALUES ($1, 'v20 demo task', 'hash-v20', 'resolved')`,
		projectID); err != nil {
		t.Fatalf("동일 해시 resolved INSERT 허용되어야 함: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (task, state_hash) VALUES ('v20 orphan task', 'hash-orphan')`); err != nil {
		t.Fatalf("project_id NULL open INSERT: %v", err)
	}
}

func TestV20ViewsExcludeEvent(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v20-view-decision"), knowledgeRec("v20-view-event"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	v20Gate(t, r, ctx)

	var eventN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = 'event'`).Scan(&eventN); err != nil {
		t.Fatalf("backfill event 카운트: %v", err)
	}
	if eventN != 2 {
		t.Fatalf("backfill 후 claim='event' %d건, want 2", eventN)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET claim = 'decision' WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("decision 재분류: %v", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, topic, summary, tags, entity, domain, claim, confidence, conditions
		 FROM recall_knowledge`)
	if err != nil {
		t.Fatalf("recall_knowledge 질의: %v", err)
	}
	defer rows.Close()
	seen := map[int]string{}
	for rows.Next() {
		var id int
		var topic, summary, tags, domain, claim, conditions string
		var entity *string
		var confidence float64
		if err := rows.Scan(&id, &topic, &summary, &tags, &entity, &domain, &claim, &confidence, &conditions); err != nil {
			t.Fatalf("recall_knowledge 스캔: %v", err)
		}
		seen[id] = claim
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recall_knowledge rows: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("recall_knowledge %d건, want 1(event 제외)", len(seen))
	}
	if got, ok := seen[ids[0]]; !ok || got != "decision" {
		t.Fatalf("recall_knowledge에 decision 행 부재(또는 claim=%q)", got)
	}

	var projectID int
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ('v20-view-demo') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("project 생성: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash) VALUES ($1, 'v20 view task', 'hash-view')`, projectID); err != nil {
		t.Fatalf("open work_state 생성: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash, status) VALUES ($1, 'v20 resolved task', 'hash-view-r', 'resolved')`,
		projectID); err != nil {
		t.Fatalf("resolved work_state 생성: %v", err)
	}

	var gotID int
	var gotProjectID *int
	var gotTask, gotDetail, gotNextAction string
	var gotUpdated time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT id, project_id, task, detail, next_action, updated_at FROM open_work_state`).
		Scan(&gotID, &gotProjectID, &gotTask, &gotDetail, &gotNextAction, &gotUpdated); err != nil {
		t.Fatalf("open_work_state 질의: %v", err)
	}
	if gotTask != "v20 view task" {
		t.Fatalf("open_work_state task = %q, want v20 view task(resolved 제외)", gotTask)
	}
	if gotProjectID == nil || *gotProjectID != projectID {
		t.Fatalf("open_work_state project_id = %v, want %d", gotProjectID, projectID)
	}
	if gotDetail != "" || gotNextAction != "" {
		t.Fatalf("open_work_state 기본값 detail=%q next_action=%q, want 빈 문자열", gotDetail, gotNextAction)
	}
}

func TestAdminVersionsCountInvariant(t *testing.T) {
	want := map[string]bool{
		"TestV20BackfillClaimsAllExistingRowsAsEvent":   true,
		"TestV20PartialUniqueRejectsSecondOpenSameHash": true,
		"TestV20ViewsExcludeEvent":                      true,
		"TestSlowV15GateDedupeUniqueIndex":              true,
		"TestSequencingViolationClassification":         true,
		"TestSlowParallelLinkInsertSingleRowV1201":      true,
		"TestSlowLoopsCapClamp":                         true,
		"TestSlowSuiteSerialGreen":                      true,
		"TestSlowSupersedeCrossRaceV1201":               true,
		"TestSlowSupersedeChainDepthCapV1201":           true,
	}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("테스트 파일 조사 실패: %v", err)
	}
	fset := token.NewFileSet()
	got := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s 파싱 실패: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if want[fd.Name.Name] {
				got++
			}
		}
	}
	if got != len(want) {
		t.Fatalf("admin_v 계열 카운트 = %d, want %d(통합 전후 불변)", got, len(want))
	}
	leftover, err := filepath.Glob("admin_v[0-9]*_test.go")
	if err != nil {
		t.Fatalf("접미 잔여 조사 실패: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("admin_v 접미 파일 잔여 %d건: %v", len(leftover), leftover)
	}
}
