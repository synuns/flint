// import_jsonl.go — v1.3.0 T9: flint import --format jsonl|pam 마이그레이션
// 경로. export jsonl(T3 포맷) 재이식 — 행마다 기존 write 회로(Normalize →
// content_hash dedup → InsertBatch)를 재사용하고, superseded_by 참조 행은
// 후처리로 기존 Supersede 전이를 재사용해 계보를 복원한다. 기존 `flint
// import [path]` 규칙 변환 경로는 불변(RunImportRouted가 --format 유무로
// 분기 — 등록은 knowledge.go 1줄 위임).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseJSONLRows — export jsonl(T3 FormatJSONL 출력)을 ExportRow 슬라이스로
// 되돌린다. 행별 파싱이며 파손 행은 1 기반 행 번호를 포함한 오류(호출부는
// exit 3).
func ParseJSONLRows(data []byte) ([]ExportRow, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	var rows []ExportRow
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v exportJSONLRow
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("jsonl %d번째 행 파손: %w", i+1, err)
		}
		rows = append(rows, ExportRow(v)) // S1016 — 필드 구성 동일 타입 변환
	}
	return rows, nil
}

// importMigrateSummary — 요약 1행 조립(stdout 계약: status·imported·
// duplicated·superseded. id 재할당은 remapped, PAM 스킵(status 비적립
// 대상·비 knowledge 후보)은 skipped를 조건부 추가 보고).
func importMigrateSummary(imported, duplicated, superseded, remapped, skipped int) string {
	m := map[string]any{
		"status":     "ok",
		"imported":   imported,
		"duplicated": duplicated,
		"superseded": superseded,
	}
	if remapped > 0 {
		m["remapped"] = remapped
	}
	if skipped > 0 {
		m["skipped"] = skipped
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf(`{"status":"ok","imported":%d,"duplicated":%d,"superseded":%d}`,
			imported, duplicated, superseded)
	}
	return string(b)
}

// migrateValidFormats — --format 허용값(jsonl·pam).
var migrateValidFormats = map[string]bool{"jsonl": true, "pam": true}

// parseImportMigrateArgs — --format 값(필수)과 FILE 위치 인자(선택 1개).
// 무효 값·값 누락·미확인 플래그·FILE 2개 이상은 사용법 오류(exit 3).
func parseImportMigrateArgs(args []string) (format, file string, err error) {
	format = ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--format" || strings.HasPrefix(a, "--format=") {
			if strings.HasPrefix(a, "--format=") {
				format = strings.TrimPrefix(a, "--format=")
			} else {
				if i+1 >= len(args) {
					return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam 값 필요"}
				}
				format = args[i+1]
				i++
			}
			if !migrateValidFormats[format] {
				return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam"}
			}
			if err := ValidateFlagValue("format", format); err != nil {
				return "", "", err
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return "", "", fmt.Errorf("unknown flag: %s", a)
		}
		if file != "" {
			return "", "", &store.SchemaError{Field: "file", Expect: "위치 인자 1개(FILE)"}
		}
		file = a
	}
	if format == "" {
		return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam 값 필요"}
	}
	return format, file, nil
}

// importMigrateOutcome — 1회 실행 결과 집계.
type importMigrateOutcome struct {
	imported, duplicated, superseded, remapped, skipped int
	idmap                                               map[int]int // export id → 적립 id
	byIndex                                             []int       // 입력 행 인덱스 → 적립 id(0=미해석)
	validAt                                             map[int]time.Time
}

// rowHashRec — ExportRow 1건을 (해시 입력, 적립 rec) 쌍으로 변환.
// 해시 입력은 write 경로와 동일 키만(topic·summary·tags·entity·domain·
// confidence·origin — valid_at 제외, kind·status·id 미포함) 넣어 재전송
// 멱등을 맞춘다. entities는 entity 텍스트 열로 ":" 결합(T2 원천 매핑
// 승계 — 왕복 대칭).
func rowHashRec(r ExportRow) (hashInput, rec map[string]any, se *store.SchemaError) {
	rec = map[string]any{
		"kind":       "knowledge",
		"topic":      r.Topic,
		"summary":    r.Content,
		"domain":     r.Domain,
		"confidence": r.Confidence,
		"origin":     r.Source,
	}
	if len(r.Tags) > 0 {
		rec["tags"] = r.Tags
	}
	if len(r.Entities) > 0 {
		rec["entity"] = strings.Join(r.Entities, ":")
	}
	if !r.UpdatedAt.IsZero() {
		rec["valid_at"] = r.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if r.Status != "" {
		rec["status"] = r.Status
	} else {
		rec["status"] = "active"
	}
	if r.ID > 0 {
		rec["id"] = r.ID
	}
	if se := store.Validate("knowledge", rec); se != nil {
		return nil, nil, se
	}
	// 살균(ADR-0006): import는 외부 파일 원천이다 — write 파이프라인과
	// 동일하게 Validate 뒤·해시 앞에 제어 문자·[flint] 마커 중립화 + 비밀
	// 스캔을 적용한다(살균 결과가 곧 저장·해시 본문 — 왕복 대칭 유지).
	for _, w := range SanitizePayload(rec) {
		fmt.Fprintf(os.Stderr, "[flint] import: %s\n", w)
	}
	n := store.Normalize("knowledge", rec)
	hashInput = map[string]any{}
	for _, k := range []string{"topic", "summary", "tags", "entity", "domain", "confidence", "origin"} {
		if v, ok := n[k]; ok {
			hashInput[k] = v
		}
	}
	return hashInput, rec, nil
}

// importRowsBatch — dedup 통과 행 일괄 적립(InsertBatch 1회) + id 매핑.
func importRowsBatch(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc := &importMigrateOutcome{
		idmap:   map[int]int{},
		byIndex: make([]int, len(rows)),
		validAt: map[int]time.Time{},
	}
	type pending struct {
		idx int
		row ExportRow
		rec map[string]any
	}
	var batch []pending
	seen := map[string]bool{}
	for i, r := range rows {
		if r.Kind != "" && r.Kind != "knowledge" {
			return nil, &store.SchemaError{Field: "kind", Expect: "knowledge 행만 재이식 가능"}
		}
		hashInput, rec, se := rowHashRec(r)
		if se != nil {
			return nil, se
		}
		h := store.ContentHash(store.HashCanonical(hashInput))
		if seen[h] {
			oc.duplicated++
			continue
		}
		seen[h] = true
		existsMeta, exists, err := lookupHash(ctx, st, h)
		if err != nil {
			return nil, err
		}
		if exists {
			oc.duplicated++
			if existsMeta.ID > 0 {
				oc.idmap[r.ID] = existsMeta.ID // 계보 참조 해석용
				oc.byIndex[i] = existsMeta.ID
			}
			continue
		}
		rec = store.Normalize("knowledge", rec)
		rec["content_hash"] = h // NOT NULL UNIQUE — write 경로와 동일 적립
		if vat, ok := rec["valid_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, vat); err == nil {
				oc.validAt[r.ID] = t
			}
		}
		batch = append(batch, pending{idx: i, row: r, rec: rec})
	}
	if len(batch) > 0 {
		recs := make([]map[string]any, 0, len(batch))
		for _, p := range batch {
			recs = append(recs, p.rec)
		}
		ids, err := st.InsertBatch(ctx, recs)
		if err != nil {
			return nil, err
		}
		for i, p := range batch {
			id := ids[i]
			oc.imported++
			if p.row.ID > 0 && id != p.row.ID {
				oc.remapped++ // id 보존 불가 — 자동 할당 후 매핑 보고
			}
			oc.idmap[p.row.ID] = id
			oc.byIndex[p.idx] = id
			if t, ok := oc.validAt[p.row.ID]; ok {
				oc.validAt[id] = t // 재할당 id로도 참조 가능하게
			}
		}
	}
	return oc, nil
}

// supersedeRefStore — 계보 참조 복원 capability(v1.3.0 T9). realStore는
// db.Repo.SetSupersededRef로 구현(cli/import_migrate.go 위임). 쓰기 SQL은
// db.Repo 단일 경로(writes_through_repo 가드) — knowledge는 capability
// 단언으로만 소비한다.
type supersedeRefStore interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
}

// restoreSupersede — 계보 복원 후처리: superseded_by 참조 행을 기존
// Supersede 전이로 갱신(from→to id 매핑 후). 전이 불가(이미 invalidated
// 스냅샷 복원 등 ErrNotFound)면 SetSupersededRef로 참조만 복원하고,
// 둘 다 없는 store는 경고 후 스킵한다(직접 쓰기 SQL 금지).
func restoreSupersede(ctx context.Context, st wire.Store, oc *importMigrateOutcome, refs [][2]int) (int, error) {
	n := 0
	for _, pair := range refs {
		oldID, newID := pair[0], pair[1]
		at, ok := oc.validAt[newID]
		if !ok {
			at = time.Now().UTC()
		}
		done := false
		if ss, ok := st.(supersedeStore); ok {
			_, err := ss.Supersede(ctx, oldID, newID, at)
			if err != nil {
				if !errors.Is(err, db.ErrNotFound) {
					return n, err
				}
			} else {
				n++
				done = true
			}
		}
		if !done {
			if rs, ok := st.(supersedeRefStore); ok {
				if _, err := rs.SetSupersededRef(ctx, oldID, newID); err != nil {
					return n, err
				}
				n++ // 0행(이미 참조 존재)도 멱등 복원으로 계상
				continue
			}
			fmt.Fprintf(os.Stderr, "[flint] import: id=%d 계보 복원 미지원 store — 스킵(→ %d)\n", oldID, newID)
		}
	}
	return n, nil
}

// resolveRef — export id → 실제 적립 id(배치 내 매핑 → 기존 행 확인).
func resolveRef(ctx context.Context, st wire.Store, oc *importMigrateOutcome, id int) (int, bool) {
	if id <= 0 {
		return 0, false
	}
	if got, ok := oc.idmap[id]; ok {
		return got, true
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT id FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 {
		return 0, false
	}
	got := 0
	switch v := rows[0][0].(type) {
	case int:
		got = v
	case int16:
		got = int(v)
	case int32:
		got = int(v)
	case int64:
		got = int(v)
	default:
		if _, perr := fmt.Sscan(fmt.Sprint(v), &got); perr != nil {
			return 0, false
		}
	}
	if got <= 0 {
		return 0, false
	}
	oc.idmap[id] = got
	return got, true
}

// importJSONL — jsonl 재이식 본체(파싱은 완료된 rows).
func importJSONL(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc, err := importRowsBatch(ctx, st, rows)
	if err != nil {
		return nil, err
	}
	var refs [][2]int
	for _, r := range rows {
		if r.SupersededBy == nil {
			continue
		}
		newID, ok := resolveRef(ctx, st, oc, *r.SupersededBy)
		if !ok {
			fmt.Fprintf(os.Stderr, "[flint] import jsonl: id=%d의 superseded_by=%d 대상 부재 — 계보 스킵\n", r.ID, *r.SupersededBy)
			continue
		}
		oldID, ok := resolveRef(ctx, st, oc, r.ID)
		if !ok {
			continue
		}
		refs = append(refs, [2]int{oldID, newID})
	}
	if len(refs) > 0 {
		k, err := restoreSupersede(ctx, st, oc, refs)
		if err != nil {
			return nil, err
		}
		oc.superseded = k
	}
	return oc, nil
}

// importPAM — PAM 후보 pending 적립(승격 모델 준수 — approve 경로 승계) +
// relations supersedes 계보 복원. topic은 PAM memory id에서 파생(import.go
// "규칙[헤딩]" 관례).
func importPAM(ctx context.Context, st wire.Store, res *PAMResult) (*importMigrateOutcome, error) {
	var rows []ExportRow
	skipped := len(res.Skipped)
	for _, c := range res.Candidates {
		if c.Kind != "knowledge" {
			skipped++
			fmt.Fprintf(os.Stderr, "[flint] import pam: 비 knowledge 후보 스킵(id=%s kind=%s)\n", c.ID, c.Kind)
			continue
		}
		rows = append(rows, ExportRow{
			Topic:      render.Text(render.TruncateRunes("pam["+c.ID+"]", 80)),
			Content:    c.Content,
			Kind:       "knowledge",
			Status:     "pending",
			Confidence: c.Confidence,
			Tags:       c.Tags,
			Source:     c.Source,
		})
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(os.Stderr, "[flint] import pam: 스킵(id=%s status=%s)\n", s.ID, s.Status)
	}
	oc, err := importRowsBatch(ctx, st, rows)
	if err != nil {
		return nil, err
	}
	oc.skipped = skipped
	// 후보 PAM id → 적립 id 연결(행 인덱스 — 중복 적립은 lookupHash 메타).
	pamID := map[string]int{}
	idx := 0
	for _, c := range res.Candidates {
		if c.Kind != "knowledge" {
			continue
		}
		if idx < len(oc.byIndex) {
			pamID[c.ID] = oc.byIndex[idx]
		}
		idx++
	}
	var refs [][2]int
	for _, p := range res.SupersedePairs {
		oldID, okFrom := pamID[p.From]
		newID, okTo := pamID[p.To]
		if !okFrom || oldID <= 0 || !okTo || newID <= 0 {
			fmt.Fprintf(os.Stderr, "[flint] import pam: supersedes(%s→%s) 대상 부재 — 계보 스킵\n", p.From, p.To)
			continue
		}
		refs = append(refs, [2]int{oldID, newID})
	}
	if len(refs) > 0 {
		k, err := restoreSupersede(ctx, st, oc, refs)
		if err != nil {
			return nil, err
		}
		oc.superseded = k
	}
	return oc, nil
}

// RunImportRouted — "import" 커맨드 라우팅(v1.3.0 T9): --format 있으면
// 마이그레이션 경로, 없으면 기존 규칙 변환 RunImport(불변). 등록은
// knowledge.go가 이 핸들러로 1줄 위임한다.
func RunImportRouted(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for _, a := range args {
		if a == "--format" || strings.HasPrefix(a, "--format=") {
			return RunImportMigrate(ctx, st, args, stdin, stdout)
		}
	}
	return RunImport(ctx, st, args, stdin, stdout)
}

// RunImportMigrate — `flint import --format jsonl|pam [FILE]`(FILE 미지정
// stdin). 무효 format·파손 jsonl exit 3, 서버다운 exit 2(MapExit 재사용).
func RunImportMigrate(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	format, file, err := parseImportMigrateArgs(args)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	var data []byte
	if file != "" {
		data, err = os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "file", Expect: "읽기 가능한 jsonl 파일"}))
			return 3
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "stdin", Expect: "readable"}))
			return 3
		}
	}
	var oc *importMigrateOutcome
	switch format {
	case "jsonl":
		rows, perr := ParseJSONLRows(data)
		if perr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(perr))
			return 3
		}
		oc, err = importJSONL(ctx, st, rows)
	case "pam":
		if ParsePAMFn == nil { // T10 seam 미준비
			fmt.Fprintln(stdout, `{"error":"pam import 미지원"}`)
			return 3
		}
		var res *PAMResult
		res, err = ParsePAMFn(data)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 3
		}
		oc, err = importPAM(ctx, st, res)
	}
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if oc.imported > 0 || oc.superseded > 0 {
		if _, cerr := st.Commit(ctx, fmt.Sprintf(
			"import --format %s: imported=%d duplicated=%d superseded=%d [session:0]",
			format, oc.imported, oc.duplicated, oc.superseded)); cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
	}
	fmt.Fprintln(stdout, importMigrateSummary(oc.imported, oc.duplicated, oc.superseded, oc.remapped, oc.skipped))
	return 0
}
