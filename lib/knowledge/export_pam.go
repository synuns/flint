package knowledge

import (
	"bytes"
	"context"
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

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const pamSpecURI = "https://portable-ai-memory.org/spec/v1.0"

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

type pamExportOwner struct {
	ID string `json:"id"`
}

type pamExportSignature struct {
	Algorithm string `json:"algorithm"`
	SignedBy  string `json:"signed_by"`
	Value     string `json:"value"`
}

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

type pamExportTemporal struct {
	CreatedAt    string `json:"created_at"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

type pamExportConfidence struct {
	Current float64 `json:"current"`
}

type pamExportProvenance struct {
	Platform string `json:"platform"`
}

type pamExportRelation struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
}

type PAMLinkRow struct {
	FromID    int
	ToID      int
	Relation  string
	CreatedAt time.Time
}

func ExportLinkRows(ctx context.Context, st wire.Store) ([]PAMLinkRow, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT from_id, to_id, relation, created_at FROM knowledge_links`, nil)
	if err != nil {
		return nil, fmt.Errorf("export: links query failed: %w", err)
	}
	out := make([]PAMLinkRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		f, okF := render.CellToInt(r[0])
		o, okT := render.CellToInt(r[1])
		rel, _ := r[2].(string)
		at, _ := r[3].(time.Time)
		if !okF || !okT || rel == "" {
			continue
		}
		out = append(out, PAMLinkRow{FromID: f, ToID: o, Relation: rel, CreatedAt: at})
	}
	return out, nil
}

type pamExportIntegrity struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

var pamWhitespaceRe = regexp.MustCompile(`\s+`)

func PAMContentHash(content string) string {
	s := strings.TrimSpace(strings.ToLower(content))
	s = string(norm.NFC.String(s))
	s = pamWhitespaceRe.ReplaceAllString(s, " ")
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pamUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pam export_id issue failed: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func pamStatus(status string) string {
	switch status {
	case "active":
		return "active"
	case "invalidated":
		return "superseded"
	default:
		return "archived"
	}
}

func toPAMExportMemory(r ExportRow) pamExportMemory {
	m := pamExportMemory{
		ID:          fmt.Sprintf("flint:%d", r.ID),
		Type:        "custom",
		CustomType:  r.Kind,
		Content:     r.Content,
		ContentHash: PAMContentHash(r.Content),
		Temporal: pamExportTemporal{
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		},
		Status:     pamStatus(r.Status),
		Tags:       r.Tags,
		Confidence: pamExportConfidence{Current: r.Confidence},
		Provenance: pamExportProvenance{Platform: "flint"},
	}
	if r.Tags == nil {
		m.Tags = []string{}
	}

	if isHex64(r.ContentHash) {
		m.Metadata = map[string]string{"content_hash": r.ContentHash}
	}
	if r.SupersededBy != nil {
		m.Temporal.SupersededBy = fmt.Sprintf("flint:%d", *r.SupersededBy)
	}
	return m
}

const pamJCSHex = "0123456789abcdef"

const jcsChunk = 8192

type jcsBuf struct {
	h hash.Hash
	b []byte
}

func (w *jcsBuf) flush() {
	if len(w.b) > 0 {
		w.h.Write(w.b)
		w.b = w.b[:0]
	}
}

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
				w.put("\ufffd")
			} else {
				w.put(s[i : i+size])
				i += size - 1
			}
		}
	}
	w.putByte('"')
}

func (w *jcsBuf) num(f float64) error {
	s, err := jsoncanonicalizer.NumberToJSON(f)
	if err != nil {
		return fmt.Errorf("pam JCS number invalid: %w", err)
	}
	w.put(s)
	return nil
}

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

type pamDocWriter struct {
	buf   bytes.Buffer
	small bytes.Buffer
	enc   *json.Encoder
}

func newPAMDocWriter(est int) *pamDocWriter {
	w := &pamDocWriter{}
	w.buf.Grow(est)
	w.enc = json.NewEncoder(&w.small)
	w.enc.SetEscapeHTML(false)
	return w
}

func (w *pamDocWriter) piece(v any) ([]byte, error) {
	w.small.Reset()
	if err := w.enc.Encode(v); err != nil {
		return nil, err
	}
	return w.small.Bytes()[:w.small.Len()-1], nil
}

func (w *pamDocWriter) scalar(s string) error {
	p, err := w.piece(s)
	if err != nil {
		return err
	}
	w.buf.Write(p)
	return nil
}

func (w *pamDocWriter) value(v any, prefix string) error {
	p, err := w.piece(v)
	if err != nil {
		return err
	}
	return json.Indent(&w.buf, p, prefix, "  ")
}

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
	b.WriteString("\n}\n")
	return nil
}

func pamDocEstimate(memories []pamExportMemory, relations []pamExportRelation) int {
	est := 512
	for i := range memories {
		est += 420 + 2*len(memories[i].Content) + 32*len(memories[i].Tags)
	}
	return est + 320*len(relations)
}

func FormatPAM(rows []ExportRow, exportedBy string, links ...PAMLinkRow) ([]byte, error) {
	data, _, err := FormatPAMWithCount(rows, exportedBy, links...)
	return data, err
}

func FormatPAMWithCount(rows []ExportRow, exportedBy string, links ...PAMLinkRow) ([]byte, int, error) {
	return formatPAMDoc(rows, exportedBy, nil, links)
}

func FormatPAMSigned(rows []ExportRow, exportedBy string, key ed25519.PrivateKey, links ...PAMLinkRow) ([]byte, error) {
	data, _, err := FormatPAMSignedWithCount(rows, exportedBy, key, links...)
	return data, err
}

func FormatPAMSignedWithCount(rows []ExportRow, exportedBy string, key ed25519.PrivateKey, links ...PAMLinkRow) ([]byte, int, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, 0, fmt.Errorf("pam signing key invalid (want %d bytes, got %d)",
			ed25519.PrivateKeySize, len(key))
	}
	return formatPAMDoc(rows, exportedBy, key, links)
}

func formatPAMDoc(rows []ExportRow, exportedBy string, key ed25519.PrivateKey, links []PAMLinkRow) ([]byte, int, error) {

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

	emitted := make(map[string]bool, len(rows))

	emittedAt := make(map[string]time.Time, len(rows))
	for _, i := range order {
		r := rows[i]
		if r.Status == "pending" {

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
			continue
		}

		at := emittedAt[from]
		if at.IsZero() {
			at = emittedAt[to]
		}
		relations = append(relations, pamExportRelation{
			ID:        pamRelationID(from, to, "supersedes"),
			From:      from,
			To:        to,
			Type:      "supersedes",
			CreatedAt: at.UTC().Format(time.RFC3339),
		})
	}

	sortedLinks := make([]PAMLinkRow, len(links))
	copy(sortedLinks, links)
	sort.SliceStable(sortedLinks, func(a, b int) bool {
		x, y := sortedLinks[a], sortedLinks[b]
		if x.FromID != y.FromID {
			return x.FromID < y.FromID
		}
		if x.ToID != y.ToID {
			return x.ToID < y.ToID
		}
		return x.Relation < y.Relation
	})
	for _, l := range sortedLinks {
		from := "flint:" + strconv.Itoa(l.FromID)
		to := "flint:" + strconv.Itoa(l.ToID)
		if !emitted[from] || !emitted[to] {
			continue
		}
		at := l.CreatedAt
		if at.IsZero() {
			at = emittedAt[from]
		}
		relations = append(relations, pamExportRelation{
			ID:        pamRelationID(from, to, l.Relation),
			From:      from,
			To:        to,
			Type:      l.Relation,
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
