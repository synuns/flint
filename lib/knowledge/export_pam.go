// export_pam.go — v1.4.0 T1: FormatPAM — PAM(Portable AI Memory) v1.0
// 내보내기 포맷터(portable-ai-memory.org/spec/v1.0). 순수 함수:
// 원천 조회(ExportRows)·파일 기록과 무관하며 스토어 무접촉.
// v1.6.0 T4: ① relations §13.1 필수필드 완결({id, from, to, type,
// created_at} — id는 결정적 UUIDv5) ② §18 FormatPAMSigned — 루트
// owner{did:key:ed25519}·signature 블록(무서명 기본 경로 바이트 불변).
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
//     정렬은 id **문자열** 오름차순(코드포인트) — PAM id는 임의 문자열이므로
//     "flint:10" < "flint:9"(수치 아님). flint:N 규칙을 모르는 외부 검증자도
//     동일 규칙으로 체크섬을 재현할 수 있다(자릿수 경계 상호운용).
//     동일 rows면 export_id·export_date 제외 본문·체크섬 항상 동일
//     (입력 행 순서와 무관 — 정렬 후 산출).
package knowledge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"golang.org/x/text/unicode/norm"
)

// pamSpecURI — PAM v1.0 spec 식별자(루트 spec_uri).
const pamSpecURI = "https://portable-ai-memory.org/spec/v1.0"

// pamExportDoc — PAM v1.0 문서 루트(§4). 필드 순서 = 직렬화 순서.
// v1.6.0 T4: owner(§4 — 서명 경로만 발행)·signature(§18) 추가.
type pamExportDoc struct {
	Schema        string              `json:"schema"`
	SchemaVersion string              `json:"schema_version"`
	SpecURI       string              `json:"spec_uri"`
	ExportID      string              `json:"export_id"`
	ExportedBy    string              `json:"exported_by"`
	ExportDate    string              `json:"export_date"`
	Owner         *pamExportOwner     `json:"owner,omitempty"`
	Memories      []pamExportMemory   `json:"memories"`
	Relations     []pamExportRelation `json:"relations"`
	Integrity     pamExportIntegrity  `json:"integrity"`
	Signature     *pamExportSignature `json:"signature,omitempty"`
}

// pamExportOwner — §4 루트 owner 블록(최소). id는 서명 키 공개키의
// did:key:ed25519(§18 — owner.id = signature.signed_by).
type pamExportOwner struct {
	ID string `json:"id"`
}

// pamExportSignature — §18 문서 서명: payload{checksum, export_id,
// export_date, owner_id} JCS → Ed25519 → base64.
type pamExportSignature struct {
	Algorithm string `json:"algorithm"` // "ed25519"
	SignedBy  string `json:"signed_by"` // owner did:key
	Value     string `json:"value"`     // base64(64바이트 서명)
}

// pamExportMemory — PAM memory 1건(flint 행 매핑은 상단 주석).
// v1.6.1 T2 A2: Metadata — flint 저장 content_hash(64 hex) 왕복 멱등
// 확장(§10 additive). hex64 보유 행만 발행(무서명 골든 바이트 불변 —
// 미보유·비hex 행은 미발행).
type pamExportMemory struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	CustomType  string              `json:"custom_type"`
	Content     string              `json:"content"`
	ContentHash string              `json:"content_hash"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
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

// pamExportRelation — §13.1 relation 필수필드 전부: {id, from, to, type,
// created_at}. From=신규, To=구(§13.2 "Source replaces target"). id는
// (from,to,type)의 결정적 UUIDv5 — created_at은 신규(대체 원천) 행의
// 생성 시각.
type pamExportRelation struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
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
		return "", fmt.Errorf("pam export_id issue failed: %w", err)
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
	// A2(왕복 멱등): flint 저장 content_hash(64 hex)는 §10 metadata로
	// additive 발행해 수입 dedup가 원천 해시와 대칭하게 승계한다(jsonl
	// A9 isHex64 대칭). 비hex·미보유 행은 미발행 — 골든 바이트 불변.
	if isHex64(r.ContentHash) {
		m.Metadata = map[string]string{"content_hash": r.ContentHash}
	}
	if r.SupersededBy != nil {
		m.Temporal.SupersededBy = fmt.Sprintf("flint:%d", *r.SupersededBy)
	}
	return m
}

// --- v1.6.0 T5: JCS 스트리밍 체크섬 + 문서 스트리밍 인코딩 (R30-A) ---

// pamJCSHex — JCS \u00xx 이스케이프용 소문자 16진(jsoncanonicalizer
// decorateString의 fmt "\\u%04x" 규칙과 동일).
const pamJCSHex = "0123456789abcdef"

// jcsChunk — jcsBuf 스크래치 버퍼 상한(도달 시 해시로 flush — 정규화
// 전체 바이트를 메모리에 적재하지 않는다).
const jcsChunk = 8192

// jcsBuf — §15 체크섬 산출용 JCS(RFC 8785) 스트리밍 기록기.
// v1.6.0 T5: 종전 경로(json.Marshal → jsoncanonicalizer.Transform)는
// 전체 문서를 interface 트리로 재파싱했다(R30-A 실측 churn 52%·
// alloc 7.15M). pamExportMemory 타입 집합은 폐쇄적이므로 JCS 직렬화를
// 직접 기록한다 — 이스케이프는 jsoncanonicalizer와 바이트 단위 동일
// (`"` `\` \b \f \n \r \t 단축형, 그 외 제어문자 \u00xx 소문자, 나머지
// 원바이트 통과 — HTML·U+2028/9 무이스케이프, invalid UTF-8은
// json.Marshal과 동일하게 U+FFFD 치환). 숫자는 라이브러리의
// NumberToJSON을 그대로 호출해 규칙 재구현 drift를 원천 차단한다.
// 동일성 보증: TestPAMChecksumStreamEquivalence가 이스케이프·부동소수점
// 경계값 전부에서 라이브러리 재현 체크섬과 일치함을 검증한다.
type jcsBuf struct {
	h hash.Hash
	b []byte
}

func (w *jcsBuf) flush() {
	if len(w.b) > 0 {
		w.h.Write(w.b) // hash.Hash.Write은 오류 없음
		w.b = w.b[:0]
	}
}

// put — 원 문자열 청크 적재(버퍼 상한 도달 시 flush).
func (w *jcsBuf) put(s string) {
	if len(w.b)+len(s) > jcsChunk && len(w.b) > 0 {
		w.flush()
	}
	w.b = append(w.b, s...)
}

func (w *jcsBuf) putByte(c byte) {
	if len(w.b) >= jcsChunk {
		w.flush()
	}
	w.b = append(w.b, c)
}

// jcsString — JCS 문자열(따옴표 포함). 규칙은 타입 주석 참조.
func (w *jcsBuf) jcsString(s string) {
	w.putByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			w.put(`\"`)
		case c == '\\':
			w.put(`\\`)
		case c == '\b':
			w.put(`\b`)
		case c == '\f':
			w.put(`\f`)
		case c == '\n':
			w.put(`\n`)
		case c == '\r':
			w.put(`\r`)
		case c == '\t':
			w.put(`\t`)
		case c < 0x20:
			w.put(`\u00`)
			w.putByte(pamJCSHex[c>>4])
			w.putByte(pamJCSHex[c&0xf])
		case c < utf8.RuneSelf:
			w.putByte(c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				w.put("\ufffd") // json.Marshal 치환 규칙 승계
			} else {
				w.put(s[i : i+size])
				i += size - 1
			}
		}
	}
	w.putByte('"')
}

// num — JCS 숫자(jsoncanonicalizer.NumberToJSON 직접 재사용 — ES6
// 최단 표현·-0→0·지수형 정리 전부 라이브러리 규칙 그대로).
func (w *jcsBuf) num(f float64) error {
	s, err := jsoncanonicalizer.NumberToJSON(f)
	if err != nil {
		return fmt.Errorf("pam JCS number invalid: %w", err)
	}
	w.put(s)
	return nil
}

// memory — memory 1건의 JCS 객체. 키는 UTF-16 코드유닛 정렬순(전부
// ASCII라 바이트순과 동일): confidence, content, content_hash,
// custom_type, id, [metadata], provenance, status, tags, temporal, type.
// superseded_by는 omitempty(공백이면 미발행 — 종전과 동일). metadata도
// omitempty(A2 — hex64 저장 해시 보유 행만 발행).
func (w *jcsBuf) memory(m pamExportMemory) error {
	w.put(`{"confidence":{"current":`)
	if err := w.num(m.Confidence.Current); err != nil {
		return err
	}
	w.put(`},"content":`)
	w.jcsString(m.Content)
	w.put(`,"content_hash":`)
	w.jcsString(m.ContentHash)
	w.put(`,"custom_type":`)
	w.jcsString(m.CustomType)
	w.put(`,"id":`)
	w.jcsString(m.ID)
	// v1.6.1 T2 A2 — metadata(§10 확장, hex64 보유 행만): 키 정렬순상
	// id와 provenance 사이("metadata" < "provenance"). 키 집합은 폐쇄적
	// (content_hash 1키)이므로 고정 발행 — 맵 반복 비결정을 유발하지
	// 않는다.
	if m.Metadata != nil {
		w.put(`,"metadata":{"content_hash":`)
		w.jcsString(m.Metadata["content_hash"])
		w.putByte('}')
	}
	w.put(`,"provenance":{"platform":`)
	w.jcsString(m.Provenance.Platform)
	w.put(`},"status":`)
	w.jcsString(m.Status)
	w.put(`,"tags":[`)
	for i, t := range m.Tags {
		if i > 0 {
			w.putByte(',')
		}
		w.jcsString(t)
	}
	w.put(`],"temporal":{"created_at":`)
	w.jcsString(m.Temporal.CreatedAt)
	if m.Temporal.SupersededBy != "" {
		w.put(`,"superseded_by":`)
		w.jcsString(m.Temporal.SupersededBy)
	}
	w.put(`},"type":`)
	w.jcsString(m.Type)
	w.putByte('}')
	return nil
}

// pamChecksum — §15: memories(id 문자열 오름차순) → JCS 직렬화 →
// SHA-256. memories는 이미 id 문자열순으로 구성되어 있어 호출부 정렬
// 상태와 무결. v1.6.0 T5: 전체 재파싱 대신 jcsBuf로 스트리밍 — 체크섬
// 값은 종전과 동일(동일성 테스트가 보증).
func pamChecksum(memories []pamExportMemory) (string, error) {
	w := &jcsBuf{h: sha256.New(), b: make([]byte, 0, jcsChunk)}
	w.putByte('[')
	for i := range memories {
		if i > 0 {
			w.putByte(',')
		}
		if err := w.memory(memories[i]); err != nil {
			return "", err
		}
	}
	w.putByte(']')
	w.flush()
	sum := w.h.Sum(nil)
	return "sha256:" + hex.EncodeToString(sum), nil
}

// pamDocWriter — PAM 문서 스트리밍 인코더(v1.6.0 T5 ②). 종전
// json.Encoder(SetIndent)는 compact 전체 버퍼 + indent 전체 버퍼를
// 이중으로 적재했다(R30-A 인덴트 인코딩 버퍼). 여기선 사전 확보한 단일
// 출력 버퍼에 조각(스칼라·행 1건) 단위로 인코딩해 붙인다 — 조각은 재사용
// 소형 버퍼에서 json.Encoder(SetEscapeHTML(false))로 산출하므로 이스케이프
// 규칙이 종전과 완전히 동일하고, json.Indent(동일 파라미터)가 인덴트를
// 부여해 전체 출력은 종전 Encoder.Encode(doc)과 바이트 동일하다
// (TestFormatPAMRawBytesMatchEncoder가 전 문서 수준 바이트 동일성 검증).
type pamDocWriter struct {
	buf   bytes.Buffer
	small bytes.Buffer
	enc   *json.Encoder
}

func newPAMDocWriter(est int) *pamDocWriter {
	w := &pamDocWriter{}
	w.buf.Grow(est)
	w.enc = json.NewEncoder(&w.small)
	w.enc.SetEscapeHTML(false) // 종전 Encoder 세팅 승계(<>& 무이스케이프)
	return w
}

// piece — v 1건의 compact JSON 조각(Encoder가 붙이는 끝 개행 제거).
// 반환 슬라이스는 다음 piece 호출까지 유효(소형 버퍼 재사용).
func (w *pamDocWriter) piece(v any) ([]byte, error) {
	w.small.Reset()
	if err := w.enc.Encode(v); err != nil {
		return nil, err
	}
	return w.small.Bytes()[:w.small.Len()-1], nil
}

// scalar — 문자열 스칼라 값(Encoder 이스케이프 규칙 그대로 기록).
func (w *pamDocWriter) scalar(s string) error {
	p, err := w.piece(s)
	if err != nil {
		return err
	}
	w.buf.Write(p)
	return nil
}

// value — 중첩 값 1건(배열·객체)을 prefix/indent로 인덴트해 기록.
func (w *pamDocWriter) value(v any, prefix string) error {
	p, err := w.piece(v)
	if err != nil {
		return err
	}
	return json.Indent(&w.buf, p, prefix, "  ")
}

// doc — 루트 문서 조립. 필드 순서·인덴트·개행은 종전 Encoder 출력과
// 바이트 동일(무서명 기본 경로 바이트 불변 계약).
func (w *pamDocWriter) doc(doc *pamExportDoc) error {
	b := &w.buf
	b.WriteString("{\n")
	b.WriteString(`  "schema": `)
	if err := w.scalar(doc.Schema); err != nil {
		return err
	}
	b.WriteString(",\n  \"schema_version\": ")
	if err := w.scalar(doc.SchemaVersion); err != nil {
		return err
	}
	b.WriteString(",\n  \"spec_uri\": ")
	if err := w.scalar(doc.SpecURI); err != nil {
		return err
	}
	b.WriteString(",\n  \"export_id\": ")
	if err := w.scalar(doc.ExportID); err != nil {
		return err
	}
	b.WriteString(",\n  \"exported_by\": ")
	if err := w.scalar(doc.ExportedBy); err != nil {
		return err
	}
	b.WriteString(",\n  \"export_date\": ")
	if err := w.scalar(doc.ExportDate); err != nil {
		return err
	}
	if doc.Owner != nil {
		b.WriteString(",\n  \"owner\": ")
		if err := w.value(doc.Owner, "  "); err != nil {
			return err
		}
	}
	// memories — 대량 배열: 행 단위 조각 인코딩으로 중간 전체 버퍼 없이
	// 기록(요소는 depth 3 — prefix 4sp + indent 2sp).
	b.WriteString(",\n  \"memories\": ")
	if len(doc.Memories) == 0 {
		b.WriteString("[]")
	} else {
		b.WriteString("[\n")
		for i := range doc.Memories {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString("    ")
			p, err := w.piece(doc.Memories[i])
			if err != nil {
				return err
			}
			if err := json.Indent(b, p, "    ", "  "); err != nil {
				return err
			}
		}
		b.WriteString("\n  ]")
	}
	b.WriteString(",\n  \"relations\": ")
	if len(doc.Relations) == 0 {
		b.WriteString("[]")
	} else if err := w.value(doc.Relations, "  "); err != nil {
		return err
	}
	b.WriteString(",\n  \"integrity\": ")
	if err := w.value(doc.Integrity, "  "); err != nil {
		return err
	}
	if doc.Signature != nil {
		b.WriteString(",\n  \"signature\": ")
		if err := w.value(doc.Signature, "  "); err != nil {
			return err
		}
	}
	b.WriteString("\n}\n") // Encoder.Encode의 끝 개행 포함
	return nil
}

// pamDocEstimate — buf.Grow 사전 확보량 산정. 과소 예측은 성장 재할당만
// 낳을 뿐(결과 동일), 과대 예측보다 저렴하다 — 인덴트 오버헤드(행당
// 30여 줄)·이스케이프 확장 여유 반영.
func pamDocEstimate(memories []pamExportMemory, relations []pamExportRelation) int {
	est := 512
	for i := range memories {
		est += 420 + 2*len(memories[i].Content) + 32*len(memories[i].Tags)
	}
	return est + 320*len(relations)
}

// FormatPAM — rows를 PAM v1.0 문서로 직렬화한다(무서명 기본 경로 —
// owner·signature 블록 없음, 바이트 불변 계약). 결정적: 동일 rows면
// export_id(매 호출 UUIDv4)·export_date(현재 시각) 제외 본문과
// integrity.checksum은 항상 동일하다(입력 순서 무관 — id순 정렬 후 산출).
func FormatPAM(rows []ExportRow, exportedBy string) ([]byte, error) {
	data, _, err := FormatPAMWithCount(rows, exportedBy)
	return data, err
}

// FormatPAMWithCount — FormatPAM과 동일한 문서에 발행 수
// (integrity.total_memories — pending 제외)를 함께 반환한다.
// v1.6.0 T5 P6: cli가 발행 수를 읽으려고 직렬화한 문서 전체(대량
// export에서 수십 MB)를 재 Unmarshal하는 것을 제거하는 seam — 요약
// 행수의 단일 원천은 포맷터다.
func FormatPAMWithCount(rows []ExportRow, exportedBy string) ([]byte, int, error) {
	return formatPAMDoc(rows, exportedBy, nil)
}

// FormatPAMSigned — §18: FormatPAM 본문 + 루트 owner{did:key}·signature
// 블록. memories·relations·integrity는 무서명 경로와 동일(재직렬화 회피 —
// 문서 조립 1회, 서명은 조립 전 payload로 산출). key는 Ed25519 개인키
// (64바이트 — LoadOrCreatePAMSigningKey 산출물).
func FormatPAMSigned(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, error) {
	data, _, err := FormatPAMSignedWithCount(rows, exportedBy, key)
	return data, err
}

// FormatPAMSignedWithCount — FormatPAMSigned의 발행 수 반환 변형(P6 —
// FormatPAMWithCount 참조).
func FormatPAMSignedWithCount(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, int, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, 0, fmt.Errorf("pam signing key invalid (want %d bytes, got %d)",
			ed25519.PrivateKeySize, len(key))
	}
	return formatPAMDoc(rows, exportedBy, key)
}

// formatPAMDoc — 공용 조립 본체. key==nil이면 무서명(기본 경로 바이트
// 불변), 아니면 owner·signature 블록 추가. 반환 int는 발행 수.
func formatPAMDoc(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, int, error) {
	// §15 id 오름차순 = id 문자열(코드포인트) 오름차순. 수치 정렬이면
	// "flint:9" < "flint:10"이 되지만, 외부 PAM 검증자는 id를 문자열로만
	// 알므로 "flint:10" < "flint:9"로 정렬해 재현한다 — 자릿수 경계에서
	// 체크섬 상호검증이 깨진다(리뷰 v1.4.0 수정). v1.6.0 T5: 비교함수
	// 안의 Sprintf(n log n회 — alloc 다수점) 대신 id를 1회 선계산해
	// 순열 인덱스로 안정 정렬한다(호출부 슬라이스 미변경 승계).
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = "flint:" + strconv.Itoa(r.ID)
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return ids[order[a]] < ids[order[b]]
	})

	memories := make([]pamExportMemory, 0, len(rows))
	// emitted — memories에 실린 id 집합(v1.4.1 A1). relation은 양쪽
	// 엔드포인트(From·To)가 모두 이 집합 내인 경우만 발행한다. pending
	// superseder(미승인 대체 원천)·조회 범위 밖 superseder를 가리키는
	// relation은 허참조가 되어 수입 측 계보 복원(import_pam.go
	// SupersedePair)이 대상 행 부재로 스킵되는 계보 파괴를 일으키므로
	// 원천에서 발행하지 않는다.
	emitted := make(map[string]bool, len(rows))
	// emittedAt — §13.1 relation.created_at용: 발행 행의 생성 시각.
	emittedAt := make(map[string]time.Time, len(rows))
	for _, i := range order {
		r := rows[i]
		if r.Status == "pending" {
			// pending(미승인 내부 후보)은 PAM 문서에서 제외 — 폐쇄 분류에
			// 미승인 개념이 없고(archived는 '역사 보존'이라 의미 왜곡),
			// 승인되지 않은 후보를 표준 교환 문서에 싣지 않는다(룰링
			// v1.4.0 T1). relations·total_memories·체크섬 전부 미포함.
			continue
		}
		memories = append(memories, toPAMExportMemory(r))
		emitted[ids[i]] = true
		emittedAt[ids[i]] = r.CreatedAt
	}
	relations := make([]pamExportRelation, 0)
	for _, i := range order {
		r := rows[i]
		if r.Status == "pending" || r.SupersededBy == nil {
			continue
		}
		from := "flint:" + strconv.Itoa(*r.SupersededBy)
		to := ids[i]
		if !emitted[from] || !emitted[to] {
			continue // 한쪽이라도 문서에 없는 id — 허참조 relation 미발행
		}
		// §13.1 created_at — 대체가 성립한 시각 = 신규(대체 원천) 행의
		// 생성 시각. 원천 시각이 없는 손작성 행의 폴백은 구 행 시각.
		at := emittedAt[from]
		if at.IsZero() {
			at = emittedAt[to]
		}
		relations = append(relations, pamExportRelation{
			ID:        pamRelationID(from, to, "supersedes"),
			From:      from, // 신규(대체 원천)
			To:        to,   // 구(대체 대상)
			Type:      "supersedes",
			CreatedAt: at.UTC().Format(time.RFC3339),
		})
	}

	checksum, err := pamChecksum(memories)
	if err != nil {
		return nil, 0, err
	}
	exportID, err := pamUUIDv4()
	if err != nil {
		return nil, 0, err
	}
	exportDate := time.Now().UTC().Format(time.RFC3339)
	doc := pamExportDoc{
		Schema:        "portable-ai-memory",
		SchemaVersion: "1.0",
		SpecURI:       pamSpecURI,
		ExportID:      exportID,
		ExportedBy:    exportedBy,
		ExportDate:    exportDate,
		Memories:      memories,
		Relations:     relations,
		Integrity: pamExportIntegrity{
			Checksum:      checksum,
			TotalMemories: len(memories),
		},
	}
	if key != nil {
		// §18 — 서명은 문서 조립 전 payload로 산출(재직렬화 회피):
		// {checksum, export_id, export_date, owner_id} JCS → Ed25519.
		ownerID := PAMOwnerDID(key.Public().(ed25519.PublicKey))
		value, serr := PAMSignPayload(key, pamSignPayload{
			Checksum:   checksum,
			ExportID:   exportID,
			ExportDate: exportDate,
			OwnerID:    ownerID,
		})
		if serr != nil {
			return nil, 0, serr
		}
		doc.Owner = &pamExportOwner{ID: ownerID}
		doc.Signature = &pamExportSignature{
			Algorithm: "ed25519",
			SignedBy:  ownerID,
			Value:     value,
		}
	}
	w := newPAMDocWriter(pamDocEstimate(memories, relations))
	if err := w.doc(&doc); err != nil {
		return nil, 0, fmt.Errorf("pam document marshal failed: %w", err)
	}
	return w.buf.Bytes(), len(memories), nil
}
