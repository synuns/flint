// export_pam_test.go — v1.4.0 T1: FormatPAM 계약 테스트.
// PAM v1.0(portable-ai-memory.org/spec/v1.0) §4 문서 형식·§6 content_hash
// 정규화·status 매핑·§13.2 relations 방향·§15 체크섬 결정성.
package knowledge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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

// --- v1.6.0 T4: §13.1 relations 필수필드·§18 서명·키 관리 ---

// pamUUIDRe — relation id(UUID) 형식.
var pamUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// TestFormatPAMRelationRequiredFields — §13.1: relations 각 항목은
// {id, from, to, type, created_at} 전부 발행. id는 ULID/UUID(여기선
// 결정적 UUIDv5), created_at은 대체 원천(신규 행)의 생성 시각.
func TestFormatPAMRelationRequiredFields(t *testing.T) {
	sup := 12
	t0 := time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)
	newAt := t0.Add(2 * time.Hour)
	rows := []ExportRow{
		{
			ID: 8, Topic: "demo-old", Content: "대체되는 데모 행", Kind: "note",
			Status: "invalidated", Confidence: 0.5, Tags: []string{"demo"},
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: &sup,
		},
		{
			ID: 12, Topic: "demo-new", Content: "대체하는 데모 행", Kind: "note",
			Status: "active", Confidence: 0.8, Tags: []string{"demo"},
			CreatedAt: newAt, UpdatedAt: newAt,
		},
	}
	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	rels, _ := pamParseDoc(t, out)["relations"].([]any)
	if len(rels) != 1 {
		t.Fatalf("relations 수 = %d, want 1", len(rels))
	}
	r, _ := rels[0].(map[string]any)
	if id, _ := r["id"].(string); !pamUUIDRe.MatchString(id) {
		t.Errorf("relation.id = %v, want UUID 형식", r["id"])
	}
	if r["from"] != "flint:12" || r["to"] != "flint:8" || r["type"] != "supersedes" {
		t.Errorf("relation from/to/type = %v/%v/%v, want flint:12/flint:8/supersedes",
			r["from"], r["to"], r["type"])
	}
	if r["created_at"] != newAt.UTC().Format(time.RFC3339) {
		t.Errorf("relation.created_at = %v, want 신규 행 생성 시각 %v",
			r["created_at"], newAt.UTC().Format(time.RFC3339))
	}
	// relation id는 결정적 — 동일 rows 재발행 시 동일 id(외부 검증 재현성).
	out2, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("2차 FormatPAM error: %v", err)
	}
	rels2, _ := pamParseDoc(t, out2)["relations"].([]any)
	if len(rels2) != 1 || rels2[0].(map[string]any)["id"] != r["id"] {
		t.Errorf("relation.id 비결정적: %v vs %v", rels2[0].(map[string]any)["id"], r["id"])
	}
}

// TestFormatPAMUnsignedNoOwnerOrSignature — 무서명 기본 경로는 owner·
// signature 블록을 발행하지 않는다(서명 없는 기본 경로 불변 — T5 바이트
// 동일성 기준선).
func TestFormatPAMUnsignedNoOwnerOrSignature(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)
	if _, ok := doc["owner"]; ok {
		t.Errorf("무서명 경로에 owner 블록 존재")
	}
	if _, ok := doc["signature"]; ok {
		t.Errorf("무서명 경로에 signature 블록 존재")
	}
}

// TestFormatPAMUnsignedBodyGolden — 무서명 기본 경로 본문 골든(마스킹:
// export_id·export_date 제외). T5 메모리 최적화의 출력 바이트 동일성
// 회귀 기준 — 이 골든이 바뀌면 체크섬 상호운용이 깨진 것이다.
func TestFormatPAMUnsignedBodyGolden(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	got := pamMaskedBody(t, out)
	want := `{"exported_by":"flint-test","integrity":{"checksum":"sha256:e59912fd647a1557b1b50d5bc9ed337441ce3c6c1e68e934a7fc2b5eaf88b03c","total_memories":2},"memories":[{"confidence":{"current":0.75},"content":"첫 번째 데모 지식 행","content_hash":"sha256:6f12c4490a2f72c4cba67c47013a15f06dc0934bdb39bef78b2eb7653fd4d1a4","custom_type":"lesson","id":"flint:7","provenance":{"platform":"flint"},"status":"active","tags":["go","export"],"temporal":{"created_at":"2026-11-01T09:00:00Z"},"type":"custom"},{"confidence":{"current":0.5},"content":"두 번째 데모 지식 행(대체됨)","content_hash":"sha256:ebb989d09e910a60371820b0cfcd917c5a8bedc84533292ffacff04361bba07d","custom_type":"note","id":"flint:8","provenance":{"platform":"flint"},"status":"superseded","tags":[],"temporal":{"created_at":"2026-11-02T10:30:00Z","superseded_by":"flint:12"},"type":"custom"}],"relations":[],"schema":"portable-ai-memory","schema_version":"1.0","spec_uri":"https://portable-ai-memory.org/spec/v1.0"}`
	if got != want {
		t.Errorf("무서명 본문 골든 불일치(T5 바이트 동일성 회귀):\ngot:  %s\nwant: %s", got, want)
	}
}

// pamTestKey — 테스트 고정 Ed25519 키(합성 — 32바이트 시드에서 유도).
func pamTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed := sha256.Sum256([]byte("flint-pam-demo-seed"))
	key := ed25519.NewKeyFromSeed(seed[:])
	return key
}

// TestFormatPAMSignedOwnerAndSignature — §18: --sign 경로는 루트 owner
// {id: did:key:ed25519 공개키}·signature{algorithm, signed_by, value}를
// 발행. 서명은 payload{checksum, export_id, export_date, owner_id} JCS
// → Ed25519 — 문서 외부에서 did:key 공개키로 재검증된다.
func TestFormatPAMSignedOwnerAndSignature(t *testing.T) {
	key := pamTestKey(t)
	out, err := FormatPAMSigned(pamFixtureRows(), "flint-test", key)
	if err != nil {
		t.Fatalf("FormatPAMSigned error: %v", err)
	}
	doc := pamParseDoc(t, out)

	owner, _ := doc["owner"].(map[string]any)
	if owner == nil {
		t.Fatalf("owner 블록 누락")
	}
	did, _ := owner["id"].(string)
	wantDID := PAMOwnerDID(key.Public().(ed25519.PublicKey))
	if did != wantDID {
		t.Errorf("owner.id = %q, want did:key 공개키 %q", did, wantDID)
	}
	if !strings.HasPrefix(did, "did:key:z6Mk") {
		t.Errorf("owner.id = %q, want ed25519 did:key(z6Mk 접두)", did)
	}

	sig, _ := doc["signature"].(map[string]any)
	if sig == nil {
		t.Fatalf("signature 블록 누락")
	}
	if sig["algorithm"] != "ed25519" {
		t.Errorf("signature.algorithm = %v, want ed25519", sig["algorithm"])
	}
	if sig["signed_by"] != did {
		t.Errorf("signature.signed_by = %v, want owner.id %v", sig["signed_by"], did)
	}
	value, _ := sig["value"].(string)
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatalf("signature.value = %q, want base64 64바이트 서명", value)
	}

	// 문서 외부 독립 재검증: did:key → 공개키 → payload JCS → Verify.
	pub, err := PAMDIDKeyPubkey(did)
	if err != nil {
		t.Fatalf("did:key 공개키 복호 실패: %v", err)
	}
	ig, _ := doc["integrity"].(map[string]any)
	payload := pamSignPayload{
		Checksum:   ig["checksum"].(string),
		ExportID:   doc["export_id"].(string),
		ExportDate: doc["export_date"].(string),
		OwnerID:    did,
	}
	if !PAMVerifySignature(pub, value, payload) {
		t.Errorf("§18 서명 독립 재검증 실패 — payload JCS+Ed25519 불일치")
	}
}

// TestFormatPAMSignedInvalidKey — 잘못된 길이 키는 오류(문서 발행 금지).
func TestFormatPAMSignedInvalidKey(t *testing.T) {
	if _, err := FormatPAMSigned(pamFixtureRows(), "flint-test", ed25519.PrivateKey(make([]byte, 10))); err == nil {
		t.Errorf("잘못된 키 길이(10) 오류 없음 — 오류 필요")
	}
}

// TestFormatPAMSignedUnsignedBodySame — 서명 경로의 memories·relations·
// integrity는 무서명 경로와 동일(서명 블록만 추가 — 재직렬화 회피 조립).
func TestFormatPAMSignedUnsignedBodySame(t *testing.T) {
	plain, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := FormatPAMSigned(pamFixtureRows(), "flint-test", pamTestKey(t))
	if err != nil {
		t.Fatal(err)
	}
	mp, ms := pamParseDoc(t, plain), pamParseDoc(t, signed)
	for _, k := range []string{"memories", "relations", "integrity", "exported_by"} {
		a, _ := json.Marshal(mp[k])
		b, _ := json.Marshal(ms[k])
		if string(a) != string(b) {
			t.Errorf("서명 경로 %s 블록이 무서명과 다름(본문 불변 위반):\n%s\n%s", k, a, b)
		}
	}
}

// TestPAMSigningKeyLifecycle — sign.go 키 관리: 경로 seam 주입 시 신규
// 생성(0600·부모 디렉터리 0700)·재로드 동일 키·파손 키 오류.
func TestPAMSigningKeyLifecycle(t *testing.T) {
	// 미존재 하위 디렉터리 — MkdirAll 0700 생성 경로 검증(t.TempDir
	// 자체 디렉터리는 이미 존재해 권한 단언 대상이 아니다).
	sub := filepath.Join(t.TempDir(), "flint-config")
	path := filepath.Join(sub, "pam-signing-key")
	pamSwapKeyPath(t, path)

	k1, err := LoadOrCreatePAMSigningKey()
	if err != nil {
		t.Fatalf("신규 생성 실패: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("키 파일 미생성: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("키 파일 권한 = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("키 디렉터리 권한 = %v, want 0700", di.Mode().Perm())
	}
	k2, err := LoadOrCreatePAMSigningKey()
	if err != nil {
		t.Fatalf("재로드 실패: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Errorf("재로드 키 불일치 — 생성 시 키가 안정적으로 저장되지 않음")
	}
	// 파손 키 — 조용 재생성이 아니라 오류(신뢰 사슬 보존).
	if werr := os.WriteFile(path, []byte("zz-not-hex"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if _, err := LoadOrCreatePAMSigningKey(); err == nil {
		t.Errorf("파손 키 오류 없음 — 오류 필요(조용 재생성 금지)")
	}
}

// pamSwapKeyPath — PAMSigningKeyPathFn seam 임시 교체(테스트 격리).
func pamSwapKeyPath(t *testing.T, path string) func() {
	t.Helper()
	old := PAMSigningKeyPathFn
	PAMSigningKeyPathFn = func() string { return path }
	t.Cleanup(func() { PAMSigningKeyPathFn = old })
	return func() {}
}

// TestDefaultPAMSigningKeyPathConfigDir — 기본 경로는 FLINT_CONFIG_DIR
// 승계(configDir()/pam-signing-key).
func TestDefaultPAMSigningKeyPathConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if got := DefaultPAMSigningKeyPath(); got != filepath.Join(dir, "pam-signing-key") {
		t.Errorf("기본 키 경로 = %q, want %q", got, filepath.Join(dir, "pam-signing-key"))
	}
}

// --- v1.6.0 T5: PAM 메모리 최적화(등가성·발행 수 반환·골든·벤치) ---

// TestPAMChecksumStreamEquivalence — R30-A P3: JCS 체크섬 스트리밍
// 산출(재파싱 회피)이 라이브러리 경로(json.Marshal →
// jsoncanonicalizer.Transform)와 **모든 입력에서 동일한 체크섬**을 내는지
// 검증한다. 이스케이프 경계값 전부(HTML·제어문자·U+2028/9·invalid
// UTF-8·비-BMP)·부동소수점 경계값(지수형·최단표현)·빈 값·relations 포함.
func TestPAMChecksumStreamEquivalence(t *testing.T) {
	contents := []string{
		"plain ascii demo",
		`<html> & "quotes" \ backslash /slash`,
		"ctl\u0001\u0008\b\f\n\r\t end",
		"line\u2028sep\u2029mark",
		"한글 혼합 😀 emoji 記憶",
		"invalid\xff utf8 \xfe\xfd byte",
		"",
	}
	confidences := []float64{0, 0.5, 0.75, 0.1, 0.005, 0.0000015, 1, 1e-7, 0.123456789012345}
	t0 := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	sup := 900001
	rows := make([]ExportRow, 0, len(contents)*len(confidences)+2)
	i := 0
	for _, c := range contents {
		for _, cf := range confidences {
			i++
			var tags []string
			if i%3 == 0 {
				tags = []string{"demo", "go"}
			} else if i%3 == 1 {
				tags = []string{}
			} // i%3==2 → nil(빈 배열 발행 경로)
			rows = append(rows, ExportRow{
				ID: i, Topic: "demo-topic", Content: c, Kind: "lesson",
				Status: "active", Confidence: cf, Tags: tags,
				CreatedAt: t0.Add(time.Duration(i) * time.Minute),
			})
		}
	}
	// invalidated + relation(양쪽 발행 — from=900001 신규 행도 추가).
	rows = append(rows,
		ExportRow{ID: 900000, Content: "대체된 데모 행", Kind: "note", Status: "invalidated",
			Confidence: 0.5, Tags: []string{"demo"}, CreatedAt: t0, SupersededBy: &sup},
		ExportRow{ID: 900001, Content: "대체하는 데모 행", Kind: "note", Status: "active",
			Confidence: 0.8, Tags: nil, CreatedAt: t0.Add(time.Hour)},
	)

	out, err := FormatPAM(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAM error: %v", err)
	}
	doc := pamParseDoc(t, out)

	// 라이브러리 독립 재현(구현 방식과 무관한 체크섬 계약).
	memBytes, err := json.Marshal(doc["memories"])
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsoncanonicalizer.Transform(memBytes)
	if err != nil {
		t.Fatalf("JCS 변환 실패: %v", err)
	}
	sum := sha256.Sum256(canonical)
	want := "sha256:" + hex.EncodeToString(sum[:])
	got, _ := doc["integrity"].(map[string]any)["checksum"].(string)
	if got != want {
		t.Fatalf("스트리밍 체크섬 = %q, want 라이브러리 재현 %q", got, want)
	}
	rels, _ := doc["relations"].([]any)
	if len(rels) != 1 {
		t.Fatalf("relations 수 = %d, want 1", len(rels))
	}
}

// TestFormatPAMWithCount — v1.6.0 T5 P6: 발행 수(total_memories)를
// 문서 재파싱 없이 함께 반환한다(cli 44MB 재 Unmarshal 제거 seam).
// pending은 발행 수에서 제외(기존 계약 승계).
func TestFormatPAMWithCount(t *testing.T) {
	rows := append(pamFixtureRows(), ExportRow{
		ID: 9, Content: "미승인 내부 후보 행", Kind: "note", Status: "pending",
		Confidence: 0.5, CreatedAt: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
	})
	out, issued, err := FormatPAMWithCount(rows, "flint-test")
	if err != nil {
		t.Fatalf("FormatPAMWithCount error: %v", err)
	}
	if issued != 2 {
		t.Errorf("issued = %d, want 2(pending 제외 발행 수)", issued)
	}
	ig, _ := pamParseDoc(t, out)["integrity"].(map[string]any)
	if ig["total_memories"] != float64(issued) {
		t.Errorf("total_memories = %v, want 발행 수 %d와 일치", ig["total_memories"], issued)
	}

	signed, signedIssued, err := FormatPAMSignedWithCount(pamFixtureRows(), "flint-test", pamTestKey(t))
	if err != nil {
		t.Fatalf("FormatPAMSignedWithCount error: %v", err)
	}
	if signedIssued != 2 {
		t.Errorf("signed issued = %d, want 2", signedIssued)
	}
	sig, _ := pamParseDoc(t, signed)["signature"].(map[string]any)
	if sig == nil {
		t.Errorf("서명 경로인데 signature 블록 부재")
	}
}

// TestFormatPAMRawBytesMatchEncoder — 문서 인코딩을 스트리밍 기록기로
// 바꿔도 출력 바이트는 json.Encoder(SetEscapeHTML(false)+SetIndent)와
// 전체 문서 수준에서 동일해야 한다(무서명 본문 바이트 불변 — 인덴트·
// 개행·이스케이프 전부). 휘발 필드(export_id·export_date)는 파싱 후
// 재조립해 동일값 기준으로 비교한다.
func TestFormatPAMRawBytesMatchEncoder(t *testing.T) {
	run := func(t *testing.T, out []byte) {
		t.Helper()
		var doc pamExportDoc
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("Unmarshal 실패: %v", err)
		}
		var want bytes.Buffer
		enc := json.NewEncoder(&want)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			t.Fatalf("기준 Encoder 실패: %v", err)
		}
		if !bytes.Equal(out, want.Bytes()) {
			t.Fatalf("출력 바이트가 Encoder 기준과 불일치(스트리밍 인코더 회귀):\ngot:  %q\nwant: %q", out, want.Bytes())
		}
	}
	plain, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	run(t, plain)
	signed, err := FormatPAMSigned(pamFixtureRows(), "flint-test", pamTestKey(t))
	if err != nil {
		t.Fatal(err)
	}
	run(t, signed)
	// relations 1건 발행 케이스(비어 있지 않은 배열의 인덴트 경로)·
	// 빈 rows 케이스(memories·relations 모두 빈 배열).
	relRows := []ExportRow{
		{ID: 8, Content: "대체되는 데모 행", Kind: "note", Status: "invalidated",
			Confidence: 0.5, Tags: []string{"demo"}, CreatedAt: time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)},
		{ID: 12, Content: "대체하는 데모 행", Kind: "note", Status: "active",
			Confidence: 0.8, Tags: nil, CreatedAt: time.Date(2026, 11, 2, 12, 30, 0, 0, time.UTC)},
	}
	relRows[0].SupersededBy = &[]int{12}[0]
	withRels, err := FormatPAM(relRows, "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	run(t, withRels)
	empty, err := FormatPAM(nil, "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	run(t, empty)
}

// pamBenchRows — 합성 벤치 입력(결정적 — 고정 시드). 내용 길이·상태·
// 태그를 섞고 일부 행에 supersedes relation을 만든다.
func pamBenchRows(n int) []ExportRow {
	t0 := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	rows := make([]ExportRow, n)
	for i := 0; i < n; i++ {
		r := &rows[i]
		r.ID = i + 1
		r.Topic = "demo-topic"
		r.Kind = "lesson"
		r.Status = "active"
		r.Confidence = 0.5 + float64(i%50)/100
		r.CreatedAt = t0.Add(time.Duration(i) * time.Second)
		r.UpdatedAt = r.CreatedAt
		// 내용 — 가변 길이(40~240바이트), 일부 유니코드.
		base := "합성 데모 지식 행 synthetic demo knowledge row with some length "
		for len(base) < 40+(i%200) {
			base += "x"
		}
		r.Content = base
		switch i % 3 {
		case 0:
			r.Tags = []string{"demo", "go"}
		case 1:
			r.Tags = []string{}
		}
		if i%10 == 0 && i+1 < n {
			sup := i + 2
			r.Status = "invalidated"
			r.SupersededBy = &sup
		}
	}
	return rows
}

// BenchmarkFormatPAM — R30-A 실측 재현: 50k행 churn 876MB(출력 8.6배)의
// 20k 합성 환산. 최적화 전후 totalAlloc(B/op) 비교 보고가 목적.
func BenchmarkFormatPAM(b *testing.B) {
	rows := pamBenchRows(20000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FormatPAM(rows, "flint-bench"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatPAMSigned — 서명 경로 포함 전후 측정(§18).
func BenchmarkFormatPAMSigned(b *testing.B) {
	rows := pamBenchRows(20000)
	seed := sha256.Sum256([]byte("flint-pam-demo-seed"))
	key := ed25519.NewKeyFromSeed(seed[:])
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FormatPAMSigned(rows, "flint-bench", key); err != nil {
			b.Fatal(err)
		}
	}
}

// TestPAMOwnerDIDRoundtrip — did:key 인코딩 왕복: 공개키 → z6Mk 접두
// did → 복호 시 원 공개키.
func TestPAMOwnerDIDRoundtrip(t *testing.T) {
	key := pamTestKey(t)
	did := PAMOwnerDID(key.Public().(ed25519.PublicKey))
	if !strings.HasPrefix(did, "did:key:z6Mk") {
		t.Errorf("did = %q, want z6Mk 접두(ed25519 multicodec)", did)
	}
	pub, err := PAMDIDKeyPubkey(did)
	if err != nil {
		t.Fatalf("복호 실패: %v", err)
	}
	if !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
		t.Errorf("왕복 공개키 불일치")
	}
	if _, err := PAMDIDKeyPubkey("did:key:notz6mk"); err == nil {
		t.Errorf("비-ed25519 did에서 오류 없음")
	}
}
