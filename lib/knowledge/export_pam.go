// export_pam.go — v1.4.0 T1: FormatPAM — PAM(Portable AI Memory) v1.0
// 내보내기 포맷터(portable-ai-memory.org/spec/v1.0). 순수 함수:
// 원천 조회(ExportRows)·파일 기록과 무관하며 스토어 무접촉.
//
// 매핑 계약:
//   - memory id "flint:<N>"(N=row.ID), type "custom" + custom_type=<Kind>
//     (flint kind는 PAM 표준형이 아니므로 custom 확장 슬롯에 실는다).
//   - content_hash는 PAM §6 정규화(trim→lower→NFC→연속 공백 collapse→
//     SHA-256)로 **별도 산출** — flint 저장 해시(row.ContentHash)와
//     알고리즘이 다르므로 재사용하지 않는다.
//   - status: active→"active", invalidated→"superseded"(SupersededBy≠nil
//     이면 temporal.superseded_by="flint:<N>"). pending은 행 자체를
//     제외(미승인 내부 후보 — 문서 유출 방지), 그 외 미래 status는
//     PAM의 비활동 계열 "archived"로.
//   - relations §13.2 "Source replaces target": from=신규(row.SupersededBy),
//     to=구(행 자신) — SupersedePair 방향 계약(import_pam.go)과 동일.
//   - integrity §15: memories를 id 오름차순 정렬 → JCS 직렬화 → SHA-256.
//     동일 rows면 export_id·export_date 제외 본문·체크섬 항상 동일
//     (입력 행 순서와 무관 — 정렬 후 산출).
package knowledge

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"golang.org/x/text/unicode/norm"
)

// pamSpecURI — PAM v1.0 spec 식별자(루트 spec_uri).
const pamSpecURI = "https://portable-ai-memory.org/spec/v1.0"

// pamExportDoc — PAM v1.0 문서 루트(§4). 필드 순서 = 직렬화 순서.
type pamExportDoc struct {
	Schema        string              `json:"schema"`
	SchemaVersion string              `json:"schema_version"`
	SpecURI       string              `json:"spec_uri"`
	ExportID      string              `json:"export_id"`
	ExportedBy    string              `json:"exported_by"`
	ExportDate    string              `json:"export_date"`
	Memories      []pamExportMemory   `json:"memories"`
	Relations     []pamExportRelation `json:"relations"`
	Integrity     pamExportIntegrity  `json:"integrity"`
}

// pamExportMemory — PAM memory 1건(flint 행 매핑은 상단 주석).
type pamExportMemory struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	CustomType  string              `json:"custom_type"`
	Content     string              `json:"content"`
	ContentHash string              `json:"content_hash"`
	Temporal    pamExportTemporal   `json:"temporal"`
	Status      string              `json:"status"`
	Tags        []string            `json:"tags"`
	Confidence  pamExportConfidence `json:"confidence"`
	Provenance  pamExportProvenance `json:"provenance"`
}

// pamExportTemporal — 생성/대체 시각 정보. SupersededBy는 superseded
// status에서만 존재(omitempty).
type pamExportTemporal struct {
	CreatedAt    string `json:"created_at"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

// pamExportConfidence — §4 confidence{current}.
type pamExportConfidence struct {
	Current float64 `json:"current"`
}

// pamExportProvenance — 출처 플랫폼 식별.
type pamExportProvenance struct {
	Platform string `json:"platform"`
}

// pamExportRelation — §13.2 relation. From=신규, To=구.
type pamExportRelation struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// pamExportIntegrity — §15 체크섬 + 총량.
type pamExportIntegrity struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

// pamWhitespaceRe — §6 연속 공백 collapse용(유니코드 공백 계열 포함).
var pamWhitespaceRe = regexp.MustCompile(`\s+`)

// PAMContentHash — §6 정규화 콘텐츠 해시: TrimSpace → ToLower → NFC →
// 연속 공백 단일 공백 collapse → SHA-256. flint 저장 해시와 별개 산출.
func PAMContentHash(content string) string {
	s := strings.TrimSpace(strings.ToLower(content))
	s = string(norm.NFC.String(s))
	s = pamWhitespaceRe.ReplaceAllString(s, " ")
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pamUUIDv4 — crypto/rand 기반 UUIDv4(버전·variant 비트 설정).
func pamUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pam export_id 발급 실패: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// pamStatus — flint status → PAM status(상단 주석 매핑표). pending은
// FormatPAM에서 행 자체를 제외하므로 여기 도달하지 않는다.
func pamStatus(status string) string {
	switch status {
	case "active":
		return "active"
	case "invalidated":
		return "superseded"
	default: // 미래 신규 status 등 — PAM 비활동 계열로 안전 낙착
		return "archived"
	}
}

// toPAMExportMemory — ExportRow 1행 → PAM memory.
func toPAMExportMemory(r ExportRow) pamExportMemory {
	m := pamExportMemory{
		ID:          fmt.Sprintf("flint:%d", r.ID),
		Type:        "custom",
		CustomType:  r.Kind,
		Content:     r.Content,
		ContentHash: PAMContentHash(r.Content), // §6 별도 산출(저장 해시 비사용)
		Temporal: pamExportTemporal{
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Status:     pamStatus(r.Status),
		Tags:       r.Tags,
		Confidence: pamExportConfidence{Current: r.Confidence},
		Provenance: pamExportProvenance{Platform: "flint"},
	}
	if r.Tags == nil {
		m.Tags = []string{} // PAM 배열 필드 — null 대신 빈 배열
	}
	if r.SupersededBy != nil {
		m.Temporal.SupersededBy = fmt.Sprintf("flint:%d", *r.SupersededBy)
	}
	return m
}

// pamChecksum — §15: memories(id 오름차순) → JCS 직렬화 → SHA-256.
// memories는 이미 id순으로 구성되어 있어 호출부 정렬 상태와 무결.
func pamChecksum(memories []pamExportMemory) (string, error) {
	memBytes, err := json.Marshal(memories)
	if err != nil {
		return "", fmt.Errorf("pam memories 직렬화 실패: %w", err)
	}
	canonical, err := jsoncanonicalizer.Transform(memBytes)
	if err != nil {
		return "", fmt.Errorf("pam JCS 정규화 실패: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// FormatPAM — rows를 PAM v1.0 문서로 직렬화한다. 결정적: 동일 rows면
// export_id(매 호출 UUIDv4)·export_date(현재 시각) 제외 본문과
// integrity.checksum은 항상 동일하다(입력 순서 무관 — id순 정렬 후 산출).
func FormatPAM(rows []ExportRow, exportedBy string) ([]byte, error) {
	sorted := make([]ExportRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	memories := make([]pamExportMemory, 0, len(sorted))
	relations := make([]pamExportRelation, 0)
	for _, r := range sorted {
		if r.Status == "pending" {
			// pending(미승인 내부 후보)은 PAM 문서에서 제외 — 폐쇄 분류에
			// 미승인 개념이 없고(archived는 '역사 보존'이라 의미 왜곡),
			// 승인되지 않은 후보를 표준 교환 문서에 싣지 않는다(룰링
			// v1.4.0 T1). relations·total_memories·체크섬 전부 미포함.
			continue
		}
		memories = append(memories, toPAMExportMemory(r))
		if r.SupersededBy != nil {
			relations = append(relations, pamExportRelation{
				Type: "supersedes",
				From: fmt.Sprintf("flint:%d", *r.SupersededBy), // 신규(대체 원천)
				To:   fmt.Sprintf("flint:%d", r.ID),            // 구(대체 대상)
			})
		}
	}

	checksum, err := pamChecksum(memories)
	if err != nil {
		return nil, err
	}
	exportID, err := pamUUIDv4()
	if err != nil {
		return nil, err
	}
	doc := pamExportDoc{
		Schema:        "portable-ai-memory",
		SchemaVersion: "1.0",
		SpecURI:       pamSpecURI,
		ExportID:      exportID,
		ExportedBy:    exportedBy,
		ExportDate:    time.Now().UTC().Format(time.RFC3339),
		Memories:      memories,
		Relations:     relations,
		Integrity: pamExportIntegrity{
			Checksum:      checksum,
			TotalMemories: len(memories),
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 문서 가독성 — <>& 이스케이프 불필요(PAM 소비자는 json 표준)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("pam 문서 직렬화 실패: %w", err)
	}
	return buf.Bytes(), nil
}
