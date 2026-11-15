// export_pam_test.go — v1.4.0 T1: FormatPAM 계약 테스트.
// PAM v1.0(portable-ai-memory.org/spec/v1.0) §4 문서 형식·§6 content_hash
// 정규화·status 매핑·§13.2 relations 방향·§15 체크섬 결정성.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"golang.org/x/text/unicode/norm"
)

// pamFixtureRows — 합성 데모 2행(활성 + 무효/대체됨).
func pamFixtureRows() []ExportRow {
	sup := 12
	return []ExportRow{
		{
			ID: 7, Topic: "demo-topic", Content: "첫 번째 데모 지식 행",
			Kind: "lesson", Status: "active", Confidence: 0.75,
			Tags: []string{"go", "export"}, Domain: "demo", Source: "agent",
			ContentHash: "flint-hash-7",
			CreatedAt:   time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			ID: 8, Topic: "demo-topic-2", Content: "두 번째 데모 지식 행(대체됨)",
			Kind: "note", Status: "invalidated", Confidence: 0.5,
			Tags: nil, Domain: "demo", Source: "agent",
			ContentHash:  "flint-hash-8",
			CreatedAt:    time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC),
			SupersededBy: &sup,
		},
	}
}

// pamWantHash — 계약 독립 재현: trim→lower→NFC→연속 공백 collapse→SHA-256.
func pamWantHash(t *testing.T, content string) string {
	t.Helper()
	s := strings.TrimSpace(strings.ToLower(content))
	s = string(norm.NFC.String(s))
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pamParseDoc — 출력을 map으로 역직렬화(공통 헬퍼).
func pamParseDoc(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("PAM 문서 Unmarshal 실패: %v\n%s", err, out)
	}
	return m
}

func pamMem(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	mems, ok := doc["memories"].([]any)
	if !ok || i >= len(mems) {
		t.Fatalf("memories[%d] 접근 불가", i)
	}
	m, ok := mems[i].(map[string]any)
	if !ok {
		t.Fatalf("memories[%d]가 객체 아님", i)
	}
	return m
}

// TestFormatPAMDocShape — §4 루트 필드·§4 memory 매핑 전수 검증.
func TestFormatPAMDocShape(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)

	for k, want := range map[string]any{
		"schema":         "portable-ai-memory",
		"schema_version": "1.0",
		"exported_by":    "flint-test",
	} {
		if got := doc[k]; got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if uri, _ := doc["spec_uri"].(string); !strings.Contains(uri, "portable-ai-memory.org/spec") {
		t.Errorf("spec_uri = %v, want spec URI", doc["spec_uri"])
	}
	if _, ok := doc["export_date"]; !ok {
		t.Errorf("export_date 누락")
	} else if d, _ := doc["export_date"].(string); d == "" {
		t.Errorf("export_date 빈 문자열")
	}
	if _, ok := doc["memories"]; !ok {
		t.Fatalf("memories 누락")
	}
	if _, ok := doc["relations"]; !ok {
		t.Fatalf("relations 누락")
	}

	// memory[0] — 활성 행 매핑.
	m0 := pamMem(t, doc, 0)
	if m0["id"] != "flint:7" {
		t.Errorf("id = %v, want flint:7", m0["id"])
	}
	if m0["type"] != "custom" {
		t.Errorf("type = %v, want custom", m0["type"])
	}
	if m0["custom_type"] != "lesson" {
		t.Errorf("custom_type = %v, want lesson", m0["custom_type"])
	}
	if m0["content"] != "첫 번째 데모 지식 행" {
		t.Errorf("content 불일치: %v", m0["content"])
	}
	if got, want := m0["content_hash"], pamWantHash(t, "첫 번째 데모 지식 행"); got != want {
		t.Errorf("content_hash = %v, want %v", got, want)
	}
	temp0, _ := m0["temporal"].(map[string]any)
	if temp0 == nil {
		t.Fatalf("temporal 누락")
	}
	if temp0["created_at"] != "2026-11-01T09:00:00Z" {
		t.Errorf("temporal.created_at = %v, want 2026-11-01T09:00:00Z", temp0["created_at"])
	}
	if m0["status"] != "active" {
		t.Errorf("status = %v, want active", m0["status"])
	}
	tags, _ := m0["tags"].([]any)
	if len(tags) != 2 || tags[0] != "go" || tags[1] != "export" {
		t.Errorf("tags = %v, want [go export]", m0["tags"])
	}
	conf, _ := m0["confidence"].(map[string]any)
	if conf == nil || conf["current"] != 0.75 {
		t.Errorf("confidence = %v, want {current:0.75}", m0["confidence"])
	}
	prov, _ := m0["provenance"].(map[string]any)
	if prov == nil || prov["platform"] != "flint" {
		t.Errorf("provenance = %v, want {platform:flint}", m0["provenance"])
	}

	// integrity.total_memories.
	ig, _ := doc["integrity"].(map[string]any)
	if ig == nil {
		t.Fatalf("integrity 누락")
	}
	if ig["total_memories"] != float64(2) {
		t.Errorf("total_memories = %v, want 2", ig["total_memories"])
	}
}

// TestFormatPAMExportIDUUIDv4 — export_id가 매 호출마다 갱신되는 UUIDv4.
func TestFormatPAMExportIDUUIDv4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		out, err := FormatPAM(pamFixtureRows()[:1], "flint-test")
		if err != nil {
			t.Fatalf("FormatPAM error: %v", err)
		}
		id, _ := pamParseDoc(t, out)["export_id"].(string)
		if !re.MatchString(id) {
			t.Fatalf("export_id %q가 UUIDv4 형식 아님", id)
		}
		if seen[id] {
			t.Fatalf("export_id %q 재사용됨(매 호출 신규 발급 필요)", id)
		}
		seen[id] = true
	}
}

// TestFormatPAMContentHashNormalization — §6 정규화 3케이스:
// 공백(연속·선후방), 대소문자, NFC 정규화 — 모두 동일 해시로 수렴.
func TestFormatPAMContentHashNormalization(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"whitespace", "Hello   World  ", "hello world"},
		{"case", "HELLO WORLD", "hello world"},
		// a는 NFD(A+결합 분음 부호), b는 NFC(조합 문자) — NFC 정규화 수렴 검증.
		{"nfc", " Decomposed A\u0301ccent ", "decomposed \u00c1ccent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := []ExportRow{
				{ID: 1, Kind: "knowledge", Status: "active", Content: c.a,
					CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
			}
			out, err := FormatPAM(rows, "flint-test")
			if err != nil {
				t.Fatalf("FormatPAM error: %v", err)
			}
			got, _ := pamMem(t, pamParseDoc(t, out), 0)["content_hash"].(string)
			if got != pamWantHash(t, c.a) {
				t.Fatalf("content_hash = %q, want %q(계약 재현)", got, pamWantHash(t, c.a))
			}
			if got != pamWantHash(t, c.b) {
				t.Errorf("정규화 미수렴: %q와 %q가 다른 해시", c.a, c.b)
			}
			if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
				t.Errorf("content_hash 형식 = %q, want sha256:<64hex>", got)
			}
		})
	}
}

// TestFormatPAMStatusMapping — active→active·invalidated→superseded,
// SupersededBy≠nil이면 temporal.superseded_by="flint:<N>"(nil이면 부재).
func TestFormatPAMStatusMapping(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)

	m0 := pamMem(t, doc, 0)
	if m0["status"] != "active" {
		t.Errorf("활성 행 status = %v, want active", m0["status"])
	}
	t0, _ := m0["temporal"].(map[string]any)
	if _, ok := t0["superseded_by"]; ok {
		t.Errorf("SupersededBy=nil인데 temporal.superseded_by 존재: %v", t0["superseded_by"])
	}

	m1 := pamMem(t, doc, 1)
	if m1["status"] != "superseded" {
		t.Errorf("무효 행 status = %v, want superseded", m1["status"])
	}
	t1, _ := m1["temporal"].(map[string]any)
	if t1 == nil || t1["superseded_by"] != "flint:12" {
		t.Errorf("temporal.superseded_by = %v, want flint:12", t1["superseded_by"])
	}
}

// TestFormatPAMRelationsDirection — §13.2 "Source replaces target":
// from=신규(대체하는 행 SupersededBy), to=구(대체되는 행 자신).
// 양쪽 엔드포인트(8·12)가 모두 memories에 실리는 합성 2행으로 검증
// (v1.4.1 A1 — 한쪽이라도 미포함이면 relation 자체를 발행하지 않는다).
func TestFormatPAMRelationsDirection(t *testing.T) {
	sup := 12
	t0 := time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)
	rows := []ExportRow{
		{
			ID: 8, Topic: "demo-old", Content: "대체되는 데모 행", Kind: "note",
			Status: "invalidated", Confidence: 0.5, Tags: []string{"demo"},
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: &sup,
		},
		{
			ID: 12, Topic: "demo-new", Content: "대체하는 데모 행", Kind: "note",
			Status: "active", Confidence: 0.8, Tags: []string{"demo"},
			CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(time.Hour),
		},
	}
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	rels, _ := pamParseDoc(t, out)["relations"].([]any)
	if len(rels) != 1 {
		t.Fatalf("relations 수 = %d, want 1(SupersededBy 1건, 양쪽 포함)", len(rels))
	}
	r, _ := rels[0].(map[string]any)
	if r["type"] != "supersedes" {
		t.Errorf("type = %v, want supersedes", r["type"])
	}
	if r["from"] != "flint:12" {
		t.Errorf("from = %v, want flint:12(신규)", r["from"])
	}
	if r["to"] != "flint:8" {
		t.Errorf("to = %v, want flint:8(구)", r["to"])
	}
}

// TestFormatPAMRelationsDroppedWhenEndpointMissing — v1.4.1 A1 회귀:
// relations는 From·To 양쪽 모두 memories에 실린 "flint:N"인 경우만
// 발행한다. pending superseder(미승인 대체 원천 — 문서에서 제외)·조회
// 범위 밖 superseder를 가리키는 relation은 허참조가 되므로 발행하지
// 않는다(손작성 jsonl 수입 계보 파괴 방어).
func TestFormatPAMRelationsDroppedWhenEndpointMissing(t *testing.T) {
	pendSup, absentSup := 12, 999
	t0 := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	base := func(sup int) []ExportRow {
		return []ExportRow{
			{
				ID: 7, Topic: "demo-active", Content: "활성 데모 행", Kind: "lesson",
				Status: "active", Confidence: 0.75, Tags: []string{"demo"},
				CreatedAt: t0, UpdatedAt: t0,
			},
			{
				ID: 8, Topic: "demo-old", Content: "대체된 데모 행", Kind: "note",
				Status: "invalidated", Confidence: 0.5, Tags: []string{"demo"},
				CreatedAt: t0, UpdatedAt: t0, SupersededBy: &sup,
			},
		}
	}
	cases := []struct {
		name string
		rows []ExportRow
	}{
		// superseder(12)가 pending — memories에서 제외되므로 From 허참조.
		{"pending superseder", append(base(pendSup), ExportRow{
			ID: pendSup, Topic: "demo-pending-superseder", Content: "미승인 대체 원천 행",
			Kind: "note", Status: "pending", Confidence: 0.5,
			CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(time.Hour),
		})},
		// superseder(999)가 조회 결과에 없음 — From 허참조.
		{"absent superseder", base(absentSup)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := FormatPAM(c.rows, "flint-test")
			if err != nil {
				t.Fatalf("FormatPAM error: %v", err)
			}
			doc := pamParseDoc(t, out)
			mems, _ := doc["memories"].([]any)
			if len(mems) != 2 {
				t.Fatalf("memories 수 = %d, want 2(pending 제외)", len(mems))
			}
			rels, _ := doc["relations"].([]any)
			if len(rels) != 0 {
				t.Fatalf("relations 수 = %d, want 0(허참조 relation 미발행): %v", len(rels), rels)
			}
		})
	}
}

// pamMaskedBody — export_id·export_date 제거 후 정규 직렬화(결정성 비교용).
func pamMaskedBody(t *testing.T, out []byte) string {
	t.Helper()
	m := pamParseDoc(t, out)
	delete(m, "export_id")
	delete(m, "export_date")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal 실패: %v", err)
	}
	return string(b)
}

// TestFormatPAMChecksumDeterminism — 결정성 2종:
// (a) 동일 rows 2회 호출 — 본문·체크섬 동일, (b) rows 셔플 입력 — 동일.
func TestFormatPAMChecksumDeterminism(t *testing.T) {
	rows := pamFixtureRows()

	out1, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("1차 FormatPAM error: %v", err)
	}
	out2, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("2차 FormatPAM error: %v", err)
	}
	if b1, b2 := pamMaskedBody(t, out1), pamMaskedBody(t, out2); b1 != b2 {
		t.Errorf("동일 rows 2회 본문 불일치:\n%s\n%s", b1, b2)
	}
	c1 := pamParseDoc(t, out1)["integrity"].(map[string]any)["checksum"]
	c2 := pamParseDoc(t, out2)["integrity"].(map[string]any)["checksum"]
	if c1 != c2 {
		t.Errorf("동일 rows 체크섬 불일치: %v vs %v", c1, c2)
	}

	// 셔플 — 행 순서를 뒤집어도 본문·체크섬 불변.
	shuffled := []ExportRow{rows[1], rows[0]}
	out3, err := FormatPAM(shuffled, "flint-test")
	if err != nil {
		t.Fatalf("셔플 FormatPAM error: %v", err)
	}
	if b1, b3 := pamMaskedBody(t, out1), pamMaskedBody(t, out3); b1 != b3 {
		t.Errorf("셔플 입력 본문 불일치:\n%s\n%s", b1, b3)
	}
	c3 := pamParseDoc(t, out3)["integrity"].(map[string]any)["checksum"]
	if c1 != c3 {
		t.Errorf("셔플 입력 체크섬 불일치: %v vs %v", c1, c3)
	}
}

// TestFormatPAMChecksumRecomputable — §15: memories를 id 오름차순 정렬 →
// JCS 직렬화 → SHA-256이 integrity.checksum과 정확히 일치(독립 재현).
func TestFormatPAMChecksumRecomputable(t *testing.T) {
	rows := pamFixtureRows()
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)

	memBytes, err := json.Marshal(doc["memories"])
	if err != nil {
		t.Fatalf("memories Marshal 실패: %v", err)
	}
	canonical, err := jsoncanonicalizer.Transform(memBytes)
	if err != nil {
		t.Fatalf("JCS 변환 실패: %v", err)
	}
	sum := sha256.Sum256(canonical)
	want := "sha256:" + hex.EncodeToString(sum[:])
	got, _ := doc["integrity"].(map[string]any)["checksum"].(string)
	if got != want {
		t.Errorf("checksum = %q, want %q(JCS+SHA-256 재현)", got, want)
	}

	// memories가 id 오름차순(flint:7, flint:8)인지도 확인.
	mems := doc["memories"].([]any)
	if mems[0].(map[string]any)["id"] != "flint:7" || mems[1].(map[string]any)["id"] != "flint:8" {
		t.Errorf("memories 정렬 불일치: %v, %v",
			mems[0].(map[string]any)["id"], mems[1].(map[string]any)["id"])
	}
}

// TestFormatPAMChecksumIDStringOrder — §15 id 정렬은 id **문자열**
// 오름차순: 자릿수 경계(9/10/100)에서 "flint:10" < "flint:100" < "flint:9".
// 수치 정렬이면 외부 검증자(문자열 정렬 재현)와 체크섬이 어긋난다
// (v1.4.0 리뷰 회귀 — 수치 정렬 시 이 테스트는 실패).
func TestFormatPAMChecksumIDStringOrder(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	rows := []ExportRow{
		{ID: 9, Topic: "demo-nine", Content: "합성 데모 9", Kind: "note", Status: "active", CreatedAt: t0},
		{ID: 100, Topic: "demo-hundred", Content: "합성 데모 100", Kind: "note", Status: "active", CreatedAt: t0},
		{ID: 10, Topic: "demo-ten", Content: "합성 데모 10", Kind: "note", Status: "active", CreatedAt: t0},
	}
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)

	// 문서 배열 순서 — 문자열 오름차순.
	mems, _ := doc["memories"].([]any)
	wantOrder := []string{"flint:10", "flint:100", "flint:9"}
	if len(mems) != len(wantOrder) {
		t.Fatalf("memories 수 = %d, want %d", len(mems), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got := mems[i].(map[string]any)["id"]; got != want {
			t.Errorf("memories[%d].id = %v, want %v(문자열 오름차순)", i, got, want)
		}
	}

	// 체크섬 — 검증자 재현 경로: id 문자열 정렬 → JCS → SHA-256.
	sorted := append([]any(nil), mems...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].(map[string]any)["id"].(string) < sorted[j].(map[string]any)["id"].(string)
	})
	b, err := json.Marshal(sorted)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsoncanonicalizer.Transform(b)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	want := "sha256:" + hex.EncodeToString(sum[:])
	got, _ := doc["integrity"].(map[string]any)["checksum"].(string)
	if got != want {
		t.Errorf("checksum = %q, want 문자열 정렬 재현 %q", got, want)
	}
}

// TestFormatPAMEmpty — 빈 rows: memories·relations 빈 배열, total 0, 체크섬 존재.
func TestFormatPAMEmpty(t *testing.T) {
	out, err := FormatPAM([]ExportRow{}, "flint-test")
	if err != nil {
		t.Fatalf("빈 rows 오류: %v", err)
	}
	doc := pamParseDoc(t, out)
	if doc["schema"] != "portable-ai-memory" {
		t.Errorf("눰 rows schema = %v", doc["schema"])
	}
	if mems, ok := doc["memories"].([]any); !ok || len(mems) != 0 {
		t.Errorf("memories = %v, want 빈 배열(널 아님)", doc["memories"])
	}
	if rels, ok := doc["relations"].([]any); !ok || len(rels) != 0 {
		t.Errorf("relations = %v, want 빈 배열(널 아님)", doc["relations"])
	}
	ig, _ := doc["integrity"].(map[string]any)
	if ig == nil || ig["total_memories"] != float64(0) {
		t.Errorf("total_memories = %v, want 0", doc["integrity"])
	}
	if c, _ := ig["checksum"].(string); !strings.HasPrefix(c, "sha256:") {
		t.Errorf("checksum = %v, want sha256: 접두", ig["checksum"])
	}
}

// TestFormatPAMSkipsPendingRows — pending 행은 PAM 문서에서 제외
// (룰링 v1.4.0 T1: PAM 폐쇄 분류에 미승인 후보 개념 부재 — 표준 교환
// 문서에 내부 미승인 후보를 싣지 않는다). memories·relations·
// total_memories·체크섬 전부 미포함.
func TestFormatPAMSkipsPendingRows(t *testing.T) {
	pendSup := 99 // pending 행의 superseded_by도 relations에 남으면 안 됨
	rows := append(pamFixtureRows(), ExportRow{
		ID: 9, Topic: "demo-pending", Content: "미승인 내부 후보 행",
		Kind: "note", Status: "pending", Confidence: 0.5,
		CreatedAt:    time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
		SupersededBy: &pendSup,
	})
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)
	mems, _ := doc["memories"].([]any)
	if len(mems) != 2 {
		t.Fatalf("memories 수 = %d, want 2(pending 제외)", len(mems))
	}
	if strings.Contains(string(out), "flint:9") {
		t.Errorf("pending 행 id flint:9가 문서에 존재함")
	}
	if strings.Contains(string(out), "flint:99") {
		t.Errorf("pending 행의 superseded_by flint:99가 relations에 존재함")
	}
	ig, _ := doc["integrity"].(map[string]any)
	if ig["total_memories"] != float64(2) {
		t.Errorf("total_memories = %v, want 2", ig["total_memories"])
	}
	// 체크섬도 pending 미포함 본문 기준 — 결정성 재현과 동일 산출인지 확인.
	memBytes, err := json.Marshal(doc["memories"])
	if err != nil {
		t.Fatalf("memories Marshal 실패: %v", err)
	}
	canonical, err := jsoncanonicalizer.Transform(memBytes)
	if err != nil {
		t.Fatalf("JCS 변환 실패: %v", err)
	}
	sum := sha256.Sum256(canonical)
	if ig["checksum"] != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("checksum이 pending 미포함 memories 기준 아님")
	}
}

// TestFormatPAMFlintHashIndependent — PAM content_hash는 flint 저장 해시
// (row.ContentHash)와 별도 산출 — 원천 해시와 달라야 한다.
func TestFormatPAMFlintHashIndependent(t *testing.T) {
	rows := []ExportRow{{
		ID: 1, Kind: "knowledge", Status: "active", Content: "demo",
		ContentHash: "deadbeef",
		CreatedAt:   time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}}
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	got, _ := pamMem(t, pamParseDoc(t, out), 0)["content_hash"].(string)
	if got == "deadbeef" {
		t.Errorf("content_hash가 flint 저장 해시 재사용: %q", got)
	}
	if got != pamWantHash(t, "demo") {
		t.Errorf("content_hash = %q, want %q", got, pamWantHash(t, "demo"))
	}
}
