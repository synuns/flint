// write.go — flint write <kind>: a five-stage pipeline (validate→dedup→
// classify→commit→output). Intake metering (RecordUsage)·decision threshold
// loader (WriteThresholds)·sanitization (SanitizePayload) are the knowledge
// shared surfaces — read·hot·done and residual cli consumers inherit them
// via the cli/knowledge_alias.go aliases.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// kindColumns — the storable columns per kind (spec §6). Non-column keys of
// the payload (e.g. session's tasks) are metadata and dropped. project's
// note is a column (F5).
var kindColumns = map[string]map[string]bool{
	"knowledge": {"topic": true, "summary": true, "tags": true, "entity": true,
		"domain": true, "confidence": true, "origin": true, "status": true,
		"content_hash": true, "session_id": true, "valid_at": true},
	"session": {"ts": true, "agent": true, "summary": true},
	// note is a SchemaV2 column (projects.note), so the drop-as-metadata
	// treatment is abolished — registered as a storage column.
	"project": {"name": true, "status": true, "updated": true, "note": true},
}

// ErrInvalidSource — an invalid --source value (exit 3). There are only
// three intake metering sources.
var ErrInvalidSource = errors.New("write: invalid --source (hook|manual|mcp)")

// validSources — the allowed metering sources (spec §1: hook via hook ·
// human/default manual · MCP mcp).
var validSources = map[string]bool{"hook": true, "manual": true, "mcp": true}

// ParseSourceFlag — parses the --source value from args (plan T61). Unset
// is "manual"; invalid values·a missing value are ErrInvalidSource (the CLI
// exits 3).
func ParseSourceFlag(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != "--source" {
			continue
		}
		if i+1 >= len(args) {
			return "", ErrInvalidSource
		}
		src := args[i+1]
		if !validSources[src] {
			return "", ErrInvalidSource
		}
		return src, nil
	}
	return "manual", nil
}

// UsageStore — a store capable of intake metering (RecordUsage, T60) (the
// same assertion pattern as maintain's MaintStore). A non-implementing
// Store has RecordUsage skipped as a no-op.
type UsageStore interface {
	RecordUsage(ctx context.Context, kind, source string) error
}

// DupIDStore — the extension supplying the existing row id for duplicate
// lines (R3). realStore implements it via db.Repo.DupIDByHash
// (cli/realstore_write.go); a non-implementing Store (legacy fake) falls
// back to ExistsHash (id 0 — a bare duplicate output, backward-compatible).
type DupIDStore interface {
	DupIDByHash(ctx context.Context, hash string) (int, bool, error)
}

// DupMeta — the existing-row metadata for a duplicate line (v0.18.0 R1).
// Returns topic·state to the resending writer to induce switching to read
// (recall)·supersede (succession)·approve (pending promotion) instead of
// rewriting — an already-exists feedback loop.
type DupMeta struct {
	ID    int
	Topic string
	State string
}

// DupMetaStore — the metadata extension of DupIDStore (v0.18.0 R1).
// realStore implements it via db.Repo.DupRowByHash (cli/realstore_write.go).
// A non-implementing Store falls back DupIDStore→ExistsHash in that order
// (id only without metadata·or a bare duplicate — backward-compatible).
type DupMetaStore interface {
	DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error)
}

// lookupHash — checks content_hash duplication. A DupMetaStore also returns
// the existing row's metadata (duplicate-response topic·state exposure,
// R1). A DupIDStore returns the id only; a non-implementing Store falls
// back to ExistsHash.
func lookupHash(ctx context.Context, st wire.Store, hash string) (DupMeta, bool, error) {
	if ms, ok := st.(DupMetaStore); ok {
		return ms.DupRowByHash(ctx, hash)
	}
	if ds, ok := st.(DupIDStore); ok {
		id, exists, err := ds.DupIDByHash(ctx, hash)
		return DupMeta{ID: id}, exists, err
	}
	exists, err := st.ExistsHash(ctx, hash)
	return DupMeta{}, exists, err
}

// UsageCountStore — the intake metering extension that increments counts in
// bulk. RecordDups records dup N with one call (avoiding O(N)
// read-writes — db.Repo.RecordUsageCount). A non-implementing Store
// (legacy fake) has RecordDups fall back to N single calls.
type UsageCountStore interface {
	RecordUsageCount(ctx context.Context, kind, source string, count int) error
}

// RecordUsage — records one intake if the Store is a UsageStore. A
// non-implementation is a no-op; failures are quiet (metering never breaks
// the write result·exit code — plan T61).
func RecordUsage(ctx context.Context, st wire.Store, kind, source string) {
	if us, ok := st.(UsageStore); ok {
		_ = us.RecordUsage(ctx, kind, source)
	}
}

// WriteThresholds — reuses wire.LoadThresholds (inheriting the
// doneThresholds convention): decision thresholds are the write-time meta
// values. A read failure falls back to defaults (non-interference: decision
// loading never changes the write result·exit).
func WriteThresholds(ctx context.Context, st wire.Store) store.Thresholds {
	th, err := wire.LoadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
}

// RunWrite — processes multiple stdin JSON/JSONL entries. The commit is one
// per batch; dedup skips duplicate lines·partially commits the new lines
// (all-duplicate means a zero-row commit). Since v1.6.0 T3, the
// pre-duplicate check for multi-line knowledge is one batched hash-array
// query (lookupHashBatch — abolishing per-line N round trips); the
// single-shot (1-row hook pattern) keeps the per-line query contract
// unchanged.
// v1.5.5 T2 A5: --help/-h prints the usage hint exit 0 before waiting on
// stdin.
func RunWrite(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// On --help/-h detection, print the usage hint exit 0 — before waiting
	// on stdin (abolishing the issue where write takes kind as a positional
	// argument, so `--help` was mistaken for the kind and hung on the stdin
	// pipe).
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "usage: flint write <kind> [--source hook|manual|mcp] [--session ID] — kind: knowledge|session|project, payload: JSON/JSONL on stdin")
			return 0
		}
	}
	// write follows the convention of absorbing unknown flags into
	// positional arguments (the kind·positional family — DashPositional),
	// so the spec is reflected as-is. --source value validation inherits
	// the ParseSourceFlag single source (first-occurrence validation
	// contract); the --help/-h pre-scan also stays in the loop above.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--session", Value: true},
			{Name: "--source", Value: true},
		},
		AllowPositional: true,
		DashPositional:  true,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	kind, session := "", res.Value("--session")
	if pos := res.Positional(); len(pos) > 0 {
		kind = pos[0]
	}
	source, err := ParseSourceFlag(args)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}

	payloads, err := readPayloads(stdin)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if len(payloads) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "payload", Expect: "non-empty JSON/JSONL on stdin"}))
		return 3
	}
	// The stdin row cap (reusing the registry key export_import_max_rows —
	// import symmetry). Blocking right after parsing·before side effects
	// provides the memory defense against accruing a huge stdin in full
	// (error JSON {max,count}·exit 3).
	if code := enforceWriteMaxRows(ctx, st, stdout, len(payloads)); code != 0 {
		return code
	}

	// Validate → sanitize → normalize → hash dedup (per-line skip) →
	// classify. Only validation failure returns immediately (exit 3).
	// Duplicate lines are skipped, not discarding the whole batch — partial
	// commit. The classification floor is the meta conf_floor this write
	// read (pass-by-value — no global injection).
	th := WriteThresholds(ctx, st)
	floor := th.ConfFloor
	// Personal-vocabulary (vocab_tags·vocab_domains comma lists) union
	// injection — personal vocabulary lives in the operational DB, not the
	// repo.
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('vocab_tags','vocab_domains')`, nil); err == nil {
		var tags, domains []string
		for _, r := range rows {
			if len(r) != 2 {
				continue
			}
			vals := strings.Split(fmt.Sprint(r[1]), ",")
			if fmt.Sprint(r[0]) == "vocab_tags" {
				tags = vals
			} else {
				domains = vals
			}
		}
		store.SetVocabExtras(tags, domains)
	}
	recs := make([]map[string]any, 0, len(payloads))
	dropped := map[string]bool{}
	var warnings []string
	hash, state := "", "active"
	dups := 0
	dupID := 0     // the existing row id for a single-line duplicate (R3) — stays 0 for unsupported Stores·multi-line
	dupTopic := "" // the existing row's topic·state — resender feedback
	dupState := ""
	// Same-key lines within a batch (knowledge content_hash·project name):
	// prevents a whole InsertBatch failure
	seen := map[string]bool{}
	// Batched pre-dedup for multi-line knowledge — instead of per-line
	// lookupHash (N round trips — RTT×N dominates large batches), hashes
	// are collected and detected after the loop with one lookupHashBatch
	// query (content_hash = ANY($1) chunks, reused from import), keeping
	// detection identical. The single-shot (len==1, hook pattern) keeps the
	// existing per-line query contract (DupMetaStore→DupIDStore→ExistsHash
	// fallback) unchanged.
	batched := len(payloads) > 1
	var stagedN []map[string]any // knowledge lines awaiting the batch query (order preserved)
	var stagedH []string         // content_hash at the same index as stagedN
	// finalizeRec — finalizes a line that passed classification: kind
	// tagging·non-column key cleanup·recs accrual.
	finalizeRec := func(n map[string]any) {
		n["kind"] = kind
		for k := range n {
			if !kindColumns[kind][k] && k != "kind" {
				delete(n, k)
				dropped[k] = true
			}
		}
		recs = append(recs, n)
	}
	for _, p := range payloads {
		// The per-payload field byte cap — rejected at the Validate stage
		// (exit 3). Blocks the OOM path where a huge single field rides
		// through the sanitize·hash·accrual pipeline and the stored body
		// wholesale.
		if se := validateFieldBytes(p); se != nil {
			fmt.Fprintln(stdout, wire.ErrLine(se))
			return 3
		}
		if se := store.Validate(kind, p); se != nil {
			fmt.Fprintln(stdout, wire.ErrLine(se))
			return 3
		}
		// Sanitization (ADR-0006): neutralize control characters·[flint]
		// markers before storage + secret scanning. This stage precedes
		// hash computation, so the sanitized result becomes the stored·
		// hashed body.
		warnings = append(warnings, SanitizePayload(p)...)
		n := store.Normalize(kind, p)
		if kind == "knowledge" {
			// Vocabulary validation: merge warnings based on the Normalized
			// domain and tags — warnings only (no storage block·exit
			// unchanged). Order preserved after the sanitization warnings.
			if raw, ok := p["tags"].([]any); ok {
				tags := make([]string, 0, len(raw))
				for _, tag := range raw {
					if s, ok := tag.(string); ok {
						tags = append(tags, s)
					}
				}
				domain, _ := n["domain"].(string)
				warnings = append(warnings, store.CheckVocabulary(tags, domain)...)
			}
			// The hash input must be the volatile-field-excluded path
			// (HashCanonical — valid_at etc.) so resending the same payload
			// is judged duplicate (seeding idempotence).
			h := store.ContentHash(store.HashCanonical(n))
			if seen[h] { // a duplicate line within the same batch is also skipped
				dups++
				continue
			}
			seen[h] = true
			if batched {
				// Multi-line: defer the pre-query to one batch after the
				// loop — collect hashes only.
				n["content_hash"] = h
				stagedN = append(stagedN, n)
				stagedH = append(stagedH, h)
				continue
			}
			existsMeta, exists, err := lookupHash(ctx, st, h)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
			if exists {
				dups++ // per-line dedup: only this line is skipped; the remaining new lines commit
				if len(payloads) == 1 {
					dupID = existsMeta.ID // single-line resend: expose the existing row metadata (R3·R1)
					dupTopic, dupState = existsMeta.Topic, existsMeta.State
				}
				continue
			}
			conf, _ := n["confidence"].(float64)
			state = store.Classify(kind, conf, floor)
			n["content_hash"] = h
			n["status"] = state
			hash = h
		}
		if kind == "project" {
			// Per-line dedup on the natural key name (the same contract as
			// knowledge content_hash — skipping unique(name) collisions
			// before the batch secures seeding idempotence).
			name, _ := n["name"].(string)
			if seen["project:"+name] { // a same-name line within the same batch is also skipped
				dups++
				continue
			}
			seen["project:"+name] = true
			exists, err := st.ExistsProject(ctx, name)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return store.MapExit(err)
			}
			if exists {
				dups++ // per-line dedup: only this line is skipped; the remaining new lines commit
				continue
			}
		}
		n["kind"] = kind
		finalizeRec(n)
	}

	// Multi-line knowledge finalize — one batch query over the collected
	// hashes (ANY($1) chunks) skips existing lines; only new lines are
	// classified·accrued (line order preserved — partial commit·metering·
	// warning contracts identical to the single-shot path). A query error
	// propagates without silent new-line classification (the error contract
	// — MapExit chain preserved).
	if len(stagedH) > 0 {
		existing, err := lookupHashBatch(ctx, st, stagedH)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		for i, n := range stagedN {
			if _, dup := existing[stagedH[i]]; dup {
				dups++ // batched dedup detection identical: only this line is skipped; the rest commit
				continue
			}
			conf, _ := n["confidence"].(float64)
			state = store.Classify(kind, conf, floor)
			n["status"] = state
			hash = stagedH[i]
			finalizeRec(n)
		}
	}

	// emitDuplicate — all duplicates (common to the pre-check·race
	// recheck): zero-row commit·exit 0 "duplicate" (seeding idempotence).
	// Intake metering is write × 1 + the actual skipped-line count (no
	// double counting); warnings are not discarded (the dup line's
	// sanitization·secret warnings survive).
	emitDuplicate := func() {
		RecordUsage(ctx, st, "write", source)
		RecordDups(ctx, st, source, dups)
		out := store.DuplicateMetaJSON(dupID, dupTopic, dupState)
		if len(dropped) > 0 || len(warnings) > 0 {
			out = withWarnings(out, dropped, warnings)
		}
		fmt.Fprintln(stdout, out)
	}

	if len(recs) == 0 {
		emitDuplicate()
		return 0
	}
	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		if !errors.Is(err, store.ErrDuplicate) {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		// The batch rolled back due to a race (concurrent insert after the
		// pre-check passed): recheck the unique keys (knowledge
		// content_hash·project name) of surviving recs, skip only the lines
		// that actually exist, and retry the remainder once — discarding
		// everything then exiting 0 "duplicate" would lose new lines and is
		// forbidden.
		survive := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			if r["kind"] == "project" {
				// Project lines recheck the natural key name (they have no
				// content_hash, so deciding by retry would collide again and
				// turn into an error — the chain of first-seeding failures).
				name, _ := r["name"].(string)
				exists, err2 := st.ExistsProject(ctx, name)
				if err2 != nil {
					fmt.Fprintln(stdout, wire.ErrLine(err2))
					return store.MapExit(err2)
				}
				if exists {
					dups++ // concurrently inserted line: skip — metering also reflects only actual skips
					continue
				}
				survive = append(survive, r)
				continue
			}
			h, _ := r["content_hash"].(string)
			if h == "" {
				survive = append(survive, r) // kinds without a hash cannot be rechecked — decided by retry
				continue
			}
			existsMeta, exists, err2 := lookupHash(ctx, st, h)
			if err2 != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err2))
				return store.MapExit(err2)
			}
			if exists {
				dups++ // concurrently inserted line: skip — metering also reflects only actual skips
				if len(payloads) == 1 {
					dupID = existsMeta.ID // the race-recheck path also exposes the existing row metadata (R3·R1)
					dupTopic, dupState = existsMeta.Topic, existsMeta.State
				}
				continue
			}
			survive = append(survive, r)
		}
		if len(survive) == 0 {
			// Everything exists on recheck: the path with no new-line loss —
			// exit 0 "duplicate".
			emitDuplicate()
			return 0
		}
		ids, err = st.InsertBatch(ctx, survive)
		if err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				// ErrDuplicate even on retry: an actual collision of a hash
				// that passed the recheck — strip the ErrDuplicate
				// qualification and report it as an error (only the
				// loss-free path exits 0).
				err = fmt.Errorf("write: batch conflict after dedup recheck: %v", err)
			}
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		recs = survive
	}
	// D4 (R33): the commit title — project prefers name (knowledge lacks
	// name, so summary). If both are absent, the "%v" <nil> fallback (the
	// existing contract kept).
	subject := recs[0]["name"]
	if subject == nil {
		subject = recs[0]["summary"]
	}
	msg := fmt.Sprintf("%s: %v", kind, subject)
	if session != "" {
		msg += fmt.Sprintf(" [session:%s]", session)
	}
	commitHash, err := st.Commit(ctx, msg)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	var out string
	if len(payloads) == 1 {
		// A single line keeps the existing {status,hash,state,id}
		// (backward-compatible).
		id := 0
		if len(ids) > 0 {
			id = ids[0]
		}
		out = store.OutputJSON("committed", hash, state, id)
	} else {
		// Multi-line: an aggregate single commit hash instead of mixing the
		// last line's hash·state + the first line's id.
		out = store.MultiCommitOutput(len(recs), dups, commitHash)
	}
	if len(dropped) > 0 || len(warnings) > 0 {
		out = withWarnings(out, dropped, warnings)
	}
	RecordUsage(ctx, st, "write", source) // one intake after a successful new commit
	RecordDups(ctx, st, source, dups)     // the skipped lines of a mixed batch are also dup-metered (total+source)
	fmt.Fprintln(stdout, out)
	return 0
}

// RecordDups — meters dup intake by the number of skipped duplicate lines
// (0 is a no-op). The total "dup" key is invariant (ruling); per-source
// "dup_"+source (hook·manual·mcp) is additionally incremented (additive —
// v0.10.2 R2). A UsageCountStore (the real implementation) increments each
// key in bulk with one count call; a non-implementing Store does it one at
// a time.
func RecordDups(ctx context.Context, st wire.Store, source string, n int) {
	if n <= 0 {
		return
	}
	if cs, ok := st.(UsageCountStore); ok {
		_ = cs.RecordUsageCount(ctx, "dup", "", n)
		_ = cs.RecordUsageCount(ctx, "dup_"+source, "", n)
		return
	}
	for i := 0; i < n; i++ {
		RecordUsage(ctx, st, "dup", "")
		RecordUsage(ctx, st, "dup_"+source, "")
	}
}

// sanitizeFields — the text columns to sanitize (the kind-common minimal
// set).
var sanitizeFields = []string{"topic", "summary", "entity", "agent", "name", "session_id"} // session_id added (defense against arbitrary unsanitized strings)

// SanitizePayload — sanitizes the text fields, updates the original map,
// and returns secret-candidate warnings ("possible secret: pattern-name").
// Not a storage rejection (warn + sanitize).
func SanitizePayload(p map[string]any) []string {
	var warns []string
	// The sanitize·secret-scan targets were expanded from the 5 string
	// fields to tags array elements·domain·origin — these fields remain
	// verbatim through normalization·storage·backup dumps and were blind
	// spots of the warning contract.
	for _, f := range sanitizeFields {
		s, ok := p[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			p[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	for _, f := range []string{"domain", "origin"} {
		s, ok := p[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			p[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	sanitizeTagElems := func(raw []string) {
		for i, s := range raw {
			clean := store.SanitizeText(s)
			if clean != s {
				raw[i] = clean
			}
			for _, name := range store.ScanSecrets(clean) {
				warns = append(warns, "possible secret: "+name)
			}
		}
	}
	if raw, ok := p["tags"].([]any); ok {
		for i, t := range raw {
			s, ok := t.(string)
			if !ok {
				continue
			}
			clean := store.SanitizeText(s)
			if clean != s {
				raw[i] = clean
			}
			for _, name := range store.ScanSecrets(clean) {
				warns = append(warns, "possible secret: "+name)
			}
		}
	}
	// []string tags (import re-planting recs — the jsonl round-trip path)
	// get the same sanitization.
	if raw, ok := p["tags"].([]string); ok {
		sanitizeTagElems(raw)
	}
	return warns
}

// withWarnings — appends a warnings array (dropped keys·merged secret
// scans) to the one-line output JSON (no silent drops·no missed
// detections).
func withWarnings(line string, dropped map[string]bool, secretWarns []string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	w := append([]string{}, secretWarns...)
	if len(dropped) > 0 {
		keys := make([]string, 0, len(dropped))
		for k := range dropped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w = append(w, "dropped keys: "+strings.Join(keys, ","))
	}
	m["warnings"] = w
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

// maxPayloadFieldBytes — the byte cap of a single payload field (string
// value). A code constant + TUNING notation, not a registry target
// (thresholds meta override). The size matches the compact hook boundary
// (compactMaxBytes 8MiB, §13 non-interference) — compact flows an 8MiB
// compact_summary straight into RunWrite as one session row, so a smaller
// cap would silently no-op normal compaction (only the boundary excess is
// blocked).
const maxPayloadFieldBytes = compactMaxBytes

// validateFieldBytes — returns a SchemaError for the field when a payload
// string field value exceeds the cap (the same exit 3 path as Validate —
// excess only is blocked).
func validateFieldBytes(p map[string]any) *store.SchemaError {
	for k, v := range p {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > maxPayloadFieldBytes {
			return &store.SchemaError{
				Field:  k,
				Expect: fmt.Sprintf("field bytes <= %d", maxPayloadFieldBytes),
			}
		}
	}
	return nil
}

// enforceWriteMaxRows — the write stdin row cap (reusing the registry key
// export_import_max_rows — the same shape as import's
// enforceImportMaxRows). On excess, prints an error JSON with max·count to
// stdout and returns 3 (the caller returns it as-is). This stage is right
// after parsing·before side effects.
func enforceWriteMaxRows(ctx context.Context, st wire.Store, stdout io.Writer, count int) int {
	max := importMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("write row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"write row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
}

// readPayloads — parses stdin as line-delimited JSON (or one bulk JSON).
func readPayloads(stdin io.Reader) ([]map[string]any, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stdin); err != nil {
		return nil, &store.SchemaError{Field: "stdin", Expect: "readable"}
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	var out []map[string]any
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, &store.SchemaError{Field: "payload", Expect: "valid JSON object per line"}
		}
		out = append(out, p)
	}
	return out, nil
}
