// import_jsonl_test.go — import --format jsonl 왕복 재이식
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

// TestParseJSONLRowsRoundtrip — FormatJSONL 출력을 ParseJSONLRows가
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
// duplicate 스킵·exit 0(멱등). dedup은 ANY($1) 일괄 조회
// 경로다 — memMigrateFake가 적립→중복 판정 왕복을 시뮬레이션한다.
func TestRunImportMigrateJSONLIdempotent(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows())
	if err != nil {
		t.Fatal(err)
	}
	m := newMemMigrateFake()
	out, code := runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
	if code != 0 || !strings.Contains(out, `"imported":2`) {
		t.Fatalf("1회차 exit=%d out=%s", code, out)
	}
	out, code = runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("2회차 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":0`) || !strings.Contains(out, `"duplicated":2`) {
		t.Fatalf("멱등 위반: %s", out)
	}
	if len(m.inserted) != 2 { // 1회차 2건만 — 2회차 적립 없음
		t.Fatalf("2회차 재적립: %d", len(m.inserted))
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
	if !strings.Contains(out, "pam import unsupported") {
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
	// 2회차 dedup은 ANY($1) 일괄 조회 — memMigrateFake로
	// 적립 해시가 조회에 보이는지 검증(ExistsHash 스텁 폐지).
	m := newMemMigrateFake()
	if out2, code2 := runMigrate(t, m, []string{"--format", "pam"}, pamSample); code2 != 0 || !strings.Contains(out2, `"imported":1`) {
		t.Fatalf("mem 1회차 exit=%d out=%s", code2, out2)
	}
	out, code = runMigrate(t, m, []string{"--format", "pam"}, pamSample)
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

// ── 감사 회귀 — 전부 합성 데모 데이터. ──

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

// TestRunImportMigrateStoredHashDedup — A9[경미]: write가 부가 키(여기선
// jsonl에 실리지 않는 memo)까지 포함해 산출한 content_hash로 저장된 행은
// 재계산으로 재현 불가다 — export가 실어온 저장 해시(content_hash 64 hex)를
// 승계해 dedup해야 중복 적립이 없다. 기존 행 판정은 ANY($1)
// 일괄 조회 — memMigrateFake의 사전 적립 맵으로 검증.
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
	f := newMemMigrateFake()
	f.dup[h] = DupMeta{ID: 91, Topic: row.Topic, State: "active"}
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

// ── import 행 수 상한(export_import_max_rows 등록표 키). ──

// maxRowsFake — meta에서 export_import_max_rows 오버라이드만 읽는 fake
// (LoadThresholds IN 조회 응답 — 나머지 조회는 0행).
func maxRowsFake(max string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"export_import_max_rows", max}}, nil
		}
		return nil, nil
	}}
}

// maxRowsFixtureRows — 상한 검증용 합성 행 N건(해시 충돌 없는 단순 행).
func maxRowsFixtureRows(n int) []ExportRow {
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	rows := make([]ExportRow, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, ExportRow{
			Topic:   fmt.Sprintf("demo maxrows %02d", i),
			Content: fmt.Sprintf("합성 데모 — import 상한 검증 행 %02d.", i),
			Kind:    "knowledge", Status: "active", Confidence: 0.9,
			Tags: []string{"demo", "maxrows"}, Domain: "main", Source: "agent",
			CreatedAt: t0, UpdatedAt: t0,
		})
	}
	return rows
}

// TestRunImportMigrateMaxRowsExceeded — Review Focus: 행 수가 상한을
// 초과하면 exit 3·오류 JSON에 max·count가 포함되고 적립은 발생하지
// 않는다(대량 문서 메모리 방어 — 적립 전 파이프라인 차단).
func TestRunImportMigrateMaxRowsExceeded(t *testing.T) {
	data, err := FormatJSONL(maxRowsFixtureRows(3))
	if err != nil {
		t.Fatal(err)
	}
	f := maxRowsFake("2")
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 3 {
		t.Fatalf("exit=%d, want 3(상한 초과); out=%s", code, out)
	}
	if !strings.Contains(out, `"max":2`) || !strings.Contains(out, `"count":3`) {
		t.Fatalf("오류 JSON max·count 미포함: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

// TestRunImportMigrateMaxRowsBoundary — 상한과 같은 행 수는 통과
// (count == max 허용 — 초과만 차단).
func TestRunImportMigrateMaxRowsBoundary(t *testing.T) {
	data, err := FormatJSONL(maxRowsFixtureRows(2))
	if err != nil {
		t.Fatal(err)
	}
	f := maxRowsFake("2")
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, string(data))
	if code != 0 || !strings.Contains(out, `"imported":2`) {
		t.Fatalf("경계(==max) exit=%d out=%s", code, out)
	}
}

// TestRunImportMigrateMaxRowsPAMExceeded — pam 경로 동일 상한: 후보+스킵
// 합산 행 수 초과 시 exit 3·적립 없음.
func TestRunImportMigrateMaxRowsPAMExceeded(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"m1","type":"fact","content":"demo maxrows one","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"m2","type":"fact","content":"demo maxrows two","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"m3","type":"fact","content":"demo maxrows three","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}}
 ],
 "relations": []}`
	f := maxRowsFake("2")
	out, code := runMigrate(t, f, []string{"--format", "pam"}, doc)
	if code != 3 {
		t.Fatalf("pam exit=%d, want 3(상한 초과); out=%s", code, out)
	}
	if !strings.Contains(out, `"max":2`) || !strings.Contains(out, `"count":3`) {
		t.Fatalf("pam 오류 JSON max·count 미포함: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("pam 상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

// TestRunImportMigrateValidAtPreserved — Review Focus: valid_at은
// 원값으로 보존 적립된다(경로 확장 — 종전 CreatedAt 치환 폐지.
// 검증 시각 ≠ 수집 시각 행에서 원천 valid_at이 그대로 적립).
func TestRunImportMigrateValidAtPreserved(t *testing.T) {
	created := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	valid := time.Date(2026, 11, 12, 23, 15, 0, 0, time.UTC)
	rows := []ExportRow{{
		ID: 61, Topic: "demo validat preserve", Content: "합성 데모 — valid_at 보존 적립.",
		Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
		Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: created,
		ValidAt: valid,
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
	rec := f.inserted[0]
	if va, ok := rec["valid_at"].(time.Time); !ok || !va.Equal(valid) {
		t.Fatalf("valid_at=%v, want 원값 %v(CreatedAt 치환 폐지)", rec["valid_at"], valid)
	}
	if ga, ok := rec["ingested_at"].(time.Time); !ok || !ga.Equal(created) {
		t.Fatalf("ingested_at=%v, want CreatedAt %v", rec["ingested_at"], created)
	}
}

// TestRunImportMigratePAMProjectKind — Review Focus: PAM project형
// 후보는 스킵되지 않고 pending 적립된다(승격 모델 준수). 원천 kind 보존은
// 태그 "pam:project" 추가로(ExportRow.Kind는 저장 라우터 상수라 태그로만
// 구분 — 코디네이터 룰링 2026-11-14).
func TestRunImportMigratePAMProjectKind(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"pj1","type":"project","content":"demo project memory content","status":"active","confidence":{"current":0.6},"tags":["roadmap"],"provenance":{"platform":"demo"}}
 ],
 "relations": []}`
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format", "pam"}, doc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":1`) {
		t.Fatalf("project형 적립 요약: %s", out)
	}
	if strings.Contains(out, `"skipped"`) {
		t.Fatalf("project형이 스킵 계상됨: %s", out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립=%d, want 1(스킵 해제)", len(f.inserted))
	}
	rec := f.inserted[0]
	if rec["status"] != "pending" {
		t.Fatalf("status = %v, want pending(승격 모델)", rec["status"])
	}
	if rec["kind"] != "knowledge" {
		t.Fatalf("kind = %v, want knowledge(저장 라우터 상수)", rec["kind"])
	}
	tags, _ := rec["tags"].(string)
	if !strings.Contains(tags, ":pam:project:") {
		t.Fatalf("tags = %q, want pam:project 보존(원천 kind 구분)", tags)
	}
	if !strings.Contains(tags, ":roadmap:") {
		t.Fatalf("tags = %q, want 원 태그 보존", tags)
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

// TestImportMaxRowsRealSlow — 실DB 회귀(slow): meta 오버라이드
// export_import_max_rows가 실 LoadThresholds 경로(IN 조회)로 반영되어
// 초과 문서는 exit 3·오류 JSON max·count, 경계 문서는 정상 적립.
// 합성 데모 데이터만 사용하고 태그로 행을 정리한다.
func TestImportMaxRowsRealSlow(t *testing.T) {
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
	const tag = "maxrows" // maxRowsFixtureRows가 붙이는 정리 태그와 동일
	const key = "export_import_max_rows"
	// 행·meta 정리(선행+후행 — 이전 런 잔재·본 런 오버라이드 흡수).
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE strpos(tags, ':' || $1 || ':') > 0`, tag)
		_, _ = st.Query(ctx, `DELETE FROM meta WHERE key = $1`, key)
	}
	t.Cleanup(cleanup)
	cleanup()
	if _, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '2')`, key); err != nil {
		t.Fatalf("meta 오버라이드 설정 실패: %v", err)
	}

	t.Run("exceeded exits 3 with max count", func(t *testing.T) {
		data, err := FormatJSONL(maxRowsFixtureRows(3))
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 3 {
			t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), `"max":2`) || !strings.Contains(out.String(), `"count":3`) {
			t.Fatalf("오류 JSON max·count 미포함: %s", out.String())
		}
	})
	t.Run("boundary imports", func(t *testing.T) {
		data, err := FormatJSONL(maxRowsFixtureRows(2))
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 || !strings.Contains(out.String(), `"imported":2`) {
			t.Fatalf("경계 exit=%d out=%s", code, out.String())
		}
		cleanup()
	})
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
		// 최상위 집계 COALESCE(MAX(id),0)는 Doltgres 확장 프로토콜에서
		// 빈 테이블 때 0행을 반환한다(단순 프로토콜 psql은 1행 — 엔진
		// 비대칭). 스칼라 서브쿼리는 항상 1행.
		maxRows, err := wire.QueryOnce(ctx, st, `SELECT COALESCE((SELECT MAX(id) FROM knowledge), 0)`, nil)
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

	t.Run("invalidated timestamps roundtrip", func(t *testing.T) { // valid_at
		created := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
		updated := time.Date(2026, 11, 13, 11, 30, 0, 0, time.UTC)
		valid := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC) // 검증 시각 ≠ 수집 시각
		sup := 919106
		rows := []ExportRow{
			{ID: 919106, Topic: "demo r19 slow ts active", Content: "합성 데모 slow — 시각 active.",
				Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{tag},
				Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: created,
				ValidAt: valid},
			{ID: 919107, Topic: "demo r19 slow ts invalidated", Content: "합성 데모 slow — 시각 invalidated.",
				Kind: "knowledge", Status: "invalidated", Confidence: 0.8, Tags: []string{tag},
				Domain: "main", Source: "agent", CreatedAt: created, UpdatedAt: updated,
				ValidAt: valid, SupersededBy: &sup},
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
		if !inv.ValidAt.Equal(valid) {
			t.Fatalf("valid_at 왕복 비대칭: %v, want %v(원값 보존)", inv.ValidAt, valid)
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

// ── import 배치+청크 트랜잭션. ──
// 전부 합성 데모 데이터. dedup ANY($1) 일괄 조회·INSERT 청크 커밋·
// 상한 조기 라인 스캔 계약.

// memMigrateFake — dedup ANY($1) 조회와 InsertBatch이 하나의 메모리 해시
// 맵으로 동작하는 fake(실DB "적립→재실행 중복 판정" 왕복 시뮬레이션 —
// dedup은 Query 일괄 조회 경로다). insertErrs는 InsertBatch
// 호출 순번(0-기반)별 오류 — 해당 순번 청크만 실패(이전 청크는 커밋된
// 것으로 시뮬레이션).
type memMigrateFake struct {
	*migrateFake
	dup        map[string]DupMeta
	ids        map[int]bool // 기존·적립 knowledge id 집합(id 충돌 판정)
	nextID     int
	insertErrs map[int]error
	batchSizes []int
	calls      int
}

// newMemMigrateFake — 기본 초기화(dup·id 맵·오류표 준비).
func newMemMigrateFake() *memMigrateFake {
	return &memMigrateFake{
		migrateFake: &migrateFake{fakeStore: &fakeStore{}},
		dup:         map[string]DupMeta{},
		ids:         map[int]bool{},
		insertErrs:  map[int]error{},
	}
}

// Query — content_hash = ANY($1) 일괄 조회를 dup 맵으로, id = ANY($1)
// 충돌 조회를 ids 맵으로 응답한다(나머지는 fakeStore 위임).
func (m *memMigrateFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if strings.Contains(sql, "content_hash = ANY($1)") {
		m.fakeStore.queries = append(m.fakeStore.queries, sql)
		want, _ := args[0].([]string)
		set := map[string]bool{}
		for _, h := range want {
			set[h] = true
		}
		var out [][]any
		for h, meta := range m.dup {
			if set[h] {
				out = append(out, []any{h, int32(meta.ID), meta.Topic, meta.State})
			}
		}
		return out, nil
	}
	if strings.Contains(sql, "id = ANY($1)") && strings.Contains(sql, "FROM knowledge") {
		m.fakeStore.queries = append(m.fakeStore.queries, sql)
		want, _ := args[0].([]int)
		var out [][]any
		for _, id := range want {
			if m.ids[id] {
				out = append(out, []any{int32(id)})
			}
		}
		return out, nil
	}
	return m.fakeStore.Query(ctx, sql, args...)
}

// InsertBatch — 성공 시 content_hash를 dup 맵에 등록(재실행 dedup 원천).
// 오류 청크는 롤백 정합으로 미기록·미등록(이전 청크 커밋은 유지).
func (m *memMigrateFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	m.batchSizes = append(m.batchSizes, len(recs))
	call := m.calls
	m.calls++
	if err := m.insertErrs[call]; err != nil {
		return nil, err
	}
	m.fakeStore.inserted = append(m.fakeStore.inserted, recs...)
	ids := make([]int, len(recs))
	for i, r := range recs {
		m.nextID++
		id := m.nextID
		if v, ok := r["id"].(int); ok && v > 0 {
			id = v // 실구현 RETURNING id 정합(명시 id 보존)
			if v >= m.nextID {
				m.nextID = v
			}
		}
		ids[i] = id
		m.ids[id] = true // 적립 id 등록(G5 — 재이식 충돌 판정 원천)
		if h, ok := r["content_hash"].(string); ok && h != "" {
			m.dup[h] = DupMeta{ID: id}
		}
	}
	return ids, nil
}

// overrideImportChunks — 청크 크기 축소(테스트 종료 시 원복).
func overrideImportChunks(t *testing.T, dedup, insert int) {
	t.Helper()
	savedD, savedI := importDedupChunk, importInsertChunk
	importDedupChunk, importInsertChunk = dedup, insert
	t.Cleanup(func() { importDedupChunk, importInsertChunk = savedD, savedI })
}

// TestCountJSONLRows — 라인 스캔 카운트(P5): 공백 줄 제외 줄 수가
// ParseJSONLRows 행 수 기준과 동일(빈 입력 0·후행 개행·공백만 줄).
func TestCountJSONLRows(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"빈 입력", "", 0},
		{"공백만", "\n\n  \t\r\n", 0},
		{"2행", "{\"a\":1}\n{\"b\":2}\n", 2},
		{"공백 줄 끼움", "{\"a\":1}\n\n   \n{\"b\":2}", 2},
		{"후행 개행 없음", "{\"a\":1}\n{\"b\":2}", 2},
	}
	for _, tc := range cases {
		if got := countJSONLRows([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: countJSONLRows=%d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestRunImportMigrateMaxRowsEarlyScan — P5 Review Focus: 상한 초과 판정은
// 전량 파싱 전 라인 스캔으로 조기 발생한다 — 파손 행이 섞여 있어도 상한
// 오류가 우선(파싱 메모리 방어가 parse 오류보다 앞선다). 적립 없음.
func TestRunImportMigrateMaxRowsEarlyScan(t *testing.T) {
	f := maxRowsFake("2")
	doc := "{\"id\":1,\"topic\":\"demo early a\"}\n" +
		"{\"id\":2,\"topic\":\"demo early b\"}\n" +
		"{\"id\":not-json}\n"
	out, code := runMigrate(t, f, []string{"--format", "jsonl"}, doc)
	if code != 3 {
		t.Fatalf("exit=%d, want 3(상한 초과 조기); out=%s", code, out)
	}
	if !strings.Contains(out, `"max":2`) || !strings.Contains(out, `"count":3`) {
		t.Fatalf("오류 JSON max·count 미포함: %s", out)
	}
	if strings.Contains(out, "corrupt") {
		t.Fatalf("전량 파싱이 선행(조기 스캔 아님): %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

// TestImportRowsBatchDedupBatchLookup — P1 Review Focus: dedup은 행별
// lookupHash가 아니라 content_hash = ANY($1) 청크 사전 조회 1회(청크
// 크기 초과분만 다음 왕복)로 판정한다. 파일 내 중복은 여전히 첫 행 우선
// 스킵, 기존 행 id는 계보 참조 해석용 idmap으로 승계된다.
func TestImportRowsBatchDedupBatchLookup(t *testing.T) {
	overrideImportChunks(t, 2, 100)
	m := newMemMigrateFake()
	// 행 1·2는 기존 DB 행(해시 사전 등록), 행 3·4는 신규.
	rows := maxRowsFixtureRows(4)
	var data []byte
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseJSONLRows(data)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, r := range parsed {
		hashInput, _, se := rowHashRec(r)
		if se != nil {
			t.Fatalf("rowHashRec: %v", se)
		}
		hashes = append(hashes, store.ContentHash(store.HashCanonical(hashInput)))
	}
	m.dup[hashes[0]] = DupMeta{ID: 501, Topic: "demo pre-existing 1", State: "active"}
	m.dup[hashes[1]] = DupMeta{ID: 502, Topic: "demo pre-existing 2", State: "active"}
	oc, err := importRowsBatch(context.Background(), m, parsed)
	if err != nil {
		t.Fatalf("importRowsBatch: %v", err)
	}
	if oc.imported != 2 || oc.duplicated != 2 {
		t.Fatalf("imported=%d duplicated=%d, want 2/2", oc.imported, oc.duplicated)
	}
	anyQueries := 0
	for _, q := range m.fakeStore.queries {
		if strings.Contains(q, "content_hash = ANY($1)") {
			anyQueries++
		}
	}
	// 해시 4개를 청크 2로 → 2회 왕복(행별 조회였다면 4회).
	if anyQueries != 2 {
		t.Fatalf("ANY($1) 왕복=%d회, want 2(청크 2)", anyQueries)
	}
	if oc.byIndex[0] != 501 || oc.byIndex[1] != 502 {
		t.Fatalf("기존 행 id 승계: byIndex=%v, want [501 502 ...]", oc.byIndex)
	}
	if len(m.inserted) != 2 {
		t.Fatalf("신규 적립=%d, want 2", len(m.inserted))
	}
}

// TestImportRowsBatchChunkBoundary — P2 Review Focus: 적립은 청크
// INSERT로 나뉜다(마지막 청크는 잔여 크기). id 매핑·imported 집계는
// 행 순서와 동일하게 유지된다.
func TestImportRowsBatchChunkBoundary(t *testing.T) {
	overrideImportChunks(t, 100, 2)
	m := newMemMigrateFake()
	rows := maxRowsFixtureRows(5)
	oc, err := importRowsBatch(context.Background(), m, rows)
	if err != nil {
		t.Fatalf("importRowsBatch: %v", err)
	}
	if oc.imported != 5 {
		t.Fatalf("imported=%d, want 5", oc.imported)
	}
	if len(m.batchSizes) != 3 || m.batchSizes[0] != 2 || m.batchSizes[1] != 2 || m.batchSizes[2] != 1 {
		t.Fatalf("청크 크기=%v, want [2 2 1]", m.batchSizes)
	}
	for i, id := range oc.byIndex {
		if id != i+1 {
			t.Fatalf("byIndex[%d]=%d, want 순차 id %d", i, id, i+1)
		}
	}
}

// TestRunImportMigratePartialFailureResume — 청크 커밋 회귀 Review Focus:
// 청크 2 INSERT 실패(23505 원인 보존·exit 1) 시 청크 1 커밋은 유지되고,
// 재실행은 dedup가 이미 적립된 청크를 회복해 잔여만 적립(부분 성공
// imported 수 보존·멱등 재개).
func TestRunImportMigratePartialFailureResume(t *testing.T) {
	overrideImportChunks(t, 100, 2)
	rows := maxRowsFixtureRows(6)
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	m := newMemMigrateFake()
	m.insertErrs[1] = fmt.Errorf("%w: ERROR 23505 duplicate key knowledge_pkey", store.ErrDuplicate)
	out, code := runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
	if code != 1 {
		t.Fatalf("1회차 exit=%d, want 1(배치 실패 유령 성공 금지); out=%s", code, out)
	}
	if !strings.Contains(out, "23505") {
		t.Fatalf("오류 원인 미보존: %s", out)
	}
	if len(m.batchSizes) != 2 || m.batchSizes[0] != 2 || m.batchSizes[1] != 2 {
		t.Fatalf("1회차 청크=%v, want [2 2](첫 청크 커밋 후 실패)", m.batchSizes)
	}
	if len(m.inserted) != 2 { // 청크 1만 적립 — 부분 성공 보존
		t.Fatalf("부분 성공 적립=%d, want 2(첫 청크)", len(m.inserted))
	}
	delete(m.insertErrs, 1)
	out, code = runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
	if code != 0 {
		t.Fatalf("재개 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":4`) || !strings.Contains(out, `"duplicated":2`) {
		t.Fatalf("재개 요약(부분 성공 회복): %s", out)
	}
	if len(m.inserted) != 6 {
		t.Fatalf("총 적립=%d, want 6", len(m.inserted))
	}
}

// TestImportChunkedRealSlow — 실DB 회귀(slow): 청크 축소 상태에서
// 왕복·멱등·계보·부분 실패 재개. 합성 데모 데이터만 사용하고 태그로
// 정리한다. SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만.
func TestImportChunkedRealSlow(t *testing.T) {
	dsn, ok := importRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	overrideImportChunks(t, 5, 7) // dedup 5해시·INSERT 7행 청크로 경계 강제
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
	const tag = "importt2"
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE strpos(tags, ':' || $1 || ':') > 0`, tag)
	}
	t.Cleanup(cleanup)
	cleanup()

	t.Run("roundtrip idempotent lineage across chunks", func(t *testing.T) {
		defer cleanup()
		maxRows, err := wire.QueryOnce(ctx, st, `SELECT COALESCE((SELECT MAX(id) FROM knowledge), 0)`, nil)
		if err != nil || len(maxRows) == 0 {
			t.Fatalf("max(id) 조회 실패: %v", err)
		}
		base, _ := render.CellToInt(maxRows[0][0])
		t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
		var rows []ExportRow
		for i := 1; i <= 17; i++ {
			r := ExportRow{
				ID:      base + i, // 계보 참조 해석용 명시 id(idmap 승계)
				Topic:   fmt.Sprintf("demo t2 chunk %02d", i),
				Content: fmt.Sprintf("합성 데모 slow — 청크 왕복 행 %02d.", i),
				Kind:    "knowledge", Status: "active", Confidence: 0.9,
				Tags: []string{tag}, Domain: "main", Source: "agent",
				CreatedAt: t0, UpdatedAt: t0,
			}
			if i == 16 { // 계보: 마지막 청크 행이 이전 청크 행을 대체
				r.Status = "invalidated"
				sup := base + i - 1
				r.SupersededBy = &sup
			}
			rows = append(rows, r)
		}
		data, err := FormatJSONL(rows)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("1회차 exit=%d out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), `"imported":17`) || !strings.Contains(out.String(), `"superseded":1`) {
			t.Fatalf("1회차 요약: %s", out.String())
		}
		out.Reset()
		code = RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("2회차 exit=%d out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), `"imported":0`) || !strings.Contains(out.String(), `"duplicated":17`) {
			t.Fatalf("멱등 위반(ANY($1) 일괄 dedup): %s", out.String())
		}
		cleanup()
	})

	t.Run("id collision reassign keeps import alive", func(t *testing.T) {
		// 선점 id(다른 내용)가 있어도 배치 PK 충돌 exit 1이
		// 아니라 자동 재할당으로 전량 적립 exit 0 — 종전 부분 실패 재개
		// 시나리오의 왕복 회복 회귀(청크 실패 재개는 단위 insertErrs로 승계).
		maxRows, err := wire.QueryOnce(ctx, st, `SELECT COALESCE((SELECT MAX(id) FROM knowledge), 0)`, nil)
		if err != nil || len(maxRows) == 0 {
			t.Fatalf("max(id) 조회 실패: %v", err)
		}
		base, _ := render.CellToInt(maxRows[0][0])
		t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
		var rows []ExportRow
		for i := 1; i <= 12; i++ { // 청크 7: [1..7][8..12] — 2청크
			rows = append(rows, ExportRow{
				ID:      base + i,
				Topic:   fmt.Sprintf("demo t2 resume %02d", i),
				Content: fmt.Sprintf("합성 데모 slow — 재할당 검증 행 %02d.", i),
				Kind:    "knowledge", Status: "active", Confidence: 0.9,
				Tags: []string{tag}, Domain: "main", Source: "agent",
				CreatedAt: t0, UpdatedAt: t0,
			})
		}
		data, err := FormatJSONL(rows)
		if err != nil {
			t.Fatal(err)
		}
		// 청크 2 위치(id base+9)를 상이 본문으로 선점 → G5 재할당 대상.
		if _, err := wire.QueryOnce(ctx, st, fmt.Sprintf(
			`INSERT INTO knowledge (id, topic, summary, tags, confidence, origin, status, content_hash)
			 VALUES (%d, 'demo t2 squatter', '합성 데모 slow — 선점 행.', ':%s:', 0.9, 'agent', 'active', 't2-squatter-%d')`,
			base+9, tag, base), nil); err != nil {
			t.Fatalf("선점 행 적립 실패: %v", err)
		}
		var out strings.Builder
		code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
		if code != 0 {
			t.Fatalf("exit=%d, want 0(충돌 id 자동 재할당); out=%s", code, out.String())
		}
		if !strings.Contains(out.String(), `"imported":12`) || !strings.Contains(out.String(), `"remapped":1`) {
			t.Fatalf("재할당 요약(전량 적립): %s", out.String())
		}
		// 선점 행은 그대로 — 재할당이 덮어쓰지 않는다.
		var squatter string
		if err := st.pool.QueryRow(ctx, `SELECT topic FROM knowledge WHERE id = $1`, base+9).Scan(&squatter); err != nil {
			t.Fatalf("선점 행 조회: %v", err)
		}
		if squatter != "demo t2 squatter" {
			t.Fatalf("선점 행 변형: %q", squatter)
		}
		// 이식 후 write 성공 — setval 동기(명시 id 경로).
		payload := `{"topic":"demo t2 after resume","summary":"합성 데모 slow — 재개 직후 write.","tags":["` + tag + `"],"domain":"main","confidence":0.9,"origin":"agent"}` + "\n"
		wout, code := run(t, st, []string{"knowledge"}, payload)
		if code != 0 {
			t.Fatalf("재개 후 write 실패(exit=%d) — setval 미동기 의심: %s", code, wout)
		}
		cleanup()
	})
}

// TestImportBatchBenchRealSlow — bench(slow): 합성 5.6k행 재이식이 목표
// 수 초 구간에 들어온다. 경과 시간은
// 로그로 남기고 상한은 여유 있게 20s(환경 편차 흡수 — 실측은 t.Log).
func TestImportBatchBenchRealSlow(t *testing.T) {
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
	const tag = "importt2bench"
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE strpos(tags, ':' || $1 || ':') > 0`, tag)
	}
	t.Cleanup(cleanup)
	cleanup()

	rows := maxRowsFixtureRows(5600)
	for i := range rows { // bench 태그 부착(정리 식별용)
		rows[i].Tags = []string{tag, "bench"}
	}
	data, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var out strings.Builder
	code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("bench import exit=%d out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"imported":5600`) {
		t.Fatalf("bench 요약: %s", out.String())
	}
	t.Logf("bench import 5.6k rows: %s (목표 수 초, 상한 20s)", elapsed)
	if elapsed > 20*time.Second {
		t.Fatalf("bench 5.6k import %s — 목표(수 초) 미달, 상한 20s 초과", elapsed)
	}
	// 재실행 — 일괄 dedup(ANY($1)) 경로 벤치: 5.6k 중복 판정.
	start = time.Now()
	out.Reset()
	code = RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(data)), &out)
	dedupElapsed := time.Since(start)
	if code != 0 || !strings.Contains(out.String(), `"duplicated":5600`) {
		t.Fatalf("bench 재실행(멱등) exit=%d out=%s", code, out.String())
	}
	t.Logf("bench reimport(dedup 5.6k): %s", dedupElapsed)
	if dedupElapsed > 20*time.Second {
		t.Fatalf("bench 재실행 dedup %s — 상한 20s 초과", dedupElapsed)
	}
}

// ── import 파일 크기 상한·id 충돌 자동 재할당·
// project note 저장(F5). 전부 합성 데모 데이터. ──

// overrideImportMaxFileBytes — 파일 크기 상한 축소(테스트 종료 시 원복 —
// 실 512MB 파일을 만들지 않고 stat 경계만 검증).
func overrideImportMaxFileBytes(t *testing.T, max int64) {
	t.Helper()
	saved := importMaxFileBytes
	importMaxFileBytes = max
	t.Cleanup(func() { importMaxFileBytes = saved })
}

// TestRunImportMigrateFileSizeLimitExceeded — G3 Review Focus: FILE 크기가
// 상한(512MB 기본)을 초과하면 os.ReadFile 전 stat 판정으로 exit 3 —
// 오류 JSON에 max·size가 포함되고 적립·파싱은 발생하지 않는다(슬랩
// 적재 전 차단).
func TestRunImportMigrateFileSizeLimitExceeded(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows()[:1])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "demo-g3.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	overrideImportMaxFileBytes(t, int64(len(data)-1)) // 크기 > 상한
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format=jsonl", path}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(크기 상한 초과); out=%s", code, out)
	}
	if !strings.Contains(out, `"max":`) || !strings.Contains(out, `"size":`) {
		t.Fatalf("오류 JSON max·size 미포함: %s", out)
	}
	if size := fmt.Sprintf(`"size":%d`, len(data)); !strings.Contains(out, size) {
		t.Fatalf("오류 JSON size 부정합: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("상한 초과 적립 발생: %d건", len(f.inserted))
	}
}

// TestRunImportMigrateFileSizeBoundary — G3 경계: 크기 == 상한은 통과
// (초과 판정은 초과분이 있을 때만).
func TestRunImportMigrateFileSizeBoundary(t *testing.T) {
	data, err := FormatJSONL(migrateFixtureRows()[:1])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "demo-g3b.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	overrideImportMaxFileBytes(t, int64(len(data))) // 경계 — 통과
	f := &fakeStore{}
	out, code := runMigrate(t, f, []string{"--format=jsonl", path}, "")
	if code != 0 || !strings.Contains(out, `"imported":1`) {
		t.Fatalf("경계 파일 exit=%d out=%s", code, out)
	}
}

// g5Row — G5 검증용 합성 행 1건(명시 id — salt로 본문·저장 해시 변형).
func g5Row(id, salt int, t0 time.Time) ExportRow {
	return ExportRow{
		ID: id, Topic: fmt.Sprintf("demo g5 row %d.%d", id, salt),
		Content: fmt.Sprintf("합성 데모 — g5 재할당 검증 행 %d(변형 %d).", id, salt),
		Kind:    "knowledge", Status: "active", Confidence: 0.9,
		Tags: []string{"demo", "g5"}, Domain: "main", Source: "agent",
		ContentHash: fmt.Sprintf("%064x", id*1000+salt),
		CreatedAt:   t0, UpdatedAt: t0,
	}
}

// TestImportRowsBatchIDCollisionExistingReassigned — G5 Review Focus:
// 기존 DB 동일 id·다른 내용 행(해시 dedup 미통과 — 동일 내용이었다면
// 스킵됨)은 자동 재할당(명시 id 제거 → serial)으로 적립되고 remapped로
// 계상된다. 충돌하지 않는 명시 id는 그대로 보존.
func TestImportRowsBatchIDCollisionExistingReassigned(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	m := newMemMigrateFake()
	m.ids[9] = true // 기존 DB 행 id=9(다른 내용 — 해시는 미등록)
	rows := []ExportRow{g5Row(9, 1, t0), g5Row(10, 1, t0)}
	oc, err := importRowsBatch(context.Background(), m, rows)
	if err != nil {
		t.Fatalf("importRowsBatch: %v", err)
	}
	if oc.imported != 2 || oc.remapped != 1 {
		t.Fatalf("imported=%d remapped=%d, want 2/1(충돌 id만 재할당)", oc.imported, oc.remapped)
	}
	if len(m.inserted) != 2 {
		t.Fatalf("적립=%d, want 2(전량 적립)", len(m.inserted))
	}
	if _, has := m.inserted[0]["id"]; has {
		t.Fatalf("충돌 id 미재할당: inserted[0].id=%v", m.inserted[0]["id"])
	}
	if id, _ := m.inserted[1]["id"].(int); id != 10 {
		t.Fatalf("비충돌 명시 id 미보존: inserted[1].id=%v", m.inserted[1]["id"])
	}
	if got := oc.idmap[9]; got == 0 || got == 9 {
		t.Fatalf("재할당 id 매핑 부재/불일치: idmap[9]=%d", got)
	}
}

// TestImportRowsBatchInFileDuplicateIDReassigned — G5: 파일 내 중복 id(다른
// 내용)는 첫 행이 id를 갖고 이후 행은 자동 재할당 — 배치 PK 충돌로 전체
// 실패(exit 1)하던 경로 폐지.
func TestImportRowsBatchInFileDuplicateIDReassigned(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	m := newMemMigrateFake()
	rows := []ExportRow{g5Row(7, 1, t0), g5Row(7, 2, t0), g5Row(8, 1, t0)}
	oc, err := importRowsBatch(context.Background(), m, rows)
	if err != nil {
		t.Fatalf("importRowsBatch: %v", err)
	}
	if oc.imported != 3 || oc.remapped != 1 {
		t.Fatalf("imported=%d remapped=%d, want 3/1(파일 내 중복 1건)", oc.imported, oc.remapped)
	}
	if id, _ := m.inserted[0]["id"].(int); id != 7 {
		t.Fatalf("첫 행 id 미보존: %v", m.inserted[0]["id"])
	}
	if _, has := m.inserted[1]["id"]; has {
		t.Fatalf("파일 내 중복 id 미재할당: %v", m.inserted[1]["id"])
	}
	if id, _ := m.inserted[2]["id"].(int); id != 8 {
		t.Fatalf("독립 id 오염: %v", m.inserted[2]["id"])
	}
}

// TestRunImportMigrateSameDBReassignRoundtrip — G5 왕복 회귀: 같은 DB에
// 같은 id·다른 내용을 재이식하면 id 재할당으로 전량 적립 exit 0 —
// 요약에 remapped가 보고된다(충돌 exit 1로 왕복이 끊기던 주 시나리오 회복).
func TestRunImportMigrateSameDBReassignRoundtrip(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	m := newMemMigrateFake()
	data, err := FormatJSONL([]ExportRow{g5Row(11, 1, t0), g5Row(22, 1, t0)})
	if err != nil {
		t.Fatal(err)
	}
	out, code := runMigrate(t, m, []string{"--format", "jsonl"}, string(data))
	if code != 0 || !strings.Contains(out, `"imported":2`) {
		t.Fatalf("1회차 exit=%d out=%s", code, out)
	}
	// 2회차 — 같은 id·다른 내용(해시·본문 변형) 재이식.
	data2, err := FormatJSONL([]ExportRow{g5Row(11, 2, t0), g5Row(22, 2, t0)})
	if err != nil {
		t.Fatal(err)
	}
	out, code = runMigrate(t, m, []string{"--format", "jsonl"}, string(data2))
	if code != 0 {
		t.Fatalf("재이식 exit=%d, want 0(id 재할당); out=%s", code, out)
	}
	if !strings.Contains(out, `"imported":2`) || !strings.Contains(out, `"remapped":2`) {
		t.Fatalf("재이식 요약(id 재할당·전량 적립): %s", out)
	}
}

// TestWriteProjectNoteStored — F5 Review Focus: project payload의 note는
// kindColumns 등록 컬럼으로 저장된다(SchemaV2 projects.note — 종전
// 비컬럼 메타데이터 취급 버려짐 폐지).
func TestWriteProjectNoteStored(t *testing.T) {
	f := &fakeStore{}
	payload := `{"name":"데모 노트 프로젝트","status":"active","note":"합성 데모 노트 — 저장 검증."}` + "\n"
	out, code := run(t, f, []string{"project"}, payload)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("적립=%d, want 1: %s", len(f.inserted), out)
	}
	if got, _ := f.inserted[0]["note"].(string); got != "합성 데모 노트 — 저장 검증." {
		t.Fatalf("note 미저장: %v", f.inserted[0]["note"])
	}
}
