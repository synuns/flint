// import_pam.go — v1.3.0 T10: PAM(Portable AI Memory) v1.0 읽기.
// 외부 PAM 내보내기 문서를 flint 임포트 후보로 변환하는 순수 파서 —
// 스토어 무접촉(계약 5): pending 적립 정책·실제 write는 T9
// (import_migrate.go) 소유. T9는 seam 변수 ParsePAMFn으로 연결한다.
// v1.6.0 T4: ① integrity.checksum(§15) 재계산 검증 — 불일치는 오류
// (호출부 exit 3·조용 통과 금지) ② signature(§18) 있으면 Ed25519
// 검증 — 실패는 stderr 경고 1행 후 계속(§21.4 SHOULD).
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// ImportCandidate — PAM memory 1건의 flint 변환 결과. ExportRow 호환
// 필드명(왕복 jsonl export·import 대칭)이며 pending 적립 대상
// (status active)만 들어간다.
type ImportCandidate struct {
	ID         string         `json:"id"` // 원 PAM memory id(계보 갱신 연결용)
	Kind       string         `json:"kind"`
	Content    string         `json:"content"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source"` // "import:pam:"+platform
	CreatedAt  string         `json:"created_at,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PAMSkipped — 적립 대상이 아닌 status의 memory(보고용 — T9 출력).
type PAMSkipped struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"` // superseded/deprecated/retracted/archived
}

// SupersedePair — relations 중 type=="supersedes"의 쌍. PAM §13.2
// "supersedes: Source replaces target"에 따라 방향을 확정한다:
// From=신규(대체하는 원천 source), To=구(대체되는 대상 target).
// 즉 "From이 To를 대체" — 소비측(importPAM 계보 복원)은 이 의미만
// 정합 소구해야 한다: Supersede(oldID=To의 적립 id, newID=From의 적립 id).
type SupersedePair struct {
	From string `json:"from"` // 신규 — 대체하는 memory(source)
	To   string `json:"to"`   // 구 — 대체되는 memory(target)
}

// PAMResult — ParsePAM 반환 묶음.
type PAMResult struct {
	Candidates     []ImportCandidate
	Skipped        []PAMSkipped
	SupersedePairs []SupersedePair
}

// ParsePAMFn — T9 소비 seam(테스트 치환 가능).
var ParsePAMFn = ParsePAM

// PAM v1.0 문서 구조(필요 최소 필드만 — summary는 무시: content 우선).
type pamDoc struct {
	Schema      string           `json:"schema"`
	ExportID    string           `json:"export_id"`
	ExportDate  string           `json:"export_date"`
	Owner       *pamOwnerRef     `json:"owner"`
	Memories    []pamMemory      `json:"memories"`
	Relations   []pamRelation    `json:"relations"`
	Integrity   *pamIntegrityRef `json:"integrity"`
	Signature   *pamSignatureRef `json:"signature"`
}

// pamOwnerRef — 루트 owner(§4·§18 서명 검증용 최소).
type pamOwnerRef struct {
	ID string `json:"id"`
}

// pamIntegrityRef — §15 체크섬(검증 대상).
type pamIntegrityRef struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

// pamSignatureRef — §18 문서 서명.
type pamSignatureRef struct {
	Algorithm string `json:"algorithm"`
	SignedBy  string `json:"signed_by"`
	Value     string `json:"value"`
}

// pamImportStderr — §21.4 경고 챈널 seam(기본 os.Stderr — 테스트 캡처).
var pamImportStderr io.Writer = os.Stderr

// verifyPAMChecksum — §15 재계산: 원문 memories(원바이트 보존 — 구조체
// 왕복이 미정의 필드를 버리면 체크섬이 재현되지 않는다)를 id 문자열
// 오름차순 정렬 → JCS → SHA-256. checksum 미기재 문서(§15 이전 관례)는
// 검증 없이 통과(nil 반환).
func verifyPAMChecksum(data []byte, want string) error {
	if want == "" {
		return nil
	}
	var raw struct {
		Memories []json.RawMessage `json:"memories"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("pam memories raw parse failed: %w", err)
	}
	type idOnly struct {
		ID string `json:"id"`
	}
	type memEntry struct {
		id  string
		raw json.RawMessage
	}
	entries := make([]memEntry, 0, len(raw.Memories))
	for i, m := range raw.Memories {
		var v idOnly
		if err := json.Unmarshal(m, &v); err != nil || v.ID == "" {
			return fmt.Errorf("pam memories[%d] id unreadable", i)
		}
		entries = append(entries, memEntry{id: v.ID, raw: m})
	}
	// §15 — id 문자열(코드포인트) 오름차순(export_pam.go 정렬 계약과
	// 동일). 안정 정렬: 동일 id 중 원문 순서 유지.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	sorted := make([]json.RawMessage, len(entries))
	for i, e := range entries {
		sorted[i] = e.raw
	}
	memBytes, err := json.Marshal(sorted)
	if err != nil {
		return fmt.Errorf("pam memories marshal failed: %w", err)
	}
	canonical, err := jsoncanonicalizer.Transform(memBytes)
	if err != nil {
		return fmt.Errorf("pam JCS normalization failed: %w", err)
	}
	sum := sha256.Sum256(canonical)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("pam integrity checksum mismatch: document=%s recomputed=%s — refusing import", want, got)
	}
	return nil
}

// verifyPAMSignature — §18: signature 있으면 payload{checksum, export_id,
// export_date, owner_id} JCS → Ed25519 검증. 실패·키 불해석은 경고 1행
// (§21.4 SHOULD — 경고 후 계속, 거부 아님). 무서명 문서는 무경고 통과.
func verifyPAMSignature(doc *pamDoc) {
	if doc.Signature == nil || doc.Signature.Value == "" {
		return
	}
	did := doc.Signature.SignedBy
	if did == "" && doc.Owner != nil {
		did = doc.Owner.ID
	}
	checksum := ""
	if doc.Integrity != nil {
		checksum = doc.Integrity.Checksum
	}
	ownerID := ""
	if doc.Owner != nil {
		ownerID = doc.Owner.ID
	}
	fail := func(reason string) {
		fmt.Fprintf(pamImportStderr,
			"[flint] import pam: signature verification failed (%s, signed_by=%q) — continuing (spec §21.4)\n",
			reason, did)
	}
	if did == "" {
		fail("signing key absent")
		return
	}
	pub, err := PAMDIDKeyPubkey(did)
	if err != nil {
		fail("signing key unreadable: " + err.Error())
		return
	}
	if !PAMVerifySignature(pub, doc.Signature.Value, pamSignPayload{
		Checksum:   checksum,
		ExportID:   doc.ExportID,
		ExportDate: doc.ExportDate,
		OwnerID:    ownerID,
	}) {
		fail("payload signature invalid — document may be tampered")
	}
}

type pamMemory struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Content    string        `json:"content"`
	Temporal   pamTemporal   `json:"temporal"`
	Status     string        `json:"status"`
	Confidence pamConfidence `json:"confidence"`
	Tags       []string      `json:"tags"`
	Provenance pamProvenance `json:"provenance"`
}

type pamTemporal struct {
	CreatedAt string `json:"created_at"`
}

// Current 포인터 — 생략(0 역직렬화)과 명시 0을 구분해 기본값 0.5 적용.
type pamConfidence struct {
	Current *float64 `json:"current"`
}

type pamProvenance struct {
	Platform string `json:"platform"`
}

type pamRelation struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// pamKindByType — PAM type → flint kind. 표준 knowledge형 9종 + project.
// custom(및 알 수 없는 type)는 여기 없다 — knowledge + Metadata["custom_type"]
// 원문 보존 경로로 떨어진다.
var pamKindByType = map[string]string{
	"fact":         "knowledge",
	"preference":   "knowledge",
	"skill":        "knowledge",
	"context":      "knowledge",
	"goal":         "knowledge",
	"instruction":  "knowledge",
	"environment":  "knowledge",
	"identity":     "knowledge",
	"relationship": "knowledge",
	"project":      "project",
}

// ParsePAM — PAM v1.0 문서(data)를 후보·스킵·supersedes 쌍으로 변환.
// 루트 schema 불일치·파손 json은 오류(파손은 offset 포함 상세).
func ParsePAM(data []byte) (*PAMResult, error) {
	var doc pamDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, fmt.Errorf("pam json corrupt (offset %d): %w", syn.Offset, err)
		}
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) {
			return nil, fmt.Errorf("pam json field type error (%s.%s offset %d): %w",
				typ.Struct, typ.Field, typ.Offset, err)
		}
		return nil, fmt.Errorf("pam json parse failed: %w", err)
	}
	if doc.Schema != "portable-ai-memory" {
		return nil, fmt.Errorf("pam schema mismatch: %q (want %q)", doc.Schema, "portable-ai-memory")
	}
	// §15 — integrity.checksum 재계산 검증(불일치 = 문서 변조 → 오류,
	// 호출부 exit 3). 조용한 통과 금지(v1.6.0 T4).
	if doc.Integrity != nil {
		if err := verifyPAMChecksum(data, doc.Integrity.Checksum); err != nil {
			return nil, err
		}
	}
	// §21.4 — 서명은 SHOULD 검증: 실패해도 경고 후 계속(위 함수).
	verifyPAMSignature(&doc)
	res := &PAMResult{}
	for _, m := range doc.Memories {
		if m.Status != "active" {
			// superseded/deprecated/retracted/archived — pending 적립
			// 대상 아님. 계약 명시 5종 외 알 수 없는 status도 조용한
			// 적립 대신 눈에 보이는 스킵 보고로 남긴다(원문 status 보존).
			res.Skipped = append(res.Skipped, PAMSkipped{ID: m.ID, Type: m.Type, Status: m.Status})
			continue
		}
		c := ImportCandidate{
			ID:         m.ID,
			Kind:       "knowledge",
			Content:    m.Content, // content 우선 — summary는 무시(계약 2)
			Tags:       m.Tags,
			Confidence: 0.5, // 생략 시 import 경로 기본(import.go 관례)
			Source:     "import:pam:" + m.Provenance.Platform,
			CreatedAt:  m.Temporal.CreatedAt,
		}
		if m.Confidence.Current != nil {
			c.Confidence = clampUnit(*m.Confidence.Current)
		}
		if k, ok := pamKindByType[m.Type]; ok {
			c.Kind = k
		} else {
			// custom — 원 custom_type 보존
			if c.Metadata == nil {
				c.Metadata = map[string]any{}
			}
			c.Metadata["custom_type"] = m.Type
		}
		res.Candidates = append(res.Candidates, c)
	}
	for _, r := range doc.Relations {
		if r.Type == "supersedes" {
			// §13.2 "Source replaces target": from=신규, to=구 — 원시
			// relation 방향을 그대로 확정(SupersedePair 주석 참조).
			res.SupersedePairs = append(res.SupersedePairs, SupersedePair{From: r.From, To: r.To})
		}
	}
	return res, nil
}

// clampUnit — [0,1] 클램프(confidence.current).
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
