// import_pam_test.go — PAM (Portable AI Memory) v1.0 parser tests. All
// synthetic demo data (personal data forbidden — the CLAUDE.md
// top-level principle). ParsePAM is a pure conversion (no store
// contact): pending accrual·write belong to import_jsonl.go.
package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// pamSample — a minimal document in the spec-example shape (synthetic).
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

// TestParsePAMMinimal — converting the 1-row minimal document: the
// kind·content·tags·source·created_at·confidence mappings.
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

// TestParsePAMTypeMapping — 6 type→kind mappings: 4 standard knowledge
// shapes·project·custom (the original custom_type preserved).
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
	// custom → the original custom_type preserved in Metadata
	custom := res.Candidates[5]
	if got, _ := custom.Metadata["custom_type"].(string); got != "demo-custom-type" {
		t.Errorf("custom_type metadata = %v, want demo-custom-type", custom.Metadata["custom_type"])
	}
	// Standard shapes need no custom_type preservation (absent is fine;
	// present must not happen)
	if _, ok := res.Candidates[4].Metadata["custom_type"]; ok {
		t.Errorf("project에 custom_type 보존됨 — 표준형은 불필요")
	}
}

// TestParsePAMStatusSkips — the 4 non-active statuses are excluded from
// candidates and remain in the Skipped list with their original status
// (for reporting).
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

// TestParsePAMSupersedes — only type=="supersedes" among relations is
// extracted as (from,to) pairs — for the post-pass lineage renewal.
// Other relation types are ignored.
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

// TestParsePAMSupersedesDirection — PAM §13.2 "supersedes: Source
// replaces target": relation from=new (the replacing side), to=old (the
// replaced). SupersedePair fixes this direction as-is (From=new, To=old
// — the consumer importPAM invokes exactly this meaning).
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

// TestRunImportMigratePAMSupersedesLineage — a direction regression
// (end-to-end): with relation from=pn2 (new) to=po1 (old), the lineage
// renewal is "new replaces old" — a Supersede(oldID=po1's accrued id,
// newID=pn2's accrued id) call. A reversal shows up as a call with
// swapped oldID/newID.
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
	// Accrued in candidate order: po1=1st row (id 1), pn2=2nd row (id
	// 2).
	if m.superseded[0].oldID != 1 || m.superseded[0].newID != 2 {
		t.Fatalf("Supersede(oldID=%d, newID=%d), want (oldID=1 po1구, newID=2 pn2신규) — 방향 반전",
			m.superseded[0].oldID, m.superseded[0].newID)
	}
}

// TestParsePAMConfidenceClamp — confidence.current clamped to 0-1. When
// omitted, the import-path default 0.5.
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

// TestParsePAMContentOverSummary — content wins·summary is ignored.
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

// TestParsePAMSchemaMismatch — if the root schema is not
// "portable-ai-memory" (including missing), an error.
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

// TestParsePAMCorruptJSON — corrupt json gets a detailed error carrying
// the position (offset).
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

// TestParsePAMFnSeam — the wiring seam variable behaves identically to
// ParsePAM.
func TestParsePAMFnSeam(t *testing.T) {
	res, err := ParsePAMFn([]byte(pamSample))
	if err != nil {
		t.Fatalf("ParsePAMFn err: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("seam candidates = %d, want 1", len(res.Candidates))
	}
}

// --- integrity.checksum import verification·§18 signature verification ---

// pamSwapImportStderr — swaps the pamImportStderr seam (warning
// capture).
func pamSwapImportStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := pamImportStderr
	pamImportStderr = &buf
	t.Cleanup(func() { pamImportStderr = old })
	return &buf
}

// pamSignedDoc — a synthetic document signed with a fixed key (the
// common source for round-trip·tamper checks).
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

// TestParsePAMVerifiesChecksumOwnExport — the integrity.checksum of
// documents flint issued (unsigned·signed alike) matches the import
// side's recomputation — passing after verification, not a quiet pass.
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

// TestParsePAMChecksumMismatch — content tampering (checksum mismatch)
// is an error — no quiet pass.
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

// TestRunImportMigratePAMChecksumMismatchExit3 — importing a
// checksum-mismatched document is exit 3 (end-to-end).
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

// TestParsePAMSignatureRoundtripNoWarning — the §18 signature round
// trip: a --sign-issued document passes import verification without
// errors·warnings.
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

// TestParsePAMSignatureTamperWarnsContinues — §21.4 SHOULD: a signature
// verification failure (payload tampering — the checksum covers only
// memories, so it still passes) warns once on stderr then continues
// (not a rejection).
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

// TestParsePAMUnsignedNoWarning — an unsigned document (§18 unused)
// passes without a signature-verification warning.
func TestParsePAMUnsignedNoWarning(t *testing.T) {
	stderr := pamSwapImportStderr(t)
	if _, err := ParsePAM([]byte(pamSample)); err != nil {
		t.Fatalf("무서명 문서 파싱 오류: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("무서명 문서에 경고 발생: %q", stderr.String())
	}
}

// --- stored content_hash inheritance·import dedup idempotence ---

// TestParsePAMInheritsStoredContentHash — A2: a memory metadata.
// content_hash (64 hex) is inherited into ImportCandidate.Metadata
// (jsonl A9 isHex64 symmetry). Non-hex values·absence are not
// inherited.
func TestParsePAMInheritsStoredContentHash(t *testing.T) {
	doc := `{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"k1","type":"fact","content":"해시 보유","status":"active","confidence":{"current":0.5},"provenance":{"platform":"flint"},
   "metadata":{"content_hash":"cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"}},
  {"id":"k2","type":"fact","content":"비hex 해시","status":"active","confidence":{"current":0.5},"provenance":{"platform":"flint"},
   "metadata":{"content_hash":"not-hex"}},
  {"id":"k3","type":"fact","content":"미기재","status":"active","confidence":{"current":0.5},"provenance":{"platform":"flint"}}
 ],
 "relations": []}`
	res, err := ParsePAM([]byte(doc))
	if err != nil {
		t.Fatalf("ParsePAM err: %v", err)
	}
	if len(res.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(res.Candidates))
	}
	if got, _ := res.Candidates[0].Metadata["content_hash"].(string); got != "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd" {
		t.Errorf("k1 metadata.content_hash 미승계: %v", res.Candidates[0].Metadata)
	}
	for i, id := range []string{"k2", "k3"} {
		if _, ok := res.Candidates[i+1].Metadata["content_hash"]; ok {
			t.Errorf("%s content_hash 승계 — 비hex·미기재는 승계 금지", id)
		}
	}
}

// TestParsePAMRoundtripOwnExportStoredHash — the issue→import round
// trip: the metadata.content_hash FormatPAM emitted is inherited
// as-is into the ParsePAM candidates.
func TestParsePAMRoundtripOwnExportStoredHash(t *testing.T) {
	out, err := FormatPAM(pamStoredHashRows(), "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ParsePAM(out)
	if err != nil {
		t.Fatalf("자체 발행 문서 파싱: %v", err)
	}
	if len(res.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(res.Candidates))
	}
	if got, _ := res.Candidates[0].Metadata["content_hash"].(string); got != "abababababababababababababababababababababababababababababababab" {
		t.Errorf("왕복 저장 해시 손실: %v", res.Candidates[0].Metadata)
	}
	if _, ok := res.Candidates[1].Metadata["content_hash"]; ok {
		t.Errorf("미보유 행 해시 승계 — 발행되지 않은 값")
	}
}

// TestRunImportMigratePAMStoredHashDedupFast — the A2 wiring (fast
// fake): a candidate's metadata.content_hash is inherited into the
// accrual ExportRow.ContentHash and used as the dedup lookup hash —
// idempotent against existing rows (duplicated=all·imported=0, jsonl A9
// symmetry). Without the inheritance, the lookup uses the recomputed
// hash and the row accrues as imported.
func TestRunImportMigratePAMStoredHashDedupFast(t *testing.T) {
	const stored = "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	doc := fmt.Sprintf(`{
 "schema": "portable-ai-memory",
 "memories": [
  {"id":"m1","type":"fact","content":"저장 해시 dedmp 데모 본문","status":"active","confidence":{"current":0.5},"provenance":{"platform":"flint"},
   "metadata":{"content_hash":%q}}
 ],
 "relations": []}`, stored)
	f := &migrateFake{fakeStore: &fakeStore{hashExistsFn: func(h string) (DupMeta, bool) {
		if h == stored {
			return DupMeta{ID: 77, Topic: "demo", State: "active"}, true
		}
		return DupMeta{}, false
	}}}
	out, code := runMigrate(t, f, []string{"--format", "pam"}, doc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"duplicated":1`) {
		t.Fatalf("저장 해시 dedup 미발동 — duplicated 미충족: %s", out)
	}
	if strings.Contains(out, `"imported":1`) {
		t.Fatalf("기존 행이 신규 적립됨 — 왕복 멱등 위반: %s", out)
	}
	if len(f.fakeStore.inserted) != 0 {
		t.Fatalf("dedup 행 적립 잔여: %d건", len(f.fakeStore.inserted))
	}
}

// TestImportPAMRoundtripIdempotentRealSlow — the A2 real-DB round trip
// (slow): accrual (rows holding a stored content_hash 64 hex) → export
// pam → import pam is all duplicates (duplicated=all·imported=0 —
// round-trip idempotence), and the re-export body is identical to the
// first export (excluding the volatile fields export_id·export_date).
// Only with SUPERLITE_REAL=1 + FLINT_TEST_DSN set.
func TestImportPAMRoundtripIdempotentRealSlow(t *testing.T) {
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
	const marker = "demo-pam-idem-slow"
	cleanup := func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE origin = $1`, marker)
	}
	cleanup()
	t.Cleanup(cleanup)

	// 1. Accrual — secure rows holding a stored content_hash (64 hex)
	// via the jsonl A9 path.
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	rows := []ExportRow{
		{ID: 910101, Topic: "demo pam idem alpha", Content: "합성 데모 pam 멱등 본문 alpha.",
			Kind: "knowledge", Status: "active", Confidence: 0.9, Tags: []string{"demo"},
			Domain: "main", Source: marker, ContentHash: "9090909090909090909090909090909090909090909090909090909090909090",
			CreatedAt: t0, UpdatedAt: t0},
		{ID: 910102, Topic: "demo pam idem beta", Content: "합성 데모 pam 멱등 본문 beta.",
			Kind: "knowledge", Status: "active", Confidence: 0.8, Tags: []string{"demo"},
			Domain: "main", Source: marker, ContentHash: "9191919191919191919191919191919191919191919191919191919191919191",
			CreatedAt: t0, UpdatedAt: t0},
	}
	seed, err := FormatJSONL(rows)
	if err != nil {
		t.Fatal(err)
	}
	var seedOut strings.Builder
	if code := RunImportMigrate(ctx, st, []string{"--format", "jsonl"}, strings.NewReader(string(seed)), &seedOut); code != 0 {
		t.Fatalf("적립 exit=%d; out=%s", code, seedOut.String())
	}

	// 2. export pam — a document emitting the stored hashes as
	// metadata. To isolate from residual rows of the shared real DB,
	// only the marker-origin rows are targeted.
	all, err := ExportRows(ctx, st, ExportFilter{})
	if err != nil {
		t.Fatalf("ExportRows: %v", err)
	}
	got := make([]ExportRow, 0, 2)
	for _, r := range all {
		if r.Source == marker {
			got = append(got, r)
		}
	}
	if len(got) != 2 {
		t.Fatalf("export marker rows = %d, want 2", len(got))
	}
	doc1, err := FormatPAM(got, "flint-test")
	if err != nil {
		t.Fatal(err)
	}

	// 3. import pam — all duplicates (idempotent): duplicated=2·
	// imported=0.
	var out strings.Builder
	if code := RunImportMigrate(ctx, st, []string{"--format", "pam"}, strings.NewReader(string(doc1)), &out); code != 0 {
		t.Fatalf("pam 수입 exit=%d; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"imported":0`) || !strings.Contains(out.String(), `"duplicated":2`) {
		t.Fatalf("왕복 멱등 미충족 — imported=0·duplicated=2 필요: %s", out.String())
	}

	// 4. Re-export identity — the body (excluding volatile fields) is
	// byte-identical to the first export.
	all2, err := ExportRows(ctx, st, ExportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got2 := make([]ExportRow, 0, len(got))
	for _, r := range all2 {
		if r.Source == marker {
			got2 = append(got2, r)
		}
	}
	doc2, err := FormatPAM(got2, "flint-test")
	if err != nil {
		t.Fatal(err)
	}
	if pamMaskedBody(t, doc1) != pamMaskedBody(t, doc2) {
		t.Errorf("재export 본문 불일치 — 수입이 원천을 변경함")
	}
}
