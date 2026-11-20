// export_pam.go — FormatPAM — the PAM (Portable AI Memory) v1.0 export
// formatter (portable-ai-memory.org/spec/v1.0). Pure function:
// independent of source queries (ExportRows)·file writing, no store
// contact. ① relations with the complete §13.1 required fields ({id,
// from, to, type, created_at} — id is a deterministic UUIDv5) ② §18
// FormatPAMSigned — the root owner{did:key:ed25519}·signature block (the
// unsigned default path stays byte-invariant).
//
// Mapping contract:
//   - memory id "flint:<N>" (N=row.ID), type "custom" + custom_type=<Kind>
//     (flint kinds are not PAM standard shapes, so they ride in the custom
//     extension slot).
//   - content_hash is **separately derived** via PAM §6 normalization
//     (trim→lower→NFC→whitespace-run collapse→SHA-256) — not reused,
//     because the algorithm differs from the flint stored hash
//     (row.ContentHash).
//   - status: active→"active", invalidated→"superseded" (when
//     SupersededBy≠nil, temporal.superseded_by="flint:<N>"). pending rows
//     are excluded entirely (unapproved internal candidates — document
//     leak prevention); other future statuses map to PAM's inactive family
//     "archived".
//   - relations §13.2 "Source replaces target": from=new
//     (row.SupersededBy), to=old (the row itself) — the same direction
//     contract as SupersedePair (import_pam.go).
//   - integrity §15: memories sorted by ascending id → JCS serialization
//     → SHA-256. The sort is ascending id **string** (code point) order —
//     PAM ids are arbitrary strings, so "flint:10" < "flint:9" (not
//     numeric). External verifiers who do not know the flint:N rule can
//     still reproduce the checksum under the same rule (digit-boundary
//     interoperability). The same rows always yield an identical body·
//     checksum excluding export_id·export_date (input row order
//     irrelevant — derived after sorting).
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

// pamSpecURI — the PAM v1.0 spec identifier (the root spec_uri).
const pamSpecURI = "https://portable-ai-memory.org/spec/v1.0"

// pamExportDoc — the PAM v1.0 document root (§4). Field order =
// serialization order. Adds owner (§4 — emitted only on the signing path)·
// signature (§18).
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

// pamExportOwner — the §4 root owner block (minimal). id is the signing
// key's public key as did:key:ed25519 (§18 — owner.id = signature.signed_by).
type pamExportOwner struct {
	ID string `json:"id"`
}

// pamExportSignature — the §18 document signature: payload{checksum,
// export_id, export_date, owner_id} JCS → Ed25519 → base64.
type pamExportSignature struct {
	Algorithm string `json:"algorithm"` // "ed25519"
	SignedBy  string `json:"signed_by"` // owner did:key
	Value     string `json:"value"`     // base64(64-byte signature)
}

// pamExportMemory — one PAM memory (flint row mapping in the comment
// above). Metadata — a round-trip idempotence extension carrying the flint
// stored content_hash (64 hex) (§10 additive). Emitted only for rows
// holding a hex64 (unsigned golden bytes unchanged — rows without one·
// non-hex are not emitted).
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

// pamExportTemporal — creation/supersession time information.
// SupersededBy exists only with superseded status (omitempty).
type pamExportTemporal struct {
	CreatedAt    string `json:"created_at"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

// pamExportConfidence — §4 confidence{current}.
type pamExportConfidence struct {
	Current float64 `json:"current"`
}

// pamExportProvenance — the source platform identifier.
type pamExportProvenance struct {
	Platform string `json:"platform"`
}

// pamExportRelation — all the §13.1 relation required fields: {id, from,
// to, type, created_at}. From=new, To=old (§13.2 "Source replaces
// target"). id is a deterministic UUIDv5 of (from,to,type) — created_at is
// the creation time of the new (replacing source) row.
type pamExportRelation struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
}

// pamExportIntegrity — the §15 checksum + totals.
type pamExportIntegrity struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

// pamWhitespaceRe — for the §6 whitespace-run collapse (covering the
// Unicode whitespace family).
var pamWhitespaceRe = regexp.MustCompile(`\s+`)

// PAMContentHash — the §6 normalized content hash: TrimSpace → ToLower →
// NFC → whitespace-run collapse to a single space → SHA-256. Derived
// separately from the flint stored hash.
func PAMContentHash(content string) string {
	s := strings.TrimSpace(strings.ToLower(content))
	s = string(norm.NFC.String(s))
	s = pamWhitespaceRe.ReplaceAllString(s, " ")
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pamUUIDv4 — a crypto/rand-based UUIDv4 (version·variant bits set).
func pamUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pam export_id issue failed: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// pamStatus — flint status → PAM status (the mapping table in the comment
// above). pending never reaches here because FormatPAM excludes the row
// itself.
func pamStatus(status string) string {
	switch status {
	case "active":
		return "active"
	case "invalidated":
		return "superseded"
	default: // future new statuses etc. — safely land in PAM's inactive family
		return "archived"
	}
}

// toPAMExportMemory — one ExportRow → a PAM memory.
func toPAMExportMemory(r ExportRow) pamExportMemory {
	m := pamExportMemory{
		ID:          fmt.Sprintf("flint:%d", r.ID),
		Type:        "custom",
		CustomType:  r.Kind,
		Content:     r.Content,
		ContentHash: PAMContentHash(r.Content), // §6 separately derived (stored hash unused)
		Temporal: pamExportTemporal{
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Status:     pamStatus(r.Status),
		Tags:       r.Tags,
		Confidence: pamExportConfidence{Current: r.Confidence},
		Provenance: pamExportProvenance{Platform: "flint"},
	}
	if r.Tags == nil {
		m.Tags = []string{} // PAM array field — an empty array instead of null
	}
	// A2 (round-trip idempotence): the flint stored content_hash (64 hex)
	// is additively emitted as §10 metadata so import dedup inherits it
	// symmetrically with the source hash (jsonl A9 isHex64 symmetry).
	// Non-hex·absent rows are not emitted — golden bytes unchanged.
	if isHex64(r.ContentHash) {
		m.Metadata = map[string]string{"content_hash": r.ContentHash}
	}
	if r.SupersededBy != nil {
		m.Temporal.SupersededBy = fmt.Sprintf("flint:%d", *r.SupersededBy)
	}
	return m
}

// --- JCS streaming checksum + document streaming encoding ---

// pamJCSHex — lowercase hex for JCS \u00xx escapes (identical to the
// jsoncanonicalizer decorateString rule of fmt "\\u%04x").
const pamJCSHex = "0123456789abcdef"

// jcsChunk — the jcsBuf scratch buffer cap (flush to the hash on reach —
// never loads the full normalized bytes into memory).
const jcsChunk = 8192

// jcsBuf — the streaming JCS (RFC 8785) writer for §15 checksum
// derivation. The former path (json.Marshal →
// jsoncanonicalizer.Transform) re-parsed the entire document into an
// interface tree (measured churn 52%·alloc 7.15M). The pamExportMemory
// type set is closed, so JCS serialization is written directly — escapes
// are byte-identical to jsoncanonicalizer (`"` `\` \b \f \n \r \t
// short forms, other control characters as \u00xx lowercase, everything
// else passed through as raw bytes — HTML·U+2028/9 unescaped, invalid
// UTF-8 replaced with U+FFFD exactly as json.Marshal does). Numbers call
// the library's NumberToJSON as-is, fundamentally blocking rule
// reimplementation drift. Equivalence guarantee: TestPAMChecksumStream-
// Equivalence verifies escape·floating-point boundary values all match
// the library-reproduced checksum.
type jcsBuf struct {
	h hash.Hash
	b []byte
}

func (w *jcsBuf) flush() {
	if len(w.b) > 0 {
		w.h.Write(w.b) // hash.Hash.Write never errors
		w.b = w.b[:0]
	}
}

// put — appends a raw string chunk (flush on reaching the buffer cap).
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

// jcsString — a JCS string (quotes included). See the type comment for
// the rules.
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
				w.put("\ufffd") // json.Marshal replacement rule inherited
			} else {
				w.put(s[i : i+size])
				i += size - 1
			}
		}
	}
	w.putByte('"')
}

// num — a JCS number (direct reuse of jsoncanonicalizer.NumberToJSON —
// ES6 shortest representation·-0→0·exponent normalization all exactly the
// library's rules).
func (w *jcsBuf) num(f float64) error {
	s, err := jsoncanonicalizer.NumberToJSON(f)
	if err != nil {
		return fmt.Errorf("pam JCS number invalid: %w", err)
	}
	w.put(s)
	return nil
}

// memory — the JCS object of one memory. Keys in UTF-16 code-unit sort
// order (all ASCII, so identical to byte order): confidence, content,
// content_hash, custom_type, id, [metadata], provenance, status, tags,
// temporal, type. superseded_by is omitempty (not emitted when blank — as
// before). metadata is also omitempty (A2 — emitted only for rows holding
// a hex64 stored hash).
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
	// metadata (§10 extension, only rows holding a hex64): in key sort
	// order it sits between id and provenance ("metadata" < "provenance").
	// The key set is closed (the single content_hash key), so emission is
	// fixed — never triggering map-iteration nondeterminism.
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

// pamChecksum — §15: memories (ascending id string) → JCS serialization →
// SHA-256. memories are already composed in id string order, so it is
// independent of caller sort state. Streams via jcsBuf instead of a full
// re-parse — the checksum value is identical to before (the equivalence
// test guarantees it).
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

// pamDocWriter — the streaming PAM document encoder. The former
// json.Encoder (SetIndent) double-loaded the compact full buffer + the
// indented full buffer (the indent-encoding buffer). Here, pieces
// (scalars·single rows) are encoded and appended into a single
// pre-allocated output buffer — pieces are produced by json.Encoder
// (SetEscapeHTML(false)) in a reused small buffer, so the escape rules
// are exactly as before, and json.Indent (the same parameters) applies
// the indentation, making the whole output byte-identical to the former
// Encoder.Encode(doc) (TestFormatPAMRawBytesMatchEncoder verifies
// whole-document byte equivalence).
type pamDocWriter struct {
	buf   bytes.Buffer
	small bytes.Buffer
	enc   *json.Encoder
}

func newPAMDocWriter(est int) *pamDocWriter {
	w := &pamDocWriter{}
	w.buf.Grow(est)
	w.enc = json.NewEncoder(&w.small)
	w.enc.SetEscapeHTML(false) // former Encoder setting inherited (<>& unescaped)
	return w
}

// piece — one compact JSON piece of v (the trailing newline the Encoder
// appends is stripped). The returned slice is valid until the next piece
// call (small-buffer reuse).
func (w *pamDocWriter) piece(v any) ([]byte, error) {
	w.small.Reset()
	if err := w.enc.Encode(v); err != nil {
		return nil, err
	}
	return w.small.Bytes()[:w.small.Len()-1], nil
}

// scalar — a string scalar value (recorded with the Encoder's escape
// rules as-is).
func (w *pamDocWriter) scalar(s string) error {
	p, err := w.piece(s)
	if err != nil {
		return err
	}
	w.buf.Write(p)
	return nil
}

// value — records one nested value (array·object) indented with
// prefix/indent.
func (w *pamDocWriter) value(v any, prefix string) error {
	p, err := w.piece(v)
	if err != nil {
		return err
	}
	return json.Indent(&w.buf, p, prefix, "  ")
}

// doc — assembles the root document. Field order·indentation·newlines
// are byte-identical to the former Encoder output (the unsigned default
// path keeps the byte-invariant contract).
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
	// memories — the large array: recorded row-by-row in encoded pieces
	// without an intermediate full buffer (elements are at depth 3 —
	// prefix 4sp + indent 2sp).
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
	b.WriteString("\n}\n") // includes Encoder.Encode's trailing newline
	return nil
}

// pamDocEstimate — computes the buf.Grow pre-allocation. An
// under-estimate only costs a growth reallocation (same result) and is
// cheaper than an over-estimate — reflects indentation overhead (~30
// bytes per row)·escape expansion headroom.
func pamDocEstimate(memories []pamExportMemory, relations []pamExportRelation) int {
	est := 512
	for i := range memories {
		est += 420 + 2*len(memories[i].Content) + 32*len(memories[i].Tags)
	}
	return est + 320*len(relations)
}

// FormatPAM — serializes rows into a PAM v1.0 document (the unsigned
// default path — no owner·signature blocks, byte-invariant contract).
// Deterministic: the same rows always yield an identical body and
// integrity.checksum excluding export_id (a per-call UUIDv4)·export_date
// (the current time) (input order irrelevant — derived after id-order
// sorting).
func FormatPAM(rows []ExportRow, exportedBy string) ([]byte, error) {
	data, _, err := FormatPAMWithCount(rows, exportedBy)
	return data, err
}

// FormatPAMWithCount — returns the emitted count
// (integrity.total_memories — pending excluded) alongside the same
// document as FormatPAM. v1.6.0 T5 P6: the seam removing the cli's
// re-Unmarshal of the entire serialized document (tens of MB on large
// exports) just to read the emitted count — the formatter is the single
// source of the summary row count.
func FormatPAMWithCount(rows []ExportRow, exportedBy string) ([]byte, int, error) {
	return formatPAMDoc(rows, exportedBy, nil)
}

// FormatPAMSigned — §18: the FormatPAM body + the root owner{did:key}·
// signature blocks. memories·relations·integrity are identical to the
// unsigned path (avoiding re-serialization — one document assembly; the
// signature is derived from the pre-assembly payload). key is an Ed25519
// private key (64 bytes — the product of LoadOrCreatePAMSigningKey).
func FormatPAMSigned(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, error) {
	data, _, err := FormatPAMSignedWithCount(rows, exportedBy, key)
	return data, err
}

// FormatPAMSignedWithCount — the emitted-count variant of FormatPAMSigned
// (P6 — see FormatPAMWithCount).
func FormatPAMSignedWithCount(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, int, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, 0, fmt.Errorf("pam signing key invalid (want %d bytes, got %d)",
			ed25519.PrivateKeySize, len(key))
	}
	return formatPAMDoc(rows, exportedBy, key)
}

// formatPAMDoc — the shared assembly body. key==nil means unsigned (the
// default path stays byte-invariant); otherwise the owner·signature
// blocks are added. The returned int is the emitted count.
func formatPAMDoc(rows []ExportRow, exportedBy string, key ed25519.PrivateKey) ([]byte, int, error) {
	// §15 ascending id = ascending id string (code point). A numeric sort
	// would give "flint:9" < "flint:10", but external PAM verifiers know
	// ids only as strings, so they reproduce it sorted "flint:10" <
	// "flint:9" — breaking checksum cross-validation at digit boundaries.
	// Instead of Sprintf inside the comparison function (n log n calls —
	// an allocation hotspot), ids are precomputed once and a permutation
	// index is stable-sorted (inheriting the caller-slice-unchanged
	// contract).
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
	// emitted — the set of ids carried in memories. A relation is emitted
	// only when both endpoints (From·To) are in this set. Relations
	// pointing at a pending superseder (an unapproved replacing source)·
	// a superseder outside the query range would become dangling
	// references, causing the import side's lineage restoration
	// (import_pam.go SupersedePair) to skip for missing target rows — a
	// lineage break — so they are not emitted at the source.
	emitted := make(map[string]bool, len(rows))
	// emittedAt — for §13.1 relation.created_at: the creation times of the
	// emitted rows.
	emittedAt := make(map[string]time.Time, len(rows))
	for _, i := range order {
		r := rows[i]
		if r.Status == "pending" {
			// pending (unapproved internal candidates) is excluded from the
			// PAM document — the closed classification has no unapproved
			// notion (archived means 'historical preservation', so it would
			// distort the meaning), and unapproved candidates are not put in
			// a standard exchange document. Excluded from relations·
			// total_memories·the checksum alike.
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
			continue // an id absent from the document on either side — the dangling relation is not emitted
		}
		// §13.1 created_at — when the supersession was established = the
		// creation time of the new (replacing source) row. The fallback for
		// hand-written rows without a source time is the old row's time.
		at := emittedAt[from]
		if at.IsZero() {
			at = emittedAt[to]
		}
		relations = append(relations, pamExportRelation{
			ID:        pamRelationID(from, to, "supersedes"),
			From:      from, // new (replacing source)
			To:        to,   // old (replaced target)
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
		// §18 — the signature is derived from the pre-assembly payload
		// (avoiding re-serialization): {checksum, export_id, export_date,
		// owner_id} JCS → Ed25519.
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
