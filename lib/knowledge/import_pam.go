package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type ImportCandidate struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Content    string         `json:"content"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source"`
	CreatedAt  string         `json:"created_at,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type PAMSkipped struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type SupersedePair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type PAMLinkPair struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

type PAMResult struct {
	Candidates     []ImportCandidate
	Skipped        []PAMSkipped
	SupersedePairs []SupersedePair
	Links          []PAMLinkPair
}

var ParsePAMFn = ParsePAM

type pamDoc struct {
	Schema     string           `json:"schema"`
	ExportID   string           `json:"export_id"`
	ExportDate string           `json:"export_date"`
	Owner      *pamOwnerRef     `json:"owner"`
	Memories   []pamMemory      `json:"memories"`
	Relations  []pamRelation    `json:"relations"`
	Integrity  *pamIntegrityRef `json:"integrity"`
	Signature  *pamSignatureRef `json:"signature"`
}

type pamOwnerRef struct {
	ID string `json:"id"`
}

type pamIntegrityRef struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

type pamSignatureRef struct {
	Algorithm string `json:"algorithm"`
	SignedBy  string `json:"signed_by"`
	Value     string `json:"value"`
}

var pamImportStderr io.Writer = os.Stderr

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
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata"`
	Temporal   pamTemporal    `json:"temporal"`
	Status     string         `json:"status"`
	Confidence pamConfidence  `json:"confidence"`
	Tags       []string       `json:"tags"`
	Provenance pamProvenance  `json:"provenance"`
}

type pamTemporal struct {
	CreatedAt string `json:"created_at"`
}

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

	if doc.Integrity != nil {
		if err := verifyPAMChecksum(data, doc.Integrity.Checksum); err != nil {
			return nil, err
		}
	}

	verifyPAMSignature(&doc)
	res := &PAMResult{}
	for _, m := range doc.Memories {
		if m.Status != "active" {

			res.Skipped = append(res.Skipped, PAMSkipped{ID: m.ID, Type: m.Type, Status: m.Status})
			continue
		}
		c := ImportCandidate{
			ID:         m.ID,
			Kind:       "knowledge",
			Content:    m.Content,
			Tags:       m.Tags,
			Confidence: 0.5,
			Source:     "import:pam:" + m.Provenance.Platform,
			CreatedAt:  m.Temporal.CreatedAt,
		}
		if m.Confidence.Current != nil {
			c.Confidence = clampUnit(*m.Confidence.Current)
		}
		if k, ok := pamKindByType[m.Type]; ok {
			c.Kind = k
		} else {

			if c.Metadata == nil {
				c.Metadata = map[string]any{}
			}
			c.Metadata["custom_type"] = m.Type
		}

		if h, ok := m.Metadata["content_hash"].(string); ok && isHex64(h) {
			if c.Metadata == nil {
				c.Metadata = map[string]any{}
			}
			c.Metadata["content_hash"] = h
		}
		res.Candidates = append(res.Candidates, c)
	}
	for _, r := range doc.Relations {
		if r.Type == "supersedes" {

			res.SupersedePairs = append(res.SupersedePairs, SupersedePair{From: r.From, To: r.To})
			continue
		}

		if store.ValidRelation(r.Type) {
			res.Links = append(res.Links, PAMLinkPair{
				From:     r.From,
				To:       r.To,
				Relation: strings.ToLower(strings.TrimSpace(r.Type)),
			})
		}
	}
	return res, nil
}

func RestorePAMLinks(ctx context.Context, st wire.Store, rowIDOf func(pamID string) (int, bool), pairs []PAMLinkPair) (int, error) {
	applied := 0
	for _, p := range pairs {
		fromID, okFrom := rowIDOf(p.From)
		toID, okTo := rowIDOf(p.To)
		if !okFrom || fromID <= 0 || !okTo || toID <= 0 {
			fmt.Fprintf(pamImportStderr,
				"[flint] import pam: relation(%s %s→%s) target missing — link skipped\n",
				p.Relation, p.From, p.To)
			continue
		}
		if fromID == toID {
			fmt.Fprintf(pamImportStderr,
				"[flint] import pam: relation(%s %s→%s) self-referencing — link skipped\n",
				p.Relation, p.From, p.To)
			continue
		}
		if err := insertKnowledgeLinks(ctx, st, fromID, []linkSpec{{To: toID, Relation: p.Relation}}); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
