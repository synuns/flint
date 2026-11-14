// import_jsonl_test.go — v1.3.0 T9: import --format jsonl 왕복 재이식
// 테스트. 전부 합성 데모 데이터(개인 데이터 금지 — CLAUDE.md 최상위 원칙).
// 검증 순서: 왕복(export→import→imported=N)·멱등 2회(duplicated=N)·
// 계보 복원(superseded_by 갱신)·파손 행 exit 3·무효 format exit 3·
// 서버다운 exit 2·pam seam. 실DB 왕복은 cli/import_migrate_test.go slow.
package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// migrateFixtureRows — 합성 export 스냅샷 2행(신규 active 1·계보
// invalidated 1 — superseded_by로 active 행을 가리킨다).
func migrateFixtureRows() []ExportRow {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	sup := 11
	// ContentHash는 저장 해시 승계(A9) 검증을 위해 64 hex 서로 다른 값.
	return []ExportRow{
		{
			ID: 11, Topic: "demo migrate alpha", Content: "합성 데모 지식 본문 alpha — 왕복 검증용.",
			Kind: "knowledge", Status: "active", Confidence: 0.9,
			Tags: []string{"alpha", "demo"}, Entities: []string{"svc-demo"},
			Domain: "main", Source: "agent",
			ContentHash: "1111111111111111111111111111111111111111111111111111111111111111",
			CreatedAt:   t0, UpdatedAt: t0,
		},
		{
			ID: 22, Topic: "demo migrate beta", Content: "합성 데모 지식 본문 beta — 계보 복원용.",
			Kind: "knowledge", Status: "invalidated", Confidence: 0.8,
			Tags: []string{"demo", "beta"}, Entities: nil,
			Domain: "main", Source: "agent",
			ContentHash: "2222222222222222222222222222222222222222222222222222222222222222",
			CreatedAt:   t0, UpdatedAt: t0, SupersededBy: &sup,
		},
	}
}

// migrateFake — Supersede·SetSupersededRef capability를 더한 fakeStore
// (기본 fake은 wire.Store만 구현 — 전이·참조 복원 경로 검증).
type migrateFake struct {
	*fakeStore
	supersedeErr error
	superseded   []supersedeCall
	refCalls     [][2]int
}

func (m *migrateFake) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	m.superseded = append(m.superseded, supersedeCall{oldID: oldID, newID: newID})
	if m.supersedeErr != nil {
		return 0, m.supersedeErr
	}
	return 1, nil
}

func (m *migrateFake) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	m.refCalls = append(m.refCalls, [2]int{oldID, newID})
	return 1, nil
}

// InsertBatch — rec에 명시 id가 있으면 그 id를 돌려준다(실구현
// RETURNING id와 정합 — id 보존 경로 검증). 미명시 행은 fake 관례 순차.
func (m *migrateFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	m.fakeStore.inserted = append(m.fakeStore.inserted, recs...)
	if m.fakeStore.insertErr != nil {
		return nil, m.fakeStore.insertErr
	}
	ids := make([]int, len(recs))
	for i, r := range recs {
		if id, ok := r["id"].(int); ok && id > 0 {
			ids[i] = id
		} else {
			ids[i] = i + 1
		}
	}
	return ids, nil
}

// runMigrate — RunImportMigrate 직접 실행 + 출력 반환.
func runMigrate(t *testing.T, st wire.Store, args []string, stdin string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunImportMigrate(context.Background(), st, args, strings.NewReader(stdin), &out)
	return out.String(), code
}

// TestParseJSONLRowsRoundtrip — FormatJSONL(T3) 출력을 ParseJSONLRows가
// 전 필드 보존해 되돌린다(왕복 파싱 계약).
func TestParseJSONLRowsRoundtrip(t *testing.T) {
	rows := migrateFixtureRows()
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatalf("FormatJSONL: %v", err)
	}
	got, err := ParseJSONLRows(data)
	if err != nil {
		t.Fatalf("ParseJSONLRows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows=%d, want 2", len(got))
	}
	if got[0].ID != 11 || got[0].Topic != "demo migrate alpha" ||
		got[0].Content != rows[0].Content || got[0].Status != "active" ||
		got[0].Confidence != 0.9 || got[0].Source != "agent" {
		t.Fatalf("행0 왕복 필드 손실: %+v", got[0])
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "alpha" || got[0].Tags[1] != "demo" {
		t.Fatalf("행0 tags 왕복: %v", got[0].Tags)
	}
	if got[1].SupersededBy == nil || *got[1].SupersededBy != 11 {
		t.Fatalf("행1 superseded_by 왕복: %+v", got[1].SupersededBy)
	}
	if got[1].Status != "invalidated" {
		t.Fatalf("행1 status 왕복: %q", got[1].Status)
	}
}

// TestParseJSONLRowsCorruptLine — 파손 행은 행 번호를 포함한 오류.
func TestParseJSONLRowsCorruptLine(t *testing.T) {
	data := []byte(`{"id":1,"topic":"ok"}
{"id":not-json}
`)
	_, err := ParseJSONLRows(data)
	if err == nil {
		t.Fatal("파손 jsonl이 오류 없이 통과")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("행 번호 미포함: %v", err)
	}
}

// TestRunImportMigrateJSONLRoundtrip — 재이식: imported=2·id 보존·
// 정규화된 rec 적립. 요약은 stdout 1행.
func TestRunImportMigrateJSONLRoundtrip(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows())
	if err != nil {
		t.Fatal(err)
	}
	f := &migrateFake{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"ok"`) || !strings.Contains(out, `"imported":2`) ||
		!strings.Contains(out, `"duplicated":0`) || !strings.Contains(out, `"superseded":1`) {
		t.Fatalf("요약 출력: %s", out)
	}
	if len(f.inserted) != 2 {
		t.Fatalf("적립=%d, want 2", len(f.inserted))
	}
	if id, _ := f.inserted[0]["id"].(int); id != 11 {
		t.Fatalf("id 보존 위반: inserted[0].id=%v", f.inserted[0]["id"])
	}
	if tags, _ := f.inserted[0]["tags"].(string); tags != ":alpha:demo:" {
		t.Fatalf("tags 정규화 미적립: %q", tags)
	}
	if f.inserted[1]["status"] != "invalidated" {
		t.Fatalf("status 보존 위반: %v", f.inserted[1]["status"])
	}
	if len(f.commits) == 0 {
		t.Fatal("변경 후 dolt 커밋 부재")
	}
}

// TestRunImportMigrateJSONLFileArg — FILE 위치 인자 경로(stdin 대신).
func TestRunImportMigrateJSONLFileArg(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows()[:1])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "demo.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format=jsonl", path}, "")
	if code != 0 || !strings.Contains(out, `"imported":1`) {
		t.Fatalf("FILE 경로 exit=%d out=%s", code, out)
	}
}

// TestRunImportMigrateJSONLIdempotent — 2회 실행: 재실행은 전 행
// duplicate 스킵·exit 0(멱등).
func TestRunImportMigrateJSONLIdempotent(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 || !strings.Contains(out, `"imported":2`) {
		t.Fatalf("1회차 exit=%d out=%s", code, out)
	}
	f.exists = true // 모든 해시 존재 → 전 행 duplicate
	out, code = runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("2회차 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":0`) || !strings.Contains(out, `"duplicated":2`) {
		t.Fatalf("멱등 위반: %s", out)
	}
	if len(f.inserted) != 2 { // 1회차 2건만 — 2회차 적립 없음
		t.Fatalf("2회차 재적립: %d", len(f.inserted))
	}
}

// TestRunImportMigrateJSONLLineage — 계보 복원: superseded_by 참조 행은
// 후처리로 갱신된다. 전이 경로(Supersede 재사용)·참조 복원 경로
// (SetSupersededRef — ErrNotFound 폴백·전이 미구현 store)·무Capability
// 스킵.
func TestRunImportMigrateJSONLLineage(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("supersede capability", func(t *testing.T) {
		m := &migrateFake{fakeStore: &fakeStore{}}
		out, code := runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
		if code != 0 || !strings.Contains(out, `"superseded":1`) {
			t.Fatalf("exit=%d out=%s", code, out)
		}
		if len(m.superseded) != 1 || m.superseded[0].oldID != 22 || m.superseded[0].newID != 11 {
			t.Fatalf("Supersede 호출: %+v", m.superseded)
		}
		if len(m.refCalls) != 0 {
			t.Fatalf("전이 성공 경로에 참조 폴백 잔호출: %v", m.refCalls)
		}
	})
	t.Run("not-found fallback ref", func(t *testing.T) {
		m := &migrateFake{fakeStore: &fakeStore{},
			supersedeErr: fmt.Errorf("%w: supersede: 경합", db.ErrNotFound)}
		out, code := runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
		if code != 0 || !strings.Contains(out, `"superseded":1`) {
			t.Fatalf("exit=%d out=%s", code, out)
		}
		if len(m.refCalls) != 1 || m.refCalls[0] != [2]int{22, 11} {
			t.Fatalf("SetSupersededRef 폴백: %v", m.refCalls)
		}
	})
	t.Run("ref-only store", func(t *testing.T) {
		r := &refFake{fakeStore: &fakeStore{}}
		out, code := runMigrate(t, r, []string{"--format", "jsonl"}, string(data))
		if code != 0 || !strings.Contains(out, `"superseded":1`) {
			t.Fatalf("exit=%d out=%s", code, out)
		}
		if len(r.refCalls) != 1 || r.refCalls[0] != [2]int{22, 11} {
			t.Fatalf("SetSupersededRef 호출: %v", r.refCalls)
		}
	})
	t.Run("no capability skips", func(t *testing.T) {
		f := &fakeStore{} // 전이·참조 복원 미구현 — 경고 후 스킵
		out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
		if code != 0 || !strings.Contains(out, `"superseded":0`) {
			t.Fatalf("exit=%d out=%s", code, out)
		}
		if len(f.inserted) != 2 {
			t.Fatalf("적립은 완료되어야 한다: %d", len(f.inserted))
		}
	})
}

// refFake — SetSupersededRef만 구현한 fake(전이 미구현 store 경로).
// zeroRows=true면 0행(이미 참조 존재)을 반환한다.
type refFake struct {
	*fakeStore
	zeroRows bool
	refCalls [][2]int
}

func (r *refFake) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	r.refCalls = append(r.refCalls, [2]int{oldID, newID})
	if r.zeroRows {
		return 0, nil
	}
	return 1, nil
}

// InsertBatch — rec에 명시 id가 있으면 그 id를 돌려준다(migrateFake와
// 동일 — 실구현 RETURNING id 정합).
func (r *refFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	r.fakeStore.inserted = append(r.fakeStore.inserted, recs...)
	if r.fakeStore.insertErr != nil {
		return nil, r.fakeStore.insertErr
	}
	ids := make([]int, len(recs))
	for i, rec := range recs {
		if id, ok := rec["id"].(int); ok && id > 0 {
			ids[i] = id
		} else {
			ids[i] = i + 1
		}
	}
	return ids, nil
}

// TestRunImportMigrateInvalidArgs — 무효 format·값 누락·미확인 플래그·
// 읽기 불가 FILE은 전부 exit 3(무효 인자 계약).
func TestRunImportMigrateInvalidArgs(t *testing.T) {
	cases := [][]string{
		{"--format", "bogus"},
		{"--format"},
		{"--format=jsonl", "extra.jsonl", "more.jsonl"},
		{"--format", "jsonl", "--wat"},
		{"--format", "jsonl", "/nonexistent/demo.jsonl"},
	}
	f := &fakeStore{}
	for _, args := range cases {
		out, code := runMigrate(t, f, args, "{}\n")
		if code != 3 {
			t.Fatalf("args=%v exit=%d, want 3; out=%s", args, code, out)
		}
	}
}

// TestRunImportMigrateServerDown — 서버다운 exit 2(MapExit 재사용).
func TestRunImportMigrateServerDown(t *testing.T) {
	f := &fakeStore{pingErr: store.ErrServerDown}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, "")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; out=%s", code, out)
	}
}

// TestRunImportMigratePAMUnsupported — ParsePAM seam 미준비(nil)면
// 미지원 오류 exit 3.
func TestRunImportMigratePAMUnsupported(t *testing.T) {
	prev := ParsePAMFn
	ParsePAMFn = nil
	t.Cleanup(func() { ParsePAMFn = prev })
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "pam"}, pamSample)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "pam import 미지원") {
		t.Fatalf("미지원 문구 부재: %s", out)
	}
}

// TestRunImportMigratePAMImport — seam 준비 시 PAM 후보 pending 적립 +
// 멱등(재실행 duplicate).
func TestRunImportMigratePAMImport(t *testing.T) {
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "pam"}, pamSample)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":1`) || !strings.Contains(out, `"duplicated":0`) {
		t.Fatalf("요약: %s", out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립=%d, want 1", len(f.inserted))
	}
	rec := f.inserted[0]
	if rec["status"] != "pending" {
		t.Fatalf("pam pending 적립 위반: %v", rec["status"])
	}
	if origin, _ := rec["origin"].(string); origin != "import:pam:claude" {
		t.Fatalf("pam origin: %v", rec["origin"])
	}
	if topic, _ := rec["topic"].(string); !strings.Contains(topic, "mem-demo-1") {
		t.Fatalf("pam topic 파생: %q", topic)
	}
	f.exists = true
	out, code = runMigrate(t, f, []string{"--format", "pam"}, pamSample)
	if code != 0 || !strings.Contains(out, `"duplicated":1`) {
		t.Fatalf("pam 멱등 위반: exit=%d out=%s", code, out)
	}
}

// TestRunImportMigratePAMSkippedSummary — 스킵(status 비적립 대상) 후보는
// 요약 skipped로 보고(연결 계약 — Skipped 요약 포함).
func TestRunImportMigratePAMSkippedSummary(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id": "mem-demo-on", "type": "fact", "content": "active demo candidate", "status": "active"},
  {"id": "mem-demo-off", "type": "fact", "content": "superseded demo memory", "status": "superseded"}
 ],
 "relations": []
}`
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "pam"}, doc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":1`) || !strings.Contains(out, `"skipped":1`) {
		t.Fatalf("skipped 요약: %s", out)
	}
}

// TestRunImportMigrateJSONLSanitizes — 외부 파일 원천 살균(ADR-0006):
// summary 제어 문자·"[flint]" 마커·tags 원소 제어 문자는 적립 rec에서
// 중립화된다(write 파이프라인과 동일 — 살균 결과가 저장 본문).
func TestRunImportMigrateJSONLSanitizes(t *testing.T) {
	rows := []ExportRow{{
		ID: 31, Topic: "demo sanitize", Kind: "knowledge", Status: "active",
		Content:    "합성 데모\x00본문 [flint] 마커 포함",
		Confidence: 0.7, Tags: []string{"de\x00mo"}, Domain: "main",
		Source: "agent",
	}}
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립=%d, want 1", len(f.inserted))
	}
	summary, _ := f.inserted[0]["summary"].(string)
	if strings.Contains(summary, "\x00") || strings.Contains(summary, "[flint]") {
		t.Fatalf("summary 무살균 적립: %q", summary)
	}
	tags, _ := f.inserted[0]["tags"].(string)
	if tags != ":demo:" {
		t.Fatalf("tags 원소 무살균 적립: %q", tags)
	}
}

// TestRunImportMigrateJSONLOriginPreserved — jsonl source(origin)는
// Normalize 기본값("agent")으로 덮어쓰지 않는다(왕복 dedup 해시 대칭 —
// done 훅 적립 origin="hook" 행의 재이식 duplicate 판정 원천).
func TestRunImportMigrateJSONLOriginPreserved(t *testing.T) {
	rows := []ExportRow{{
		ID: 41, Topic: "demo origin hook", Kind: "knowledge", Status: "active",
		Content:    "합성 데모 — done 훅 적립 origin 보존 검증.",
		Confidence: 0.8, Domain: "main", Source: "hook",
	}}
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if origin, _ := f.inserted[0]["origin"].(string); origin != "hook" {
		t.Fatalf("origin = %q, want \"hook\"(기본값 덮어쓰기 — 왕복 해시 붕괴)", origin)
	}
}

// TestRunImportRoutedDelegates — 라우팅: --format 없으면 기존 RunImport
// 규칙 변환 경로 불변, --format 있으면 migrate 경로.
func TestRunImportRoutedDelegates(t *testing.T) {
	f := &fakeStore{}
	var out strings.Builder
	// 기존 경로 — 미확인 플래그 exit 3(관례 불변).
	code := RunImportRouted(context.Background(), f, []string{"--verbose"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("기존 경로 exit=%d, want 3", code)
	}
	// migrate 경로.
	out.Reset()
	data, _ := FormatJSONL(migrateFixtureRows()[:1])
	code = RunImportRouted(context.Background(), f, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
	if code != 0 || !strings.Contains(out.String(), `"imported":1`) {
		t.Fatalf("migrate 경로 exit=%d out=%s", code, out.String())
	}
}

// ── v1.3.1 R19 감사 회귀(A1·A2·A4·A7·A8·A9) — 전부 합성 데모 데이터. ──

// TestRunImportMigrateBatchConflictExitOne — A1[심각]: 배치 InsertBatch
// 실패(실구현 mapInsertErr는 unique 위반 23505를 ErrDuplicate로 감싸고
// MapExit(ErrDuplicate)=0)는 exit 1이어야 한다 — "오류 JSON + exit 0 +
// 0건 커밋" 유령 성공 금지(write.go '전체 폐기 후 exit 0 duplicate'
// 금지 원칙 승계). 오류 원인 문자열은 출력에 보존.
func TestRunImportMigrateBatchConflictExitOne(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows()[:1])
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{insertErr: fmt.Errorf("%w: ERROR 23505 duplicate key knowledge_pkey", store.ErrDuplicate)}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 1 {
		t.Fatalf("exit=%d, want 1(배치 실패 유령 성공 금지); out=%s", code, out)
	}
	if !strings.Contains(out, "23505") {
		t.Fatalf("오류 원인 미보존: %s", out)
	}
}

// TestRunImportMigrateSyncsIDSequence — A2[중간]: 명시 id 적립 완료 후
// id 시퀀스 setval(max(id)+1)이 발행된다(이식 직후 flint write의
// nextval PK 충돌 방지).
func TestRunImportMigrateSyncsIDSequence(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows()) // 명시 id 11·22
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	seen := false
	for _, q := range f.queries {
		if strings.Contains(q, "setval") {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("명시 id 적립 후 setval 미발행: %v", f.queries)
	}
}

// TestRunImportMigrateInvalidatedTimestampsPreserved — A4[중간]: invalidated
// 재이식은 invalid_at=UpdatedAt, ingested_at·valid_at=CreatedAt를 보존한다
// (기존 valid_at←UpdatedAt 치환은 무효화 시각을 덮어썼다 — 불변식 파괴).
func TestRunImportMigrateInvalidatedTimestampsPreserved(t *testing.T) {
	created := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 11, 13, 11, 30, 0, 0, time.UTC)
	sup := 71
	rows := []ExportRow{
		{ID: 71, Topic: "demo r19 ts active", Content: "합성 데모 — 시각 보존 active 본문.",
			Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
			Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: created},
		{ID: 72, Topic: "demo r19 ts invalidated", Content: "합성 데모 — 시각 보존 invalidated 본문.",
			Kind: "knowledge", Status: "invalidated", Confidence: 0.8, Tags: []string{"demo"},
			Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: updated,
			SupersededBy: &sup},
	}
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 2 {
		t.Fatalf("적립=%d, want 2", len(f.inserted))
	}
	rec := f.inserted[1]
	if va, ok := rec["valid_at"].(time.Time); !ok || !va.Equal(created) {
		t.Fatalf("valid_at=%v, want CreatedAt %v", rec["valid_at"], created)
	}
	if ga, ok := rec["ingested_at"].(time.Time); !ok || !ga.Equal(created) {
		t.Fatalf("ingested_at=%v, want CreatedAt %v", rec["ingested_at"], created)
	}
	if ia, ok := rec["invalid_at"].(time.Time); !ok || !ia.Equal(updated) {
		t.Fatalf("invalid_at=%v, want UpdatedAt %v", rec["invalid_at"], updated)
	}
}

// TestRunImportMigrateFileErrorIncludesCause — A7[경미]: FILE 읽기 오류는
// 원인(경로·ENOENT/EACCES)을 SchemaError expect에 포함한다(일률 폐기 금지).
func TestRunImportMigrateFileErrorIncludesCause(t *testing.T) {
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl", "/nonexistent/r19-demo.jsonl"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "/nonexistent/r19-demo.jsonl") {
		t.Fatalf("오류 원인(경로) 미포함: %s", out)
	}
}

// TestRunImportMigrateSupersededRefZeroRowsNotCounted — A8[경미]:
// SetSupersededRef 0행(이미 참조 존재 — 멱등 재실행)은 superseded 계상에서
// 제외한다. 호출 자체는 수행된다.
func TestRunImportMigrateSupersededRefZeroRowsNotCounted(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows())
	if err != nil {
		t.Fatal(err)
	}
	r := &refFake{fakeStore: &fakeStore{}, zeroRows: true}
	out, code := runMigrate(t, r, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"superseded":0`) {
		t.Fatalf("0행 참조 계상(과계상): %s", out)
	}
	if len(r.refCalls) != 1 {
		t.Fatalf("참조 복원 호출=%d회, want 1", len(r.refCalls))
	}
}

// dupHashFake — DupRowByHash(해시 키 판정)를 구현한 fake(realStore 계약 —
// lookupHash가 DupMetaStore 경로를 탄다).
type dupHashFake struct {
	*fakeStore
	dup map[string]DupMeta
}

func (d *dupHashFake) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	m, ok := d.dup[hash]
	return m, ok, nil
}

// TestRunImportMigrateStoredHashDedup — A9[경미]: write가 부가 키(여기선
// jsonl에 실리지 않는 memo)까지 포함해 산출한 content_hash로 저장된 행은
// 재계산으로 재현 불가다 — export가 실어온 저장 해시(content_hash 64 hex)를
// 승계해 dedup해야 중복 적립이 없다.
func TestRunImportMigrateStoredHashDedup(t *testing.T) {
	h := store.ContentHash(store.HashCanonical(map[string]any{
		"topic": "demo r19 extra key", "summary": "합성 데모 — 부가 키 write 행 dedup.",
		"tags": ":demo:", "domain": "main", "confidence": 0.9,
		"origin": "agent", "memo": "write 전용 부가 키",
	}))
	row := ExportRow{ID: 81, Topic: "demo r19 extra key",
		Content: "합성 데모 — 부가 키 write 행 dedup.", Kind: "knowledge",
		Status: "active", Confidence: 0.9, Tags: []string{"demo"},
		Domain: "main", Source: "agent", ContentHash: h}
	data, err := FormatJSONL([]ExportRow{row})
	if err != nil {
		t.Fatal(err)
	}
	f := &dupHashFake{fakeStore: &fakeStore{},
		dup: map[string]DupMeta{h: {ID: 91, Topic: row.Topic, State: "active"}}}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":0`) || !strings.Contains(out, `"duplicated":1`) {
		t.Fatalf("부가 키 행 dedup 누락: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("재적립=%d, want 0", len(f.inserted))
	}
}

// TestRunImportMigrateHashSymmetricWithWrite — A9 전제 가드: 저장 해시가
// 없는 행의 재계산 해시는 write 경로 산출식(ContentHash(HashCanonical(n))
// 전체 키)과 동일하다(부가 키 없는 payload 기준 대칭).
func TestRunImportMigrateHashSymmetricWithWrite(t *testing.T) {
	payload := `{"topic":"demo r19 symmetric","summary":"합성 데모 — write 해시 대칭.","tags":["demo"],"domain":"main","confidence":0.9,"origin":"agent"}` + "\n"
	wf := &fakeStore{}
	if wout, code := run(t, wf, []string{"knowledge"}, payload); code != 0 {
		t.Fatalf("write exit=%d out=%s", code, wout)
	}
	want, _ := wf.inserted[0]["content_hash"].(string)
	if want == "" {
		t.Fatal("write 적립 rec에 content_hash 부재")
	}
	row := ExportRow{ID: 82, Topic: "demo r19 symmetric",
		Content: "합성 데모 — write 해시 대칭.", Kind: "knowledge",
		Status: "active", Confidence: 0.9, Tags: []string{"demo"},
		Domain: "main", Source: "agent"}
	data, err := FormatJSONL([]ExportRow{row})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("import exit=%d out=%s", code, out)
	}
	got, _ := f.inserted[0]["content_hash"].(string)
	if got != want {
		t.Fatalf("재계산 해시 비대칭: got=%s want=%s", got, want)
	}
}

// ── slow 실DB seam(SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만). ──

// importRealDSN — SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만 실DB 접속
// 허용(cli seamRealDSN 계약 승계 — 운영 폴백 차단).
func importRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

// importRealStore — db.Repo 쓰기 + 풀 읽기 어댑터(cli realStore 관례.
// Supersede·SetSupersededRef는 *db.Repo 승격 메서드).
type importRealStore struct {
	*db.Repo
	pool *pgxpool.Pool
}

func (s *importRealStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *importRealStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
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

func (s *importRealStore) Close() {
	s.Repo.Close()
	s.pool.Close()
}

// TestImportMigrateR19RealSlow — 실DB 회귀(slow): A9 부가 키 행 dedup·
// A2 시퀀스 동기(이식 직후 write 성공)·A4 재export 시각 동일성·A1 PK
// 충돌 exit 1. 합성 데모 데이터만 사용하고 태그로 행을 정리한다.
func TestImportMigrateR19RealSlow(t *testing.T) {
	dsn, ok := importRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &importRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	const tag = "importr19"
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE strpos(tags, ':' || $1 || ':') > 0`, tag)
	}
	t.Cleanup(cleanup)
	cleanup()

	t.Run("extra-key write row dedups", func(t *testing.T) { // A9
		payload := `{"topic":"demo r19 slow extra","summary":"합성 데모 slow — 부가 키 write 행.","tags":["` + tag + `"],"domain":"main","confidence":0.9,"origin":"agent","memo":"write 전용 부가 키"}` + "\n"
		if wout, code := run(t, st, []string{"knowledge"}, payload); code != 0 {
			t.Fatalf("사전 write exit=%d out=%s", code, wout)
		}
		rows, err := ExportRows(ctx, st, ExportFilter{Status: "all", Tag: tag})
		if err != nil {
			t.Fatal(err)
		}
		var targets []ExportRow
		for _, r := range rows {
			if strings.Contains(r.Topic, "r19 slow extra") {
				targets = append(targets, r)
			}
		}
		if len(targets) != 1 {
			t.Fatalf("사전 write 행 조회=%d, want 1", len(targets))
		}
		data, err := FormatJSONL(targets)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("import exit=%d out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), `"imported":0`) || !strings.Contains(out.String(), `"duplicated":1`) {
			t.Fatalf("부가 키 행 dedup 누락: %s", out.String())
		}
		cleanup()
	})

	t.Run("sequence synced for explicit ids", func(t *testing.T) { // A2
		maxRows, err := wire.QueryOnce(ctx, st, `SELECT COALESCE(MAX(id), 0) FROM knowledge`, nil)
		if err != nil || len(maxRows) == 0 {
			t.Fatalf("max(id) 조회 실패: %v", err)
		}
		base, _ := render.CellToInt(maxRows[0][0])
		var rows []ExportRow
		for i := 1; i <= 30; i++ { // 연속 명시 id — nextval이 첫 id와 충돌하게
			rows = append(rows, ExportRow{
				ID: base + i, Topic: fmt.Sprintf("demo r19 slow seq %02d", i),
				Content: fmt.Sprintf("합성 데모 slow — 시퀀스 동기 행 %02d.", i),
				Kind:    "knowledge", Status: "active", Confidence: 0.9,
				Tags: []string{tag}, Domain: "main", Source: "agent",
				CreatedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
			})
		}
		data, err := FormatJSONL(rows)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("import exit=%d out=%s", code, out.String())
		}
		payload := `{"topic":"demo r19 slow after","summary":"합성 데모 slow — 이식 직후 write.","tags":["` + tag + `"],"domain":"main","confidence":0.9,"origin":"agent"}` + "\n"
		wout, code := run(t, st, []string{"knowledge"}, payload)
		if code != 0 {
			t.Fatalf("이식 후 write 실패(exit=%d) — setval 미동기 의심: %s", code, wout)
		}
		cleanup()
	})

	t.Run("invalidated timestamps roundtrip", func(t *testing.T) { // A4
		created := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
		updated := time.Date(2026, 11, 13, 11, 30, 0, 0, time.UTC)
		sup := 919106
		rows := []ExportRow{
			{ID: 919106, Topic: "demo r19 slow ts active", Content: "합성 데모 slow — 시각 active.",
				Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{tag},
				Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: created},
			{ID: 919107, Topic: "demo r19 slow ts invalidated", Content: "합성 데모 slow — 시각 invalidated.",
				Kind: "knowledge", Status: "invalidated", Confidence: 0.8, Tags: []string{tag},
				Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: updated,
				SupersededBy: &sup},
		}
		data, err := FormatJSONL(rows)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("import exit=%d out=%s", code, out.String())
		}
		got, err := ExportRows(ctx, st, ExportFilter{Status: "all", Tag: tag})
		if err != nil {
			t.Fatal(err)
		}
		var inv *ExportRow
		for i, r := range got {
			if r.ID == 919107 {
				inv = &got[i]
			}
		}
		if inv == nil {
			t.Fatal("재이식 invalidated 행 미조회")
		}
		if !inv.CreatedAt.Equal(created) || !inv.UpdatedAt.Equal(updated) {
			t.Fatalf("재export 시각 비대칭: created=%v want %v / updated=%v want %v",
				inv.CreatedAt, created, inv.UpdatedAt, updated)
		}
		cleanup()
	})

	t.Run("pk conflict exits 1", func(t *testing.T) { // A1
		if _, err := wire.QueryOnce(ctx, st,
			`INSERT INTO knowledge (id, topic, summary, tags, confidence, origin, status, content_hash)
			 VALUES (919108, 'demo r19 slow occupied', '합성 데모 slow — 선점 행.', ':importr19:', 0.9, 'agent', 'active', 'r19-slow-occupied')`, nil); err != nil {
			t.Fatalf("선점 행 적립 실패: %v", err)
		}
		row := ExportRow{ID: 919108, Topic: "demo r19 slow conflict",
			Content: "합성 데모 slow — PK 충돌 본문(선점과 상이).", Kind: "knowledge",
			Status: "active", Confidence: 0.9, Tags: []string{tag}, Domain: "main", Source: "agent"}
		data, err := FormatJSONL([]ExportRow{row})
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 1 {
			t.Fatalf("exit=%d, want 1(PK 충돌 유령 성공 금지); out=%s", code, out.String())
		}
	})
}
