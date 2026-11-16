// import_pam_test.go — v1.3.0 T10: PAM(Portable AI Memory) v1.0 파서
// 테스트. 전부 합성 데모 데이터(개인 데이터 금지 — CLAUDE.md 최상위 원칙).
// ParsePAM은 순수 변환(스토어 무접촉): pending 적립·write는 T9 소유.
package knowledge

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

// pamSample — 스펙 예시 형식의 최소 문서(합성).
const pamSample = `{
 "schema": "portable-ai-memory",
 "memories": [
  {
   "id": "mem-demo-1",
   "type": "fact",
   "content": "demo fact content for roundtrip",
   "content_hash": "sha256:deadbeef",
   "temporal": {"created_at": "2026-01-02T03:04:05Z"},
   "status": "active",
   "confidence": {"current": 0.8},
   "tags": ["demo", "alpha"],
   "provenance": {"platform": "claude"}
  }
 ],
 "relations": []
}`

// TestParsePAMMinimal — 최소 문서 1건 변환: kind·content·tags·source·
// created_at·confidence 매핑.
func TestParsePAMMinimal(t *testing.T) {
	res, err := ParsePAM([]byte(pamSample))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(res.Candidates))
	}
	c := res.Candidates[0]
	if c.ID != "mem-demo-1" {
		t.Errorf("ID = %q, want mem-demo-1", c.ID)
	}
	if c.Kind != "knowledge" {
		t.Errorf("Kind = %q, want knowledge", c.Kind)
	}
	if c.Content != "demo fact content for roundtrip" {
		t.Errorf("Content = %q", c.Content)
	}
	if len(c.Tags) != 2 || c.Tags[0] != "demo" || c.Tags[1] != "alpha" {
		t.Errorf("Tags = %v", c.Tags)
	}
	if c.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", c.Confidence)
	}
	if c.Source != "import:pam:claude" {
		t.Errorf("Source = %q, want import:pam:claude", c.Source)
	}
	if c.CreatedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q", c.CreatedAt)
	}
	if len(res.Skipped) != 0 || len(res.SupersedePairs) != 0 {
		t.Errorf("Skipped=%v SupersedePairs=%v, want empty", res.Skipped, res.SupersedePairs)
	}
}

// TestParsePAMTypeMapping — type→kind 매핑 6종: 표준 knowledge형 4종·
// project·custom(원 custom_type 보존).
func TestParsePAMTypeMapping(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"a1","type":"fact","content":"c1","status":"active","temporal":{"created_at":"2026-01-01T00:00:00Z"},"confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"a2","type":"preference","content":"c2","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"a3","type":"skill","content":"c3","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"a4","type":"goal","content":"c4","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"a5","type":"project","content":"c5","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}},
  {"id":"a6","type":"demo-custom-type","content":"c6","status":"active","confidence":{"current":0.5},"provenance":{"platform":"demo"}}
 ],
 "relations": []}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.Candidates) != 6 {
		t.Fatalf("candidates = %d, want 6", len(res.Candidates))
	}
	wantKinds := map[string]string{
		"a1": "knowledge", "a2": "knowledge", "a3": "knowledge",
		"a4": "knowledge", "a5": "project", "a6": "knowledge",
	}
	for _, c := range res.Candidates {
		if c.Kind != wantKinds[c.ID] {
			t.Errorf("%s Kind = %q, want %q", c.ID, c.Kind, wantKinds[c.ID])
		}
	}
	// custom → Metadata에 원 custom_type 보존
	custom := res.Candidates[5]
	if got, _ := custom.Metadata["custom_type"].(string); got != "demo-custom-type" {
		t.Errorf("custom_type metadata = %v, want demo-custom-type", custom.Metadata["custom_type"])
	}
	// 표준형에는 custom_type 보존 불필요(없어도 무방하나 있으면 안 됨)
	if _, ok := res.Candidates[4].Metadata["custom_type"]; ok {
		t.Errorf("project에 custom_type 보존됨 — 표준형은 불필요")
	}
}

// TestParsePAMStatusSkips — 비-active status 4종은 후보에서 제외되고
// Skipped 목록에 원 status로 남는다(보고용).
func TestParsePAMStatusSkips(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"s1","type":"fact","content":"old","status":"superseded","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"s2","type":"fact","content":"old","status":"deprecated","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"s3","type":"fact","content":"gone","status":"retracted","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"s4","type":"fact","content":"box","status":"archived","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"s5","type":"fact","content":"live","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}}
 ],
 "relations": []}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].ID != "s5" {
		t.Fatalf("candidates = %+v, want s5만", res.Candidates)
	}
	if len(res.Skipped) != 4 {
		t.Fatalf("skipped = %d, want 4", len(res.Skipped))
	}
	wantStatus := map[string]string{"s1": "superseded", "s2": "deprecated", "s3": "retracted", "s4": "archived"}
	for _, s := range res.Skipped {
		if s.Status != wantStatus[s.ID] {
			t.Errorf("skipped %s status = %q, want %q", s.ID, s.Status, wantStatus[s.ID])
		}
	}
}

// TestParsePAMSupersedes — relations 중 type=="supersedes"만 (from,to)
// 쌍으로 추출 — 후처리 계보 갱신용(T9). 다른 relation type은 무시.
func TestParsePAMSupersedes(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"n1","type":"fact","content":"v1","status":"superseded","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"n2","type":"fact","content":"v2","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}}
 ],
 "relations": [
  {"type": "supersedes", "from": "n2", "to": "n1"},
  {"type": "related_to", "from": "n1", "to": "n2"}
 ]}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.SupersedePairs) != 1 {
		t.Fatalf("pairs = %v, want 1", res.SupersedePairs)
	}
	p := res.SupersedePairs[0]
	if p.From != "n2" || p.To != "n1" {
		t.Errorf("pair = (%q,%q), want (n2,n1)", p.From, p.To)
	}
}

// TestParsePAMSupersedesDirection — PAM §13.2 "supersedes: Source replaces
// target": relation from=신규(대체하는 쪽), to=구(피대체). SupersedePair는
// 이 방향을 그대로 확정한다(From=신규, To=구 — 소비측 importPAM은 이
// 의미만 정합 소비).
func TestParsePAMSupersedesDirection(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"po1","type":"fact","content":"v1(구)","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"pn2","type":"fact","content":"v2(신규)","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}}
 ],
 "relations": [
  {"type": "supersedes", "from": "pn2", "to": "po1"}
 ]}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.SupersedePairs) != 1 {
		t.Fatalf("pairs = %v, want 1", res.SupersedePairs)
	}
	p := res.SupersedePairs[0]
	if p.From != "pn2" || p.To != "po1" {
		t.Errorf("pair = (From=%q, To=%q), want (From=pn2 신규, To=po1 구)", p.From, p.To)
	}
}

// TestRunImportMigratePAMSupersedesLineage — 방향 회귀(엔드투엔드):
// relation from=pn2(신규) to=po1(구)일 때 계보 갱신은 "신규가 구를
// 대체" — Supersede(oldID=po1의 적립 id, newID=pn2의 적립 id) 호출.
// 반전 시 oldID/newID가 뒤바뀐 호출로 잡힌다.
func TestRunImportMigratePAMSupersedesLineage(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"po1","type":"fact","content":"v1(구)","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}},
  {"id":"pn2","type":"fact","content":"v2(신규)","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}}
 ],
 "relations": [
  {"type": "supersedes", "from": "pn2", "to": "po1"}
 ]}`
	m := &migrateFake{fakeStore: &fakeStore{}}
	out, code := runMigrate(t, m, []string{"--format", "pam"}, doc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"superseded":1`) {
		t.Fatalf("계보 복원 요약 부재: %s", out)
	}
	if len(m.superseded) != 1 {
		t.Fatalf("Supersede 호출 = %d회, want 1", len(m.superseded))
	}
	// 후보 순서대로 적립: po1=1행째(id 1), pn2=2행째(id 2).
	if m.superseded[0].oldID != 1 || m.superseded[0].newID != 2 {
		t.Fatalf("Supersede(oldID=%d, newID=%d), want (oldID=1 po1구, newID=2 pn2신규) — 방향 반전",
			m.superseded[0].oldID, m.superseded[0].newID)
	}
}

// TestParsePAMConfidenceClamp — confidence.current 클램프 0-1. 생략 시
// import 경로 기본 0.5.
func TestParsePAMConfidenceClamp(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"h","type":"fact","content":"h","status":"active","confidence":{"current":1.7},"provenance":{"platform":"demo"}},
  {"id":"l","type":"fact","content":"l","status":"active","confidence":{"current":-0.2},"provenance":{"platform":"demo"}},
  {"id":"d","type":"fact","content":"d","status":"active","provenance":{"platform":"demo"}}
 ],
 "relations": []}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	byID := map[string]float64{}
	for _, c := range res.Candidates {
		byID[c.ID] = c.Confidence
	}
	if byID["h"] != 1 {
		t.Errorf("h = %v, want 1", byID["h"])
	}
	if byID["l"] != 0 {
		t.Errorf("l = %v, want 0", byID["l"])
	}
	if byID["d"] != 0.5 {
		t.Errorf("d(생략) = %v, want 0.5", byID["d"])
	}
}

// TestParsePAMContentOverSummary — content 우선·summary는 무시.
func TestParsePAMContentOverSummary(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"c1","type":"fact","content":"full content","summary":"short blur","status":"active","provenance":{"platform":"demo"},"confidence":{"current":0.5}}
 ],
 "relations": []}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if res.Candidates[0].Content != "full content" {
		t.Errorf("Content = %q, want full content", res.Candidates[0].Content)
	}
}

// TestParsePAMSchemaMismatch — 루트 schema가 "portable-ai-memory"가
// 아니면(빠짐 포함) 오류.
func TestParsePAMSchemaMismatch(t *testing.T) {
	cases := map[string]string{
		"다른 값": `{"schema":"something-else","memories":[],"relations":[]}`,
		"누락":   `{"memories":[],"relations":[]}`,
	}
	for name, doc := range cases {
		if _, err := ParsePAM([]byte(doc)); err == nil {
			t.Errorf("%s: 오류 없음 — schema 불일치 오류 필요", name)
		}
	}
}

// TestParsePAMCorruptJSON — 파손 json은 위치(offset)를 포함한 상세 오류.
func TestParsePAMCorruptJSON(t *testing.T) {
	bad := []byte(`{"schema":"portable-ai-memory","memories":[{"id":`)
	_, err := ParsePAM(bad)
	if err == nil {
		t.Fatal("오류 없음 — 파손 json 오류 필요")
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("오류 메시지에 위치(offset) 없음: %v", err)
	}
}

// TestParsePAMFnSeam — T9 연결 seam 변수가 ParsePAM과 동일 동작.
func TestParsePAMFnSeam(t *testing.T) {
	res, err := ParsePAMFn([]byte(pamSample))
	if err != nil {
		t.Fatalf("ParsePAMFn err: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("seam candidates = %d, want 1", len(res.Candidates))
	}
}

// --- v1.6.0 T4: integrity.checksum 수입 검증·§18 서명 검증 ---

// pamSwapImportStderr — pamImportStderr seam 교체(경고 캡처).
func pamSwapImportStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := pamImportStderr
	pamImportStderr = &buf
	t.Cleanup(func() { pamImportStderr = old })
	return &buf
}

// pamSignedDoc — 고정 키로 서명한 합성 문서(왕복·변조 검증 공통 원천).
func pamSignedDoc(t *testing.T) []byte {
	t.Helper()
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
	out, err := FormatPAMSigned(rows, "flint-test", pamTestKey(t))
	if err != nil {
		t.Fatalf("FormatPAMSigned: %v", err)
	}
	return out
}

// TestParsePAMVerifiesChecksumOwnExport — flint가 발행한 문서(무서명·
// 서명 양쪽)의 integrity.checksum은 수입측 재계산과 일치 — 조용 통과가
// 아니라 검증 후 통과다.
func TestParsePAMVerifiesChecksumOwnExport(t *testing.T) {
	stderr := pamSwapImportStderr(t)
	unsigned, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePAM(unsigned); err != nil {
		t.Fatalf("무서명 자체 발행 문서 검증 실패: %v", err)
	}
	if _, err := ParsePAM(pamSignedDoc(t)); err != nil {
		t.Fatalf("서명 문서 검증 실패: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("정상 문서에 경고 발생: %q", stderr.String())
	}
}

// TestParsePAMChecksumMismatch — 내용 변조(체크섬 불일치)는 오류 —
// 조용한 통과 금지.
func TestParsePAMChecksumMismatch(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(out, []byte("첫 번째 데모 지식 행"), []byte("변조된 본문"), 1)
	if string(tampered) == string(out) {
		t.Fatal("변조 치환 실패 — 픽스처 확인")
	}
	_, err = ParsePAM(tampered)
	if err == nil {
		t.Fatal("체크섬 불일치 오류 없음 — 오류 필요(조용 통과 금지)")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("오류 메시지에 checksum 원인 없음: %v", err)
	}
}

// TestRunImportMigratePAMChecksumMismatchExit3 — 체크섬 불일치 문서
// 수입은 exit 3(엔드투엔드).
func TestRunImportMigratePAMChecksumMismatchExit3(t *testing.T) {
	out, err := FormatPAM(pamFixtureRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(out, []byte("두 번째 데모 지식 행"), []byte("tampered body"), 1)
	m := &migrateFake{fakeStore: &fakeStore{}}
	got, code := runMigrate(t, m, []string{"--format", "pam"}, string(tampered))
	if code != 3 {
		t.Fatalf("exit = %d, want 3(체크섬 불일치); out=%s", code, got)
	}
	if !strings.Contains(got, "checksum") {
		t.Errorf("오류 출력에 checksum 원인 없음: %s", got)
	}
}

// TestParsePAMSignatureRoundtripNoWarning — §18 서명 왕복: --sign 발행
// 문서는 수입 검증에서 오류·경고 없이 통과.
func TestParsePAMSignatureRoundtripNoWarning(t *testing.T) {
	stderr := pamSwapImportStderr(t)
	res, err := ParsePAM(pamSignedDoc(t))
	if err != nil {
		t.Fatalf("서명 왕복 검증 실패: %v", err)
	}
	if len(res.Candidates) != 1 || len(res.SupersedePairs) != 1 {
		t.Errorf("왕복 후보/계보 손실: candidates=%d pairs=%d", len(res.Candidates), len(res.SupersedePairs))
	}
	if stderr.Len() != 0 {
		t.Errorf("유효 서명인데 경고 발생: %q", stderr.String())
	}
}

// TestParsePAMSignatureTamperWarnsContinues — §21.4 SHOULD: 서명 검증
// 실패(payload 변조 — 체크섬은 memories만 덮으므로 통과)는 stderr 경고
// 1행 후 계속한다(거부 아님).
func TestParsePAMSignatureTamperWarnsContinues(t *testing.T) {
	stderr := pamSwapImportStderr(t)
	doc := pamSignedDoc(t)
	re := regexp.MustCompile(`"export_date":\s*"[^"]*"`)
	tampered := re.ReplaceAll(doc, []byte(`"export_date":"1999-01-01T00:00:00Z"`))
	if string(tampered) == string(doc) {
		t.Fatal("export_date 치환 실패")
	}
	res, err := ParsePAM(tampered)
	if err != nil {
		t.Fatalf("서명 검증 실패가 오류(거부)로 취급됨 — 경고 후 계속이 계약: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Errorf("경고 후 계속이지만 후보 손실: %+v", res.Candidates)
	}
	warn := strings.TrimRight(stderr.String(), "\n")
	if !strings.Contains(warn, "signature") {
		t.Errorf("경고에 signature 원인 없음: %q", warn)
	}
	if n := len(strings.Split(warn, "\n")); n != 1 {
		t.Errorf("경고가 1행 아님(%d행): %q", n, warn)
	}
}

// TestParsePAMUnsignedNoWarning — 무서명 문서(§18 미사용)는 서명 검증
// 경고 없이 통과.
func TestParsePAMUnsignedNoWarning(t *testing.T) {
	stderr := pamSwapImportStderr(t)
	if _, err := ParsePAM([]byte(pamSample)); err != nil {
		t.Fatalf("무서명 문서 파싱 오류: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("무서명 문서에 경고 발생: %q", stderr.String())
	}
}
