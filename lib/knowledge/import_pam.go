// import_pam.go — PAM (Portable AI Memory) v1.0 reading. A pure parser
// converting an external PAM export document into flint import candidates —
// no store contact (contract 5): the pending accrual policy·actual write
// live in import_migrate.go. Wired through the seam variable ParsePAMFn.
// ① Recompute and verify integrity.checksum (§15) — mismatch is an error
// (caller exit 3·quiet pass forbidden) ② If a signature (§18) is present,
// verify Ed25519 — failure is a one-line stderr warning then continue (§21.4
// SHOULD).
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

// ImportCandidate — the flint conversion of one PAM memory. Field names are
// ExportRow-compatible (round-trip jsonl export·import symmetry) and it
// contains only accrual-eligible items (status active).
type ImportCandidate struct {
	ID         string         `json:"id"` // the original PAM memory id (for lineage reconnection)
	Kind       string         `json:"kind"`
	Content    string         `json:"content"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source"` // "import:pam:"+platform
	CreatedAt  string         `json:"created_at,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PAMSkipped — a memory whose status is not accrual-eligible (for reporting).
type PAMSkipped struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"` // superseded/deprecated/retracted/archived
}

// SupersedePair — a relations pair with type=="supersedes". Direction is
// fixed per PAM §13.2 "supersedes: Source replaces target":
// From=new (the replacing source), To=old (the replaced target).
// That is, "From supersedes To" — the consumer side (importPAM lineage
// restoration) must invoke exactly this meaning:
// Supersede(oldID=To's accrued id, newID=From's accrued id).
type SupersedePair struct {
	From string `json:"from"` // new — the replacing memory (source)
	To   string `json:"to"`   // old — the replaced memory (target)
}

// PAMLinkPair — a relations entry of the 4-word vocabulary (v1.20.0 T5):
// the import side of the knowledge_links round trip (from = the link's
// source row, to = its target — the same direction the export wrote).
// supersedes is NOT here — it stays in SupersedePairs
// (knowledge.superseded_by, the dual-source-of-truth avoidance).
type PAMLinkPair struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

// PAMResult — the ParsePAM return bundle.
type PAMResult struct {
	Candidates     []ImportCandidate
	Skipped        []PAMSkipped
	SupersedePairs []SupersedePair
	Links          []PAMLinkPair
}

// ParsePAMFn — the consumer seam (test-swappable).
var ParsePAMFn = ParsePAM

// PAM v1.0 document structure (only the minimal fields needed — summary is
// ignored: content wins).
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

// pamOwnerRef — the root owner (§4·§18 minimal for signature verification).
type pamOwnerRef struct {
	ID string `json:"id"`
}

// pamIntegrityRef — the §15 checksum (verification target).
type pamIntegrityRef struct {
	Checksum      string `json:"checksum"`
	TotalMemories int    `json:"total_memories"`
}

// pamSignatureRef — the §18 document signature.
type pamSignatureRef struct {
	Algorithm string `json:"algorithm"`
	SignedBy  string `json:"signed_by"`
	Value     string `json:"value"`
}

// pamImportStderr — the §21.4 warning channel seam (default os.Stderr —
// captured in tests).
var pamImportStderr io.Writer = os.Stderr

// verifyPAMChecksum — §15 recomputation: sort the original memories (raw
// bytes preserved — a struct round-trip that drops undefined fields would
// not reproduce the checksum) by ascending id string → JCS → SHA-256. A
// document without a checksum (pre-§15 convention) passes unverified (nil
// return).
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
	// §15 — ascending id string (code point) order (the same sort contract
	// as export_pam.go). Stable sort: equal ids keep original order.
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

// verifyPAMSignature — §18: if a signature is present, verify payload
// {checksum, export_id, export_date, owner_id} JCS → Ed25519. Failure·
// unresolvable key is a one-line warning (§21.4 SHOULD — warn and continue,
// not reject). Unsigned documents pass without warning.
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
	Metadata   map[string]any `json:"metadata"` // §10 extension — carries the A2 storage hash
	Temporal   pamTemporal    `json:"temporal"`
	Status     string         `json:"status"`
	Confidence pamConfidence  `json:"confidence"`
	Tags       []string       `json:"tags"`
	Provenance pamProvenance  `json:"provenance"`
}

type pamTemporal struct {
	CreatedAt string `json:"created_at"`
}

// Current pointer — distinguishes omission (zero deserialization) from an
// explicit zero to apply the 0.5 default.
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

// pamKindByType — PAM type → flint kind. Nine standard knowledge shapes +
// project. custom (and unknown types) are absent here — they fall to the
// knowledge + Metadata["custom_type"] original-preserving path.
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

// ParsePAM — converts a PAM v1.0 document (data) into candidates·skips·
// supersedes pairs. Root schema mismatch·corrupt json are errors (corruption
// includes offset detail).
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
	// §15 — integrity.checksum recompute verification (mismatch = document
	// tampering → error, caller exit 3). Quiet pass forbidden.
	if doc.Integrity != nil {
		if err := verifyPAMChecksum(data, doc.Integrity.Checksum); err != nil {
			return nil, err
		}
	}
	// §21.4 — signature is a SHOULD verify: on failure, warn and continue
	// (the function above).
	verifyPAMSignature(&doc)
	res := &PAMResult{}
	for _, m := range doc.Memories {
		if m.Status != "active" {
			// superseded/deprecated/retracted/archived — not pending-accrual
			// eligible. Unknown statuses beyond the five contract-named ones
			// also stay as a visible skip report instead of a quiet accrual
			// (original status preserved).
			res.Skipped = append(res.Skipped, PAMSkipped{ID: m.ID, Type: m.Type, Status: m.Status})
			continue
		}
		c := ImportCandidate{
			ID:         m.ID,
			Kind:       "knowledge",
			Content:    m.Content, // content wins — summary ignored (contract 2)
			Tags:       m.Tags,
			Confidence: 0.5, // default for omission on the import path (import.go convention)
			Source:     "import:pam:" + m.Provenance.Platform,
			CreatedAt:  m.Temporal.CreatedAt,
		}
		if m.Confidence.Current != nil {
			c.Confidence = clampUnit(*m.Confidence.Current)
		}
		if k, ok := pamKindByType[m.Type]; ok {
			c.Kind = k
		} else {
			// custom — original custom_type preserved
			if c.Metadata == nil {
				c.Metadata = map[string]any{}
			}
			c.Metadata["custom_type"] = m.Type
		}
		// A2 (round-trip idempotence): a flint-issued document's
		// metadata.content_hash (64 hex) carries over to the candidate
		// (jsonl A9 isHex64 symmetry — non-hex·absent are not carried). The
		// import accrual path keeps dedup symmetric against the source-stored
		// hash.
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
			// §13.2 "Source replaces target": from=new, to=old — the raw
			// relation direction is fixed as-is (see the SupersedePair
			// comment).
			res.SupersedePairs = append(res.SupersedePairs, SupersedePair{From: r.From, To: r.To})
			continue
		}
		// v1.20.0 T5 — the 4-vocabulary knowledge_links relations (the
		// export side of the round trip). Unregistered types stay
		// unconsumed (neither family — a foreign document's custom
		// relation is not flint's to store).
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

// RestorePAMLinks — re-accrues the parsed 4-vocabulary PAM relations
// onto the flint row ids (the write side of the round trip; the import
// caller supplies the PAM-id→row-id map from the accrual outcome).
// Store-contacting on purpose — ParsePAM stays pure, this helper is the
// seam the import path composes with it. A pair whose endpoint row is
// absent is skipped with one stderr warning (the SupersedePair
// target-missing convention: a partial document must not fail the whole
// import). v1.20.2 (r88): a self-referential pair (from == to) gets the
// same skip+warning treatment — the write path's self-link refusal
// (insertKnowledgeLinks) is a hard error, and letting it fire here
// failed the WHOLE document over one malformed relation, violating
// that same partial-document contract. Idempotence rides
// insertKnowledgeLinks (the NOT EXISTS guard).
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

// clampUnit — clamp to [0,1] (confidence.current).
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
