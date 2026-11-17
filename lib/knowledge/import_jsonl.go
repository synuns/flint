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
			return nil, fmt.Errorf("jsonl row %d corrupt: %w", i+1, err)
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
					return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam value required"}
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
			return "", "", wire.UsageErrorf("unknown flag: %s — usage: flint import --format jsonl|pam [FILE]", a)
		}
		if file != "" {
			return "", "", &store.SchemaError{Field: "file", Expect: "one positional argument (FILE) required"}
		}
		file = a
	}
	if format == "" {
		return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam value required"}
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

// importHashSkip — 재이식 해시 입력 제외 키(write payload에 없는 이식
// 기계 키 — id·kind·status는 적립 시 부과되고 ingested_at·invalid_at은
// A4 보존 열. valid_at·ts는 HashCanonical 휘발성 제외 승계).
var importHashSkip = map[string]bool{
	"id": true, "kind": true, "status": true, "ingested_at": true, "invalid_at": true,
}

// rowHashRec — ExportRow 1건을 (해시 입력, 적립 rec) 쌍으로 변환.
// 해시 입력은 write 경로 산출식(ContentHash(HashCanonical(n)) — 전체 키)과
// 대칭이다(R19 A9: 7키 고정 부분집합은 write payload 부가 키 행과
// 비대칭). entities는 entity 텍스트 열로 ":" 결합(T2 원천 매핑 승계 —
// 왕복 대칭).
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
	// 시각 보존(R19 A4 → v1.4.0 T3 확장): ingested_at은 CreatedAt(원천
	// ingested_at), invalidated 행의 invalid_at은 UpdatedAt(export 파생
	// COALESCE(invalid_at, ingested_at)의 역매핑). valid_at은 원값 우선 —
	// jsonl 미실어 행(손작성 등)만 기존 폴백(CreatedAt → UpdatedAt) 승계.
	// 종전 valid_at←UpdatedAt/CreatedAt 치환은 검증 시각 원값을 덮어썼다
	// (재export 시각 비대칭).
	if !r.CreatedAt.IsZero() {
		rec["ingested_at"] = r.CreatedAt.UTC()
	}
	switch {
	case !r.ValidAt.IsZero():
		rec["valid_at"] = r.ValidAt.UTC()
	case !r.CreatedAt.IsZero():
		rec["valid_at"] = r.CreatedAt.UTC()
	case !r.UpdatedAt.IsZero():
		rec["valid_at"] = r.UpdatedAt.UTC()
	}
	if r.Status == "invalidated" && !r.UpdatedAt.IsZero() {
		rec["invalid_at"] = r.UpdatedAt.UTC()
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
	for k, v := range n {
		if importHashSkip[k] {
			continue
		}
		hashInput[k] = v
	}
	return hashInput, rec, nil
}

// isHex64 — 저장 content_hash 형식(64자 hex) 판정.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// importDedupChunk·importInsertChunk — v1.6.0 T2 청크 크기(R30-A P1·P2).
// dedup은 content_hash = ANY($1) 사전 조회 해시 수(1k), 적립은 청크
// 커밋 행 수(5k — 단일 대형 트랜잭션의 EOF 위험 분산). 테스트가 경계를
// 축소해 청크 동작을 검증한다(import_jsonl_test.go overrideImportChunks).
var (
	importDedupChunk  = 1000
	importInsertChunk = 5000
)

// importMaxFileBytes — import 입력 FILE 크기 상한(R33 G3, v1.6.2 T3):
// 512MB. os.ReadFile 전 stat으로 판정해 초과 문서를 슬랩에 적재하기 전에
// exit 3으로 차단한다(대량 문서 OOM 방어 — 행 수 상한과 한 쌍). var인 것은
// 테스트 경계 축소뿐(기본값 512MB가 계약).
var importMaxFileBytes int64 = 512 << 20

// lookupHashBatch — dedup 사전 조회(R30-A P1): 해시 배열을 content_hash =
// ANY($1) 청크로 1왕복씩 조회해 해시→기존 행 메타 맵을 돌려준다(행별
// SELECT N회 폐지). 맵 부재 해시 = 신규. 오류는 전파(조용한 신규 판정
// 금지 — 체인 보존으로 서버다운 exit 2 유지).
func lookupHashBatch(ctx context.Context, st wire.Store, hashes []string) (map[string]DupMeta, error) {
	existing := make(map[string]DupMeta, len(hashes))
	for start := 0; start < len(hashes); start += importDedupChunk {
		end := min(start+importDedupChunk, len(hashes))
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT content_hash, id, topic, status FROM knowledge WHERE content_hash = ANY($1)`,
			[]any{hashes[start:end]})
		if err != nil {
			return nil, fmt.Errorf("import: dedup lookup failed: %w", err)
		}
		for _, row := range rows {
			if len(row) < 4 {
				continue
			}
			h, _ := row[0].(string)
			id, ok := render.CellToInt(row[1])
			if h == "" || !ok {
				continue
			}
			topic, _ := row[2].(string)
			state, _ := row[3].(string)
			existing[h] = DupMeta{ID: id, Topic: topic, State: state}
		}
	}
	return existing, nil
}

// lookupExistingIDs — 명시 id 충돌 사전 조회(R33 G5, v1.6.2 T3): id 배열을
// id = ANY($1) 청크로 1왕복씩 조회해 존재 id 집합을 돌려준다(해시 dedup를
// 통과한 행의 명시 id가 DB에 이미 있다는 것은 내용이 다르다는 뜻 — PK
// 충돌). 오류는 전파(dedup 조회와 동일 — 조용한 재할당 판정 금지, 서버다운
// exit 2 체인 보존).
func lookupExistingIDs(ctx context.Context, st wire.Store, ids []int) (map[int]bool, error) {
	existing := make(map[int]bool, len(ids))
	for start := 0; start < len(ids); start += importDedupChunk {
		end := min(start+importDedupChunk, len(ids))
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT id FROM knowledge WHERE id = ANY($1)`,
			[]any{ids[start:end]})
		if err != nil {
			return nil, fmt.Errorf("import: id conflict lookup failed: %w", err)
		}
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			if id, ok := render.CellToInt(row[0]); ok && id > 0 {
				existing[id] = true
			}
		}
	}
	return existing, nil
}

// countJSONLRows — 비어 있지 않은 줄 수(R30-A P5): ParseJSONLRows의 행 수
// 기준(공백 줄 제외)과 동일하게 바이트 라인 스캔 1회로 센다 — 전량
// 파싱 전 상한 조기 판정용(대량 문서의 파싱 메모리 방어). 유니코드
// 공백만으로 이루어진 줄은 JSON 줄이 아니므로 고려하지 않는다.
func countJSONLRows(data []byte) int {
	n := 0
	blank := true
	for _, c := range data {
		switch {
		case c == '\n':
			if !blank {
				n++
			}
			blank = true
		case c == ' ' || c == '\t' || c == '\r':
			// 공백류 — 줄 공백 판정 유지
		default:
			blank = false
		}
	}
	if !blank {
		n++
	}
	return n
}

// importRowsBatch — dedup 통과 행 청크 적립 + id 매핑. v1.6.0 T2: ①행별
// lookupHash 대신 해시 배열 ANY($1) 청크 사전 조회 ②InsertBatch 1회 대신
// importInsertChunk행 청크 커밋(부분 성공 보존 — 실패 청크 이전 커밋은
// 유지되고 재실행은 dedup가 회복해 멱등 재개).
func importRowsBatch(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc := &importMigrateOutcome{
		idmap:   map[int]int{},
		byIndex: make([]int, len(rows)),
		validAt: map[int]time.Time{},
	}
	type pending struct {
		idx  int
		row  ExportRow
		rec  map[string]any // rowHashRec 산출(검증·살균 완료 — 미정규화)
		hash string
	}
	// 1단계 — 검증·살균·해시 산출 + 파일 내 중복 스킵(행 순서 우선).
	var fresh []pending
	seen := map[string]bool{}
	hashes := make([]string, 0, len(rows))
	for i, r := range rows {
		if r.Kind != "" && r.Kind != "knowledge" {
			return nil, &store.SchemaError{Field: "kind", Expect: "only knowledge rows can be re-imported"}
		}
		hashInput, rec, se := rowHashRec(r)
		if se != nil {
			return nil, se
		}
		// 해시(R19 A9): export가 실어온 저장 content_hash(64 hex)는 write가
		// 부가 키(session_id 등 payload 부가 키)까지 포함해 산출한 값이라
		// 재계산으로 재현 불가다 — 유효하면 승계해 dedup·적립의 왕복 대칭을
		// 맞춘다. 없는 행(손작성 jsonl)은 write 산출식으로 재계산한다.
		h := store.ContentHash(store.HashCanonical(hashInput))
		if isHex64(r.ContentHash) {
			h = r.ContentHash
		}
		if seen[h] {
			oc.duplicated++
			continue
		}
		seen[h] = true
		hashes = append(hashes, h)
		fresh = append(fresh, pending{idx: i, row: r, rec: rec, hash: h})
	}
	// 2단계 — dedup 사전 조회(ANY($1) 청크 — 행별 조회 폐지).
	existing, err := lookupHashBatch(ctx, st, hashes)
	if err != nil {
		return nil, err
	}
	// 3단계 — 기존 행은 duplicate 스킵(계보 참조 해석용 id 승계), 신규만
	// 적립 rec 완성(정규화·content_hash·valid_at).
	var batch []pending
	for _, p := range fresh {
		if meta, ok := existing[p.hash]; ok {
			oc.duplicated++
			if meta.ID > 0 {
				oc.idmap[p.row.ID] = meta.ID // 계보 참조 해석용
				oc.byIndex[p.idx] = meta.ID
			}
			continue
		}
		rec := store.Normalize("knowledge", p.rec)
		rec["content_hash"] = p.hash // NOT NULL UNIQUE — write 경로와 동일 적립
		switch v := rec["valid_at"].(type) {
		case string:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				oc.validAt[p.row.ID] = t
			}
		case time.Time:
			oc.validAt[p.row.ID] = v
		}
		batch = append(batch, pending{idx: p.idx, row: p.row, rec: rec, hash: p.hash})
	}
	// 3.5단계 — R33 G5(v1.6.2 T3): 명시 id 충돌 자동 재할당. 기존 DB 동일
	// id(다른 내용 — 동일 내용이면 2~3단계 dedup가 이미 스킵)와 파일 내
	// 중복 id는 명시 id를 제거해 serial 재할당 경로로 보낸다(재할당 행은
	// 4단계에서 id≠원본 id로 remapped 계상·요약 보고 — 배치 PK 충돌로
	// 전체 exit 1되던 왕복 주 시나리오 회복).
	explicitIDs := make([]int, 0, len(batch))
	for _, p := range batch {
		if id, ok := p.rec["id"].(int); ok && id > 0 {
			explicitIDs = append(explicitIDs, id)
		}
	}
	if len(explicitIDs) > 0 {
		existingIDs, lerr := lookupExistingIDs(ctx, st, explicitIDs)
		if lerr != nil {
			return nil, lerr
		}
		taken := map[int]bool{} // 파일 선행 행이 점유한 명시 id
		for i := range batch {
			id, ok := batch[i].rec["id"].(int)
			if !ok || id <= 0 {
				continue
			}
			if existingIDs[id] || taken[id] {
				delete(batch[i].rec, "id") // 자동 재할당 — serial 할당
				continue
			}
			taken[id] = true
		}
	}
	// 4단계 — 청크 적립+커밋: 청크 단위 부분 성공(실패 청크 이전 커밋
	// 유지), id 매핑·집계는 행 순서 불변.
	explicit := false
	for start := 0; start < len(batch); start += importInsertChunk {
		end := min(start+importInsertChunk, len(batch))
		chunk := batch[start:end]
		recs := make([]map[string]any, 0, len(chunk))
		for _, p := range chunk {
			recs = append(recs, p.rec)
		}
		ids, err := st.InsertBatch(ctx, recs)
		if err != nil {
			// R19 A1: 배치 적립 실패은 유령 성공이 될 수 없다 — 실구현
			// mapInsertErr는 unique 위반(23505: 기존 DB 동일 id·다른 content
			// 행의 PK 충돌 포함)을 ErrDuplicate로 감싸고 MapExit(ErrDuplicate)
			// =0이라 그대로 두면 "오류 JSON + exit 0 + 0건 커밋"이 된다
			// (write.go의 '전체 폐기 후 exit 0 duplicate' 금지 원칙).
			// 서버다운(exit 2)만 체인을 보존하고 나머지는 원인 문자열을
			// 보존한 채 exit 1로 매핑한다.
			if errors.Is(err, store.ErrServerDown) {
				return nil, err
			}
			return nil, fmt.Errorf("import: batch apply failed: %v", err)
		}
		for i, p := range chunk {
			id := ids[i]
			oc.imported++
			if p.row.ID > 0 {
				explicit = true
				if id != p.row.ID {
					oc.remapped++ // id 보존 불가 — 자동 할당 후 매핑 보고
				}
			}
			oc.idmap[p.row.ID] = id
			oc.byIndex[p.idx] = id
			if t, ok := oc.validAt[p.row.ID]; ok {
				oc.validAt[id] = t // 재할당 id로도 참조 가능하게
			}
		}
	}
	if explicit {
		// R19 A2: 명시 id INSERT는 id serial의 nextval을 소비하지 않는다.
		syncKnowledgeIDSequence(ctx, st)
	}
	return oc, nil
}

// syncKnowledgeIDSequence — R19 A2: 명시 id 이식 직후 id serial nextval이
// 기존 행과 충돌해 flint write가 연속 실패하지 않게 시퀀스를 max(id)+1로
// 맞춘다(setval …, false — 다음 nextval = max+1). 시퀀스명은
// pg_get_serial_sequence로 확인하고, 미지원 저장소는 표준 명칭
// knowledge_id_seq로 폴백한다. 실패는 stderr 경고만(exit 불변).
func syncKnowledgeIDSequence(ctx context.Context, st wire.Store) {
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT pg_get_serial_sequence('knowledge', 'id')`, nil); err == nil &&
		len(rows) == 1 && len(rows[0]) == 1 {
		if name, ok := rows[0][0].(string); ok && name != "" {
			if _, err := wire.QueryOnce(ctx, st, fmt.Sprintf(
				`SELECT setval('%s', (SELECT COALESCE(MAX(id), 0) + 1 FROM knowledge), false)`,
				strings.ReplaceAll(name, "'", "''")), nil); err == nil {
				return
			}
		}
	}
	if _, err := wire.QueryOnce(ctx, st,
		`SELECT setval('knowledge_id_seq', (SELECT COALESCE(MAX(id), 0) + 1 FROM knowledge), false)`, nil); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] import: id sequence sync failed (continuing after warning): %v\n", err)
	}
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
				rows, err := rs.SetSupersededRef(ctx, oldID, newID)
				if err != nil {
					return n, err
				}
				if rows > 0 {
					n++ // R19 A8: 실제 복원된 행만 계상(0행=이미 참조 존재 제외)
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "[flint] import: id=%d lineage restore unsupported by store — skipped (→ %d)\n", oldID, newID)
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
			fmt.Fprintf(os.Stderr, "[flint] import jsonl: id=%d superseded_by=%d target missing — lineage skipped\n", r.ID, *r.SupersededBy)
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
		if c.Kind != "knowledge" && c.Kind != "project" {
			skipped++
			fmt.Fprintf(os.Stderr, "[flint] import pam: non-knowledge/project candidate skipped (id=%s kind=%s)\n", c.ID, c.Kind)
			continue
		}
		row := ExportRow{
			Topic:      render.Text(render.TruncateRunes("pam["+c.ID+"]", 80)),
			Content:    c.Content,
			Kind:       "knowledge", // 저장 라우터 상수 — projects 테이블 아님(루팅 룰링 2026-11-14)
			Status:     "pending",
			Confidence: c.Confidence,
			Tags:       c.Tags,
			Source:     c.Source,
		}
		// v1.6.1 T2 A2(왕복 멱등): flint 발행 문서가 실어온 저장
		// content_hash(ParsePAM 승계 — 64 hex만)는 적립 해시로 승계해
		// dedup가 원천 해시와 대칭한다(jsonl A9 isHex64 경로와 동일).
		if h, ok := c.Metadata["content_hash"].(string); ok && isHex64(h) {
			row.ContentHash = h
		}
		if c.Kind == "project" {
			// 원천 kind 보존: knowledge 테이블 pending 적립(승격 모델)에서
			// 원형 구분은 태그로만 가능 — "pam:project" 추가(왕복·검색 경로).
			row.Tags = append(append([]string{}, c.Tags...), "pam:project")
		}
		rows = append(rows, row)
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(os.Stderr, "[flint] import pam: skipped (id=%s status=%s)\n", s.ID, s.Status)
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
		if c.Kind != "knowledge" && c.Kind != "project" {
			continue
		}
		if idx < len(oc.byIndex) {
			pamID[c.ID] = oc.byIndex[idx]
		}
		idx++
	}
	var refs [][2]int
	for _, p := range res.SupersedePairs {
		// PAM §13.2 "supersedes: Source replaces target"(import_pam.go
		// SupersedePair 계약): From=신규(대체하는 원천), To=구(피대체) —
		// Supersede(oldID=구 To의 적립 id, newID=신규 From의 적립 id).
		newID, okFrom := pamID[p.From]
		oldID, okTo := pamID[p.To]
		if !okFrom || oldID <= 0 || !okTo || newID <= 0 {
			fmt.Fprintf(os.Stderr, "[flint] import pam: supersedes(%s→%s) target missing — lineage skipped\n", p.From, p.To)
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

// importMaxRowsLimit — import 1회 행 수 상한 로드(등록표 키
// export_import_max_rows). 조회 실패·미설정(0)은 DefaultThresholds 폴백
// (v0.16.3 키 규약 — ApplyThresholdsFallback 미등록 키의 소비자 완결).
func importMaxRowsLimit(ctx context.Context, st wire.Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

// enforceImportMaxRows — 행 수 상한 초과 시 오류 JSON(max·count 포함)을
// stdout에 내고 3을 반환(호출부는 그대로 반환). 파싱 직후·적립 전 단계라
// 부작용 없음(대량 문서 전량 적립의 메모리 방어 — v1.4.0 T3).
func enforceImportMaxRows(ctx context.Context, st wire.Store, stdout io.Writer, count int) int {
	max := importMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("import row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"import row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
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
		// G3(v1.6.2 T3): 크기 상한은 os.ReadFile 앞 stat으로 판정한다 —
		// 초과 파일을 읽지 않고 exit 3(max·size 포함). stat 실패는 ReadFile
		// 오류 경로(A7 원인 보존)에 맡긴다.
		if fi, serr := os.Stat(file); serr == nil && fi.Size() > importMaxFileBytes {
			b, merr := json.Marshal(map[string]any{
				"error": fmt.Sprintf("import file size limit exceeded (%d bytes max)", importMaxFileBytes),
				"max":   importMaxFileBytes,
				"size":  fi.Size(),
			})
			if merr != nil {
				b = []byte(fmt.Sprintf(`{"error":"import file size limit exceeded","max":%d,"size":%d}`,
					importMaxFileBytes, fi.Size()))
			}
			fmt.Fprintln(stdout, string(b))
			return 3
		}
		data, err = os.ReadFile(file)
		if err != nil {
			// R19 A7: 원인(ENOENT/EACCES·경로)을 폐기하지 않고 expect에
			// 포함한다(SchemaError 랩 유지 — exit 3 불변).
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "file",
				Expect: "readable jsonl file: " + err.Error()}))
			return 3
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "stdin",
				Expect: "readable: " + err.Error()}))
			return 3
		}
	}
	var oc *importMigrateOutcome
	switch format {
	case "jsonl":
		// v1.6.0 T2(P5): 상한 판정을 전량 파싱 앞당긴다 — 라인 스캔 1회
		// 카운트로 조기 exit 3(대량 문서의 파싱 메모리 방어가 파손 행
		// 오류보다 우선).
		if code := enforceImportMaxRows(ctx, st, stdout, countJSONLRows(data)); code != 0 {
			return code
		}
		rows, perr := ParseJSONLRows(data)
		if perr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(perr))
			return 3
		}
		oc, err = importJSONL(ctx, st, rows)
	case "pam":
		if ParsePAMFn == nil { // T10 seam 미준비
			fmt.Fprintln(stdout, `{"error":"pam import unsupported"}`)
			return 3
		}
		var res *PAMResult
		res, err = ParsePAMFn(data)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 3
		}
		// 상한은 후보+스킵 합산(문서 전체 memory 행 수 기준).
		if code := enforceImportMaxRows(ctx, st, stdout, len(res.Candidates)+len(res.Skipped)); code != 0 {
			return code
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
