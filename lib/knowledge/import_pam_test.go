// import_pam_test.go — v1.3.0 T10: PAM(Portable AI Memory) v1.0 파서
// 테스트. 전부 합성 데모 데이터(개인 데이터 금지 — CLAUDE.md 최상위 원칙).
// ParsePAM은 순수 변환(스토어 무접촉): pending 적립·write는 T9 소유.
package knowledge

import (
	"strings"
	"testing"
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
