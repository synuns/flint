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

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// migrateFixtureRows — 합성 export 스냅샷 2행(신규 active 1·계보
// invalidated 1 — superseded_by로 active 행을 가리킨다).
func migrateFixtureRows() []ExportRow {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	sup := 11
	return []ExportRow{
		{
			ID: 11, Topic: "demo migrate alpha", Content: "합성 데모 지식 본문 alpha — 왕복 검증용.",
			Kind: "knowledge", Status: "active", Confidence: 0.9,
			Tags: []string{"alpha", "demo"}, Entities: []string{"svc-demo"},
			Domain: "main", Source: "agent", ContentHash: "unused-recomputed",
			CreatedAt: t0, UpdatedAt: t0,
		},
		{
			ID: 22, Topic: "demo migrate beta", Content: "합성 데모 지식 본문 beta — 계보 복원용.",
			Kind: "knowledge", Status: "invalidated", Confidence: 0.8,
			Tags: []string{"demo", "beta"}, Entities: nil,
			Domain: "main", Source: "agent", ContentHash: "unused-recomputed",
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: &sup,
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
type refFake struct {
	*fakeStore
	refCalls [][2]int
}

func (r *refFake) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	r.refCalls = append(r.refCalls, [2]int{oldID, newID})
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
