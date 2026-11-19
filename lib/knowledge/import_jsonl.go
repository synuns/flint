// import_jsonl.go — the flint import --format jsonl|pam migration path.
// Re-planting an export jsonl (the format) — each row reuses the existing
// write circuit (Normalize → content_hash dedup → InsertBatch), and rows
// with superseded_by references restore lineage in a post-pass reusing the
// existing Supersede transition. The existing `flint import [path]` rule
// conversion path is unchanged (RunImportRouted branches on --format
// presence — registration is the one-line delegation in knowledge.go).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseJSONLRows — turns an export jsonl (the FormatJSONL output, T3) back
// into an ExportRow slice. Parses per line; a corrupt row is an error
// carrying the 1-based row number (the caller exits 3).
func ParseJSONLRows(data []byte) ([]ExportRow, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	var rows []ExportRow
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v exportJSONLRow
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("jsonl row %d corrupt: %w", i+1, err)
		}
		rows = append(rows, ExportRow(v)) // S1016 — identical field layout, a type conversion
	}
	return rows, nil
}

// importMigrateSummary — assembles the one-line summary (the stdout
// contract: status·imported·duplicated·superseded. Id reassignments are
// remapped; PAM skips (rows whose status is not accrual-eligible·non-
// knowledge candidates) are conditionally reported as skipped).
func importMigrateSummary(imported, duplicated, superseded, remapped, skipped int) string {
	m := map[string]any{
		"status":     "ok",
		"imported":   imported,
		"duplicated": duplicated,
		"superseded": superseded,
	}
	if remapped > 0 {
		m["remapped"] = remapped
	}
	if skipped > 0 {
		m["skipped"] = skipped
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf(`{"status":"ok","imported":%d,"duplicated":%d,"superseded":%d}`,
			imported, duplicated, superseded)
	}
	return string(b)
}

// migrateValidFormats — the allowed --format values (jsonl·pam).
var migrateValidFormats = map[string]bool{"jsonl": true, "pam": true}

// parseImportMigrateArgs — the --format value (required) and the FILE
// positional argument (one, optional). Invalid values·a missing value·
// unrecognized flags·two or more FILEs are usage errors (exit 3).
func parseImportMigrateArgs(args []string) (format, file string, err error) {
	format = ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--format" || strings.HasPrefix(a, "--format=") {
			if strings.HasPrefix(a, "--format=") {
				format = strings.TrimPrefix(a, "--format=")
			} else {
				if i+1 >= len(args) {
					return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam value required"}
				}
				format = args[i+1]
				i++
			}
			if !migrateValidFormats[format] {
				return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam"}
			}
			if err := ValidateFlagValue("format", format); err != nil {
				return "", "", err
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return "", "", wire.UsageErrorf("unknown flag: %s — usage: flint import --format jsonl|pam [FILE]", a)
		}
		if file != "" {
			return "", "", &store.SchemaError{Field: "file", Expect: "one positional argument (FILE) required"}
		}
		file = a
	}
	if format == "" {
		return "", "", &store.SchemaError{Field: "format", Expect: "jsonl|pam value required"}
	}
	return format, file, nil
}

// importMigrateOutcome — the aggregate result of one run.
type importMigrateOutcome struct {
	imported, duplicated, superseded, remapped, skipped int
	idmap                                               map[int]int // export id → accrued id
	byIndex                                             []int       // input row index → accrued id (0=unresolved)
	validAt                                             map[int]time.Time
}

// importHashSkip — the keys excluded from the re-plant hash input
// (migration machine keys absent from the write payload — id·kind·status
// are imposed at accrual, ingested_at·invalid_at are A4 preservation
// columns. valid_at·ts inherit the HashCanonical volatility exclusion).
// recall_score·last_recalled_at are also excluded — recall history is not
// content identity; score drift must not break hash dedup.
var importHashSkip = map[string]bool{
	"id": true, "kind": true, "status": true, "ingested_at": true, "invalid_at": true,
	"recall_score": true, "last_recalled_at": true,
}

// rowHashRec — converts one ExportRow into a (hash input, accrual rec)
// pair. The hash input is symmetric with the write-path formula
// (ContentHash(HashCanonical(n)) — full key set) — a fixed 7-key subset
// would be asymmetric against rows with write-payload extra keys.
// entities joins into the entity text column with ":" (source-mapping
// inheritance — round-trip symmetry).
func rowHashRec(r ExportRow) (hashInput, rec map[string]any, se *store.SchemaError) {
	rec = map[string]any{
		"kind":       "knowledge",
		"topic":      r.Topic,
		"summary":    r.Content,
		"domain":     r.Domain,
		"confidence": r.Confidence,
		"origin":     r.Source,
	}
	if len(r.Tags) > 0 {
		rec["tags"] = r.Tags
	}
	if len(r.Entities) > 0 {
		rec["entity"] = strings.Join(r.Entities, ":")
	}
	// Time preservation: ingested_at is CreatedAt (the source ingested_at);
	// an invalidated row's invalid_at is UpdatedAt (the reverse mapping of
	// the export-derived COALESCE(invalid_at, ingested_at)). valid_at
	// prefers the raw value — only rows not carrying it in the jsonl
	// (hand-written etc.) inherit the existing fallback (CreatedAt →
	// UpdatedAt).
	if !r.CreatedAt.IsZero() {
		rec["ingested_at"] = r.CreatedAt.UTC()
	}
	switch {
	case !r.ValidAt.IsZero():
		rec["valid_at"] = r.ValidAt.UTC()
	case !r.CreatedAt.IsZero():
		rec["valid_at"] = r.CreatedAt.UTC()
	case !r.UpdatedAt.IsZero():
		rec["valid_at"] = r.UpdatedAt.UTC()
	}
	if r.Status == "invalidated" && !r.UpdatedAt.IsZero() {
		rec["invalid_at"] = r.UpdatedAt.UTC()
	}
	if r.Status != "" {
		rec["status"] = r.Status
	} else {
		rec["status"] = "active"
	}
	// The two score keys (recall_score·last_recalled_at) do not go into the
	// INSERT rec (R42-A — with those two knowledge columns dropped in v12,
	// column assembly would hit 42703·a batch rollback). The values moved
	// to synthetic event replay after the InsertBatch commit (replayRecall
	// — after chunk accrual). Their hash-input exclusion is
	// importHashSkip (comment above). PAM does not extend metadata — the
	// jsonl path owns this.
	if r.ID > 0 {
		rec["id"] = r.ID
	}
	if se := store.Validate("knowledge", rec); se != nil {
		return nil, nil, se
	}
	// Sanitization (ADR-0006): import is an external-file source — exactly
	// like the write pipeline, control characters·[flint] markers are
	// neutralized + secrets scanned after Validate·before hashing (the
	// sanitized result becomes the stored·hashed body — round-trip
	// symmetry preserved).
	for _, w := range SanitizePayload(rec) {
		fmt.Fprintf(os.Stderr, "[flint] import: %s\n", w)
	}
	n := store.Normalize("knowledge", rec)
	hashInput = map[string]any{}
	for k, v := range n {
		if importHashSkip[k] {
			continue
		}
		hashInput[k] = v
	}
	return hashInput, rec, nil
}

// isHex64 — checks the stored content_hash format (64 hex characters).
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// importDedupChunk·importInsertChunk — chunk sizes. dedup is the pre-query
// hash count for content_hash = ANY($1) (1k); insert is the committed row
// count per chunk (5k — spreading the EOF risk of a single huge
// transaction). Tests shrink the boundaries to verify chunking
// (import_jsonl_test.go overrideImportChunks).
var (
	importDedupChunk  = 1000
	importInsertChunk = 5000
)

// importMaxFileBytes — the import input FILE size cap: 512MB. Judged via
// stat before os.ReadFile, blocking oversized documents with exit 3
// before loading them into the slab (large-document OOM defense — a pair
// with the row-count cap). A var only for test boundary shrinking (the
// 512MB default is the contract).
var importMaxFileBytes int64 = 512 << 20

// lookupHashBatch — the dedup pre-query: queries a hash array as
// content_hash = ANY($1) chunks, one round trip each, returning a
// hash→existing-row-meta map (abolishing N per-row SELECTs). A hash
// absent from the map = new. Errors propagate (no silent new-verdict —
// chain preservation keeps a server-down exit 2).
func lookupHashBatch(ctx context.Context, st wire.Store, hashes []string) (map[string]DupMeta, error) {
	existing := make(map[string]DupMeta, len(hashes))
	for start := 0; start < len(hashes); start += importDedupChunk {
		end := min(start+importDedupChunk, len(hashes))
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT content_hash, id, topic, status FROM knowledge WHERE content_hash = ANY($1)`,
			[]any{hashes[start:end]})
		if err != nil {
			return nil, fmt.Errorf("import: dedup lookup failed: %w", err)
		}
		for _, row := range rows {
			if len(row) < 4 {
				continue
			}
			h, _ := row[0].(string)
			id, ok := render.CellToInt(row[1])
			if h == "" || !ok {
				continue
			}
			topic, _ := row[2].(string)
			state, _ := row[3].(string)
			existing[h] = DupMeta{ID: id, Topic: topic, State: state}
		}
	}
	return existing, nil
}

// lookupExistingIDs — the explicit-id conflict pre-query: queries an id
// array as id = ANY($1) chunks, one round trip each, returning the set of
// existing ids (an explicit id of a row that passed hash dedup already
// being in the DB means different content — a PK conflict). Errors
// propagate (same as the dedup lookup — no silent reassignment verdict,
// preserving the server-down exit 2 chain).
func lookupExistingIDs(ctx context.Context, st wire.Store, ids []int) (map[int]bool, error) {
	existing := make(map[int]bool, len(ids))
	for start := 0; start < len(ids); start += importDedupChunk {
		end := min(start+importDedupChunk, len(ids))
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT id FROM knowledge WHERE id = ANY($1)`,
			[]any{ids[start:end]})
		if err != nil {
			return nil, fmt.Errorf("import: id conflict lookup failed: %w", err)
		}
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			if id, ok := render.CellToInt(row[0]); ok && id > 0 {
				existing[id] = true
			}
		}
	}
	return existing, nil
}

// countJSONLRows — the count of non-empty lines: one byte-line scan
// counting under the same row criterion as ParseJSONLRows (blank lines
// excluded) — for early cap judgment before full parsing (parse-memory
// defense for large documents). Lines consisting solely of Unicode
// whitespace are not JSON lines and are not considered.
func countJSONLRows(data []byte) int {
	n := 0
	blank := true
	for _, c := range data {
		switch {
		case c == '\n':
			if !blank {
				n++
			}
			blank = true
		case c == ' ' || c == '\t' || c == '\r':
			// whitespace — keeps the line blank
		default:
			blank = false
		}
	}
	if !blank {
		n++
	}
	return n
}

// importRowsBatch — chunked accrual + id mapping of dedup-passing rows.
// ①A hash-array ANY($1) chunk pre-query instead of per-row lookupHash
// ②Chunked commits of importInsertChunk rows instead of one InsertBatch
// (partial-success preservation — commits before a failing chunk survive,
// and a rerun recovers idempotently via dedup).
func importRowsBatch(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc := &importMigrateOutcome{
		idmap:   map[int]int{},
		byIndex: make([]int, len(rows)),
		validAt: map[int]time.Time{},
	}
	type pending struct {
		idx  int
		row  ExportRow
		rec  map[string]any // produced by rowHashRec (validated·sanitized — unnormalized)
		hash string
	}
	// Stage 1 — validate·sanitize·derive hashes + skip in-file duplicates
	// (row order first).
	var fresh []pending
	seen := map[string]bool{}
	hashes := make([]string, 0, len(rows))
	for i, r := range rows {
		if r.Kind != "" && r.Kind != "knowledge" {
			return nil, &store.SchemaError{Field: "kind", Expect: "only knowledge rows can be re-imported"}
		}
		hashInput, rec, se := rowHashRec(r)
		if se != nil {
			return nil, se
		}
		// Hash: a stored content_hash (64 hex) carried by the export is the
		// value write derived including payload extra keys (session_id
		// etc.), so it cannot be reproduced by recomputation — when valid,
		// inherit it to keep dedup·accrual round-trip symmetric. Rows
		// without one (hand-written jsonl) recompute with the write
		// formula.
		h := store.ContentHash(store.HashCanonical(hashInput))
		if isHex64(r.ContentHash) {
			h = r.ContentHash
		}
		if seen[h] {
			oc.duplicated++
			continue
		}
		seen[h] = true
		hashes = append(hashes, h)
		fresh = append(fresh, pending{idx: i, row: r, rec: rec, hash: h})
	}
	// Stage 2 — the dedup pre-query (ANY($1) chunks — per-row queries
	// abolished).
	existing, err := lookupHashBatch(ctx, st, hashes)
	if err != nil {
		return nil, err
	}
	// Stage 3 — existing rows skip as duplicates (inheriting ids for
	// lineage-reference resolution); only new rows complete their accrual
	// rec (normalization·content_hash·valid_at). Skipped rows with
	// score>0 are collected as stage-4.5 replenishment candidates (R43 —
	// comment below).
	var batch []pending
	var skippedReplays []db.RecallReplay // among dedup-skipped rows, those with score>0 (stage-4.5 replenishment candidates)
	for _, p := range fresh {
		if meta, ok := existing[p.hash]; ok {
			oc.duplicated++
			if meta.ID > 0 {
				oc.idmap[p.row.ID] = meta.ID // for lineage-reference resolution
				oc.byIndex[p.idx] = meta.ID
				// R43 missing-replay replenishment candidate — if a previous
				// run accrued this row (chunk commit) then failed in replay
				// with exit 1, this run's dedup skip drops it from replay
				// collection and the score transfer is permanently lost.
				// Stage 4.5 replenishes only rows with 0 events in the
				// target DB.
				if p.row.RecallScore > 0 {
					rr := db.RecallReplay{ID: meta.ID, Score: p.row.RecallScore}
					if p.row.LastRecalledAt != nil {
						rr.LastRecalled = *p.row.LastRecalledAt
					}
					skippedReplays = append(skippedReplays, rr)
				}
			}
			continue
		}
		rec := store.Normalize("knowledge", p.rec)
		rec["content_hash"] = p.hash // NOT NULL UNIQUE — the same accrual as the write path
		switch v := rec["valid_at"].(type) {
		case string:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				oc.validAt[p.row.ID] = t
			}
		case time.Time:
			oc.validAt[p.row.ID] = v
		}
		batch = append(batch, pending{idx: p.idx, row: p.row, rec: rec, hash: p.hash})
	}
	// Stage 3.5 — automatic reassignment of explicit-id conflicts. An
	// identical id already in the DB (different content — identical content
	// was already skipped by the stage-2~3 dedup) and duplicate ids within
	// the file have their explicit id removed, sending them down the serial
	// reassignment path (reassigned rows are counted as remapped with
	// id≠original id in stage 4·reported in the summary — recovering the
	// main round-trip scenario that used to die wholesale on a batch PK
	// conflict exit 1).
	explicitIDs := make([]int, 0, len(batch))
	for _, p := range batch {
		if id, ok := p.rec["id"].(int); ok && id > 0 {
			explicitIDs = append(explicitIDs, id)
		}
	}
	if len(explicitIDs) > 0 {
		existingIDs, lerr := lookupExistingIDs(ctx, st, explicitIDs)
		if lerr != nil {
			return nil, lerr
		}
		taken := map[int]bool{} // explicit ids occupied by earlier file rows
		for i := range batch {
			id, ok := batch[i].rec["id"].(int)
			if !ok || id <= 0 {
				continue
			}
			if existingIDs[id] || taken[id] {
				delete(batch[i].rec, "id") // automatic reassignment — serial allocation
				continue
			}
			taken[id] = true
		}
	}
	// Stage 4 — chunked accrual+commit: partial success per chunk (commits
	// before a failing chunk survive); id mapping·aggregation keep row
	// order invariant.
	explicit := false
	for start := 0; start < len(batch); start += importInsertChunk {
		var replays []db.RecallReplay // chunk replay items (collected below)
		end := min(start+importInsertChunk, len(batch))
		chunk := batch[start:end]
		recs := make([]map[string]any, 0, len(chunk))
		for _, p := range chunk {
			recs = append(recs, p.rec)
		}
		ids, err := st.InsertBatch(ctx, recs)
		if err != nil {
			// A batch accrual failure can never become a phantom success —
			// the real mapInsertErr wraps unique violations (23505: PK
			// conflicts of existing-DB same-id·different-content rows
			// included) as ErrDuplicate, and MapExit(ErrDuplicate)=0, so
			// leaving it as-is would yield "error JSON + exit 0 + zero-row
			// commit" (write.go's principle forbidding 'discard everything
			// then exit 0 duplicate'). Only server-down (exit 2) keeps the
			// chain; everything else maps to exit 1 with the cause string
			// preserved.
			if errors.Is(err, store.ErrServerDown) {
				return nil, err
			}
			return nil, fmt.Errorf("import: batch apply failed: %v", err)
		}
		for i, p := range chunk {
			id := ids[i]
			oc.imported++
			if p.row.ID > 0 {
				explicit = true
				if id != p.row.ID {
					oc.remapped++ // id not preservable — report the mapping after automatic allocation
				}
			}
			oc.idmap[p.row.ID] = id
			oc.byIndex[p.idx] = id
			if t, ok := oc.validAt[p.row.ID]; ok {
				oc.validAt[id] = t // referenceable under the reassigned id too
			}
			// The score-transfer replay payload (R42-A) — executed right
			// after the chunk commit (below).
			if p.row.RecallScore > 0 {
				rr := db.RecallReplay{ID: id, Score: p.row.RecallScore}
				if p.row.LastRecalledAt != nil {
					rr.LastRecalled = *p.row.LastRecalledAt
				}
				replays = append(replays, rr)
			}
		}
		// Recall-history replay (R42-A) — transfers scores as synthetic
		// events instead of the two dropped columns. Attached to the chunk
		// accrual commit so "accrue+replay" forms one recovery unit (on
		// rerun, dedup skips the committed rows → no double replay — and
		// the score gap of skipped rows is replenished by stage 4.5 below).
		if err := replayRecall(ctx, st, replays); err != nil {
			return nil, err
		}
	}
	// Stage 4.5 — replenishing the replay gap of dedup-skipped rows (R43):
	// if a previous run exited 1 on a replayRecall failure after the chunk
	// commit, this rerun's dedup skip drops that row from stage-4 replay
	// collection and the score transfer is permanently lost. Only rows
	// with 0 events in the target DB's recall_events are picked among
	// score>0 skipped rows for replenishment — normally replayed rows are
	// filtered out by their existing events, so no double replay
	// (restoreSkippedRecallReplay).
	if err := restoreSkippedRecallReplay(ctx, st, skippedReplays); err != nil {
		return nil, err
	}
	if explicit {
		// An explicit-id INSERT does not consume the id serial's nextval.
		syncKnowledgeIDSequence(ctx, st)
	}
	return oc, nil
}

// syncKnowledgeIDSequence — right after an explicit-id migration, aligns
// the id serial nextval to max(id)+1 so the sequence does not collide with
// existing rows and make flint write fail repeatedly (setval …, false —
// the next nextval = max+1). The sequence name is resolved via
// pg_get_serial_sequence; an unsupported store falls back to the standard
// name knowledge_id_seq. Failure is a stderr warning only (exit
// unchanged).
func syncKnowledgeIDSequence(ctx context.Context, st wire.Store) {
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT pg_get_serial_sequence('knowledge', 'id')`, nil); err == nil &&
		len(rows) == 1 && len(rows[0]) == 1 {
		if name, ok := rows[0][0].(string); ok && name != "" {
			if _, err := wire.QueryOnce(ctx, st, fmt.Sprintf(
				`SELECT setval('%s', (SELECT COALESCE(MAX(id), 0) + 1 FROM knowledge), false)`,
				strings.ReplaceAll(name, "'", "''")), nil); err == nil {
				return
			}
		}
	}
	if _, err := wire.QueryOnce(ctx, st,
		`SELECT setval('knowledge_id_seq', (SELECT COALESCE(MAX(id), 0) + 1 FROM knowledge), false)`, nil); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] import: id sequence sync failed (continuing after warning): %v\n", err)
	}
}

// supersedeRefStore — the lineage-reference restoration capability.
// realStore implements it via db.Repo.SetSupersededRef (delegated from
// cli/import_migrate.go). The write SQL has the single db.Repo path
// (writes_through_repo guard) — knowledge consumes it only via the
// capability assertion.
type supersedeRefStore interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
}

// recallReplayStore — the migration score-replay capability (R42-A).
// realStore implements it via db.Repo.ReplayRecallEvents. The write SQL
// has the single db.Repo path — knowledge consumes it only via the
// capability assertion (the supersedeRefStore convention).
type recallReplayStore interface {
	ReplayRecallEvents(ctx context.Context, items []db.RecallReplay) error
}

// replayRecall — migration score replay (R42-A): runs chunk-by-chunk
// right after the InsertBatch commit. In v12 the jsonl's recall_score·
// last_recalled_at are dropped knowledge columns that cannot go into the
// INSERT as columns; the transfer goes through synthetic events
// (recall_events) with the same formula as the EnsureSchemaV12 backfill.
// Errors follow the InsertBatch contract (ErrServerDown chain preserved·
// everything else an exit 1 wrap — the accrued portion is already
// committed, a rerun recovers via dedup, and no double replay: committed
// rows are duplicated-skipped and drop out of replay collection). A
// non-implementing store (fake etc.) gets a stderr warning then a skip
// (the restoreSupersede convention).
func replayRecall(ctx context.Context, st wire.Store, replays []db.RecallReplay) error {
	if len(replays) == 0 {
		return nil
	}
	rs, ok := st.(recallReplayStore)
	if !ok {
		fmt.Fprintf(os.Stderr, "[flint] import: recall replay unsupported by store — skipped (%d rows)\n", len(replays))
		return nil
	}
	if err := rs.ReplayRecallEvents(ctx, replays); err != nil {
		if errors.Is(err, store.ErrServerDown) {
			return err
		}
		return fmt.Errorf("import: recall replay failed: %v", err)
	}
	return nil
}

// restoreSkippedRecallReplay — replenishes the replay gap of
// dedup-skipped rows (R43): candidates are the replay items captured at
// the stage-3 skip (inherited id·score·last). The ids holding events in
// the target DB's recall_events are fetched with one DISTINCT round trip
// (inheriting the dedup chunk convention), and only candidates with 0
// events are replenished via replayRecall — rows already replayed in a
// previous run are filtered out (no double replay), and even after a
// partial replay across a chunk boundary, a rerun picks only the
// unreplenished rows, staying idempotent. The error contract matches
// replayRecall (ErrServerDown chain preserved·everything else an exit 1
// wrap).
func restoreSkippedRecallReplay(ctx context.Context, st wire.Store, candidates []db.RecallReplay) error {
	if len(candidates) == 0 {
		return nil
	}
	ids := make([]int, 0, len(candidates))
	for _, c := range candidates {
		if c.ID > 0 {
			ids = append(ids, c.ID)
		}
	}
	hasEvent := make(map[int]bool, len(ids))
	for start := 0; start < len(ids); start += importDedupChunk {
		end := min(start+importDedupChunk, len(ids))
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT DISTINCT knowledge_id FROM recall_events WHERE knowledge_id = ANY($1)`,
			[]any{ids[start:end]})
		if err != nil {
			if errors.Is(err, store.ErrServerDown) {
				return err
			}
			return fmt.Errorf("import: replay restore lookup failed: %v", err)
		}
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			if id, ok := render.CellToInt(row[0]); ok && id > 0 {
				hasEvent[id] = true
			}
		}
	}
	var replays []db.RecallReplay
	for _, c := range candidates {
		if c.ID > 0 && !hasEvent[c.ID] {
			replays = append(replays, c)
		}
	}
	return replayRecall(ctx, st, replays)
}

// restoreSupersede — the lineage-restoration post-pass: updates rows with
// superseded_by references via the existing Supersede transition (after
// from→to id mapping). When the transition is impossible (an already
// invalidated snapshot restoration etc. yielding ErrNotFound),
// SetSupersededRef restores the reference only; a store with neither gets
// a warning then a skip (direct write SQL forbidden).
func restoreSupersede(ctx context.Context, st wire.Store, oc *importMigrateOutcome, refs [][2]int) (int, error) {
	n := 0
	for _, pair := range refs {
		oldID, newID := pair[0], pair[1]
		at, ok := oc.validAt[newID]
		if !ok {
			at = time.Now().UTC()
		}
		done := false
		if ss, ok := st.(supersedeStore); ok {
			_, err := ss.Supersede(ctx, oldID, newID, at)
			if err != nil {
				if !errors.Is(err, db.ErrNotFound) {
					return n, err
				}
			} else {
				n++
				done = true
			}
		}
		if !done {
			if rs, ok := st.(supersedeRefStore); ok {
				rows, err := rs.SetSupersededRef(ctx, oldID, newID)
				if err != nil {
					return n, err
				}
				if rows > 0 {
					n++ // count only rows actually restored (0 rows = existing reference, excluded)
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "[flint] import: id=%d lineage restore unsupported by store — skipped (→ %d)\n", oldID, newID)
		}
	}
	return n, nil
}

// resolveRef — export id → the actually accrued id (in-batch mapping →
// existing-row check).
func resolveRef(ctx context.Context, st wire.Store, oc *importMigrateOutcome, id int) (int, bool) {
	if id <= 0 {
		return 0, false
	}
	if got, ok := oc.idmap[id]; ok {
		return got, true
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT id FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 {
		return 0, false
	}
	got := 0
	switch v := rows[0][0].(type) {
	case int:
		got = v
	case int16:
		got = int(v)
	case int32:
		got = int(v)
	case int64:
		got = int(v)
	default:
		if _, perr := fmt.Sscan(fmt.Sprint(v), &got); perr != nil {
			return 0, false
		}
	}
	if got <= 0 {
		return 0, false
	}
	oc.idmap[id] = got
	return got, true
}

// importJSONL — the jsonl re-planting body (rows already parsed).
func importJSONL(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc, err := importRowsBatch(ctx, st, rows)
	if err != nil {
		return nil, err
	}
	var refs [][2]int
	for _, r := range rows {
		if r.SupersededBy == nil {
			continue
		}
		newID, ok := resolveRef(ctx, st, oc, *r.SupersededBy)
		if !ok {
			fmt.Fprintf(os.Stderr, "[flint] import jsonl: id=%d superseded_by=%d target missing — lineage skipped\n", r.ID, *r.SupersededBy)
			continue
		}
		oldID, ok := resolveRef(ctx, st, oc, r.ID)
		if !ok {
			continue
		}
		refs = append(refs, [2]int{oldID, newID})
	}
	if len(refs) > 0 {
		k, err := restoreSupersede(ctx, st, oc, refs)
		if err != nil {
			return nil, err
		}
		oc.superseded = k
	}
	return oc, nil
}

// importPAM — pending accrual of PAM candidates (following the promotion
// model — inheriting the approve path) + relations supersedes lineage
// restoration. topic derives from the PAM memory id (the import.go
// "rule[heading]" convention).
func importPAM(ctx context.Context, st wire.Store, res *PAMResult) (*importMigrateOutcome, error) {
	var rows []ExportRow
	skipped := len(res.Skipped)
	for _, c := range res.Candidates {
		if c.Kind != "knowledge" && c.Kind != "project" {
			skipped++
			fmt.Fprintf(os.Stderr, "[flint] import pam: non-knowledge/project candidate skipped (id=%s kind=%s)\n", c.ID, c.Kind)
			continue
		}
		row := ExportRow{
			Topic:      render.Text(render.TruncateRunes("pam["+c.ID+"]", 80)),
			Content:    c.Content,
			Kind:       "knowledge", // the storage-router constant — not the projects table (routing ruling 2026-11-14)
			Status:     "pending",
			Confidence: c.Confidence,
			Tags:       c.Tags,
			Source:     c.Source,
		}
		// Round-trip idempotence: a stored content_hash carried by a
		// flint-issued document (inherited by ParsePAM — 64 hex only) is
		// inherited as the accrual hash so dedup is symmetric with the
		// source hash (the same as the jsonl A9 isHex64 path).
		if h, ok := c.Metadata["content_hash"].(string); ok && isHex64(h) {
			row.ContentHash = h
		}
		if c.Kind == "project" {
			// Source-kind preservation: in a knowledge-table pending
			// accrual (promotion model), the original shape is
			// distinguishable only by tag — append "pam:project"
			// (round-trip·search paths).
			row.Tags = append(append([]string{}, c.Tags...), "pam:project")
		}
		rows = append(rows, row)
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(os.Stderr, "[flint] import pam: skipped (id=%s status=%s)\n", s.ID, s.Status)
	}
	oc, err := importRowsBatch(ctx, st, rows)
	if err != nil {
		return nil, err
	}
	oc.skipped = skipped
	// Connect candidate PAM ids → accrued ids (row index — duplicates go
	// through the lookupHash meta).
	pamID := map[string]int{}
	idx := 0
	for _, c := range res.Candidates {
		if c.Kind != "knowledge" && c.Kind != "project" {
			continue
		}
		if idx < len(oc.byIndex) {
			pamID[c.ID] = oc.byIndex[idx]
		}
		idx++
	}
	var refs [][2]int
	for _, p := range res.SupersedePairs {
		// PAM §13.2 "supersedes: Source replaces target" (the
		// import_pam.go SupersedePair contract): From=new (the replacing
		// source), To=old (the replaced) — Supersede(oldID=old To's accrued
		// id, newID=new From's accrued id).
		newID, okFrom := pamID[p.From]
		oldID, okTo := pamID[p.To]
		if !okFrom || oldID <= 0 || !okTo || newID <= 0 {
			fmt.Fprintf(os.Stderr, "[flint] import pam: supersedes(%s→%s) target missing — lineage skipped\n", p.From, p.To)
			continue
		}
		refs = append(refs, [2]int{oldID, newID})
	}
	if len(refs) > 0 {
		k, err := restoreSupersede(ctx, st, oc, refs)
		if err != nil {
			return nil, err
		}
		oc.superseded = k
	}
	return oc, nil
}

// RunImportRouted — the "import" command routing (v1.3.0 T9): with
// --format, the migration path; without, the existing rule conversion
// RunImport (unchanged). Registration is the one-line delegation from
// knowledge.go to this handler.
func RunImportRouted(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for _, a := range args {
		if a == "--format" || strings.HasPrefix(a, "--format=") {
			return RunImportMigrate(ctx, st, args, stdin, stdout)
		}
	}
	return RunImport(ctx, st, args, stdin, stdout)
}

// importMaxRowsLimit — loads the per-import row-count cap (the registry
// key export_import_max_rows). Lookup failure·unset (0) fall back to
// DefaultThresholds (the key convention — consumer completeness for keys
// not registered in ApplyThresholdsFallback).
func importMaxRowsLimit(ctx context.Context, st wire.Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

// enforceImportMaxRows — on exceeding the row-count cap, prints an error
// JSON (with max·count) to stdout and returns 3 (the caller returns it
// as-is). The stage is right after parsing·before accrual, so no side
// effects (memory defense against accruing a large document in full).
func enforceImportMaxRows(ctx context.Context, st wire.Store, stdout io.Writer, count int) int {
	max := importMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("import row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"import row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
}

// RunImportMigrate — `flint import --format jsonl|pam [FILE]` (without
// FILE, stdin). Invalid format·corrupt jsonl exit 3; server-down exit 2
// (reusing MapExit).
func RunImportMigrate(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	format, file, err := parseImportMigrateArgs(args)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	var data []byte
	if file != "" {
		// The size cap is judged via stat before os.ReadFile — an
		// oversized file exits 3 unread (with max·size). A stat failure is
		// left to the ReadFile error path (A7 cause preservation).
		if fi, serr := os.Stat(file); serr == nil && fi.Size() > importMaxFileBytes {
			b, merr := json.Marshal(map[string]any{
				"error": fmt.Sprintf("import file size limit exceeded (%d bytes max)", importMaxFileBytes),
				"max":   importMaxFileBytes,
				"size":  fi.Size(),
			})
			if merr != nil {
				b = []byte(fmt.Sprintf(`{"error":"import file size limit exceeded","max":%d,"size":%d}`,
					importMaxFileBytes, fi.Size()))
			}
			fmt.Fprintln(stdout, string(b))
			return 3
		}
		data, err = os.ReadFile(file)
		if err != nil {
			// The cause (ENOENT/EACCES·path) is not discarded but included
			// in expect (keeping the SchemaError wrap — exit 3 unchanged).
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "file",
				Expect: "readable jsonl file: " + err.Error()}))
			return 3
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "stdin",
				Expect: "readable: " + err.Error()}))
			return 3
		}
	}
	var oc *importMigrateOutcome
	switch format {
	case "jsonl":
		// The cap check is moved ahead of full parsing — an early exit 3
		// from one line-scan count (parse-memory defense for large
		// documents takes priority over the corrupt-row error).
		if code := enforceImportMaxRows(ctx, st, stdout, countJSONLRows(data)); code != 0 {
			return code
		}
		rows, perr := ParseJSONLRows(data)
		if perr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(perr))
			return 3
		}
		oc, err = importJSONL(ctx, st, rows)
	case "pam":
		if ParsePAMFn == nil { // seam not ready
			fmt.Fprintln(stdout, `{"error":"pam import unsupported"}`)
			return 3
		}
		var res *PAMResult
		res, err = ParsePAMFn(data)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 3
		}
		// The cap counts candidates+skips together (by the document's
		// total memory rows).
		if code := enforceImportMaxRows(ctx, st, stdout, len(res.Candidates)+len(res.Skipped)); code != 0 {
			return code
		}
		oc, err = importPAM(ctx, st, res)
	}
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if oc.imported > 0 || oc.superseded > 0 {
		if _, cerr := st.Commit(ctx, fmt.Sprintf(
			"import --format %s: imported=%d duplicated=%d superseded=%d [session:0]",
			format, oc.imported, oc.duplicated, oc.superseded)); cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
	}
	fmt.Fprintln(stdout, importMigrateSummary(oc.imported, oc.duplicated, oc.superseded, oc.remapped, oc.skipped))
	return 0
}
