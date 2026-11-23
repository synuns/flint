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
		rows = append(rows, ExportRow(v))
	}
	return rows, nil
}

func importMigrateSummary(imported, duplicated, superseded, remapped, skipped, links int) string {
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
	if links > 0 {
		m["links"] = links
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf(`{"status":"ok","imported":%d,"duplicated":%d,"superseded":%d}`,
			imported, duplicated, superseded)
	}
	return string(b)
}

var migrateValidFormats = map[string]bool{"jsonl": true, "pam": true}

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

type importMigrateOutcome struct {
	imported, duplicated, superseded, remapped, skipped int
	links                                               int
	idmap                                               map[int]int
	byIndex                                             []int
	validAt                                             map[int]time.Time
}

var importHashSkip = map[string]bool{
	"id": true, "kind": true, "status": true, "ingested_at": true, "invalid_at": true,
	"recall_score": true, "last_recalled_at": true,
}

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

	if r.ID > 0 {
		rec["id"] = r.ID
	}
	if se := store.Validate("knowledge", rec); se != nil {
		return nil, nil, se
	}

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

var (
	importDedupChunk  = 1000
	importInsertChunk = 5000
)

var importMaxFileBytes int64 = 512 << 20

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

func lookupExistingIDs(ctx context.Context, st wire.Store, ids []int) (map[int]bool, error) {
	existing := make(map[int]bool, len(ids))
	for start := 0; start < len(ids); start += importDedupChunk {
		end := min(start+importDedupChunk, len(ids))
		i64 := make([]int64, 0, end-start)
		for _, id := range ids[start:end] {
			i64 = append(i64, int64(id))
		}
		rows, err := wire.QueryOnce(ctx, st,
			`SELECT id FROM knowledge WHERE id = ANY($1::bigint[])`,
			[]any{i64})
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

		default:
			blank = false
		}
	}
	if !blank {
		n++
	}
	return n
}

func importRowsBatch(ctx context.Context, st wire.Store, rows []ExportRow) (*importMigrateOutcome, error) {
	oc := &importMigrateOutcome{
		idmap:   map[int]int{},
		byIndex: make([]int, len(rows)),
		validAt: map[int]time.Time{},
	}
	type pending struct {
		idx  int
		row  ExportRow
		rec  map[string]any
		hash string
	}

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

	existing, err := lookupHashBatch(ctx, st, hashes)
	if err != nil {
		return nil, err
	}

	var batch []pending
	var skippedReplays []db.RecallReplay
	for _, p := range fresh {
		if meta, ok := existing[p.hash]; ok {
			oc.duplicated++
			if meta.ID > 0 {
				oc.idmap[p.row.ID] = meta.ID
				oc.byIndex[p.idx] = meta.ID

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
		rec["content_hash"] = p.hash
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
		taken := map[int]bool{}
		for i := range batch {
			id, ok := batch[i].rec["id"].(int)
			if !ok || id <= 0 {
				continue
			}
			if existingIDs[id] || taken[id] {
				delete(batch[i].rec, "id")
				continue
			}
			taken[id] = true
		}
	}

	explicit := false
	for start := 0; start < len(batch); start += importInsertChunk {
		var replays []db.RecallReplay
		end := min(start+importInsertChunk, len(batch))
		chunk := batch[start:end]
		recs := make([]map[string]any, 0, len(chunk))
		for _, p := range chunk {
			recs = append(recs, p.rec)
		}
		ids, err := st.InsertBatch(ctx, recs)
		if err != nil {

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
					oc.remapped++
				}
			}
			oc.idmap[p.row.ID] = id
			oc.byIndex[p.idx] = id
			if t, ok := oc.validAt[p.row.ID]; ok {
				oc.validAt[id] = t
			}

			if p.row.RecallScore > 0 {
				rr := db.RecallReplay{ID: id, Score: p.row.RecallScore}
				if p.row.LastRecalledAt != nil {
					rr.LastRecalled = *p.row.LastRecalledAt
				}
				replays = append(replays, rr)
			}
		}

		if err := replayRecall(ctx, st, replays); err != nil {
			return nil, err
		}
	}

	if err := restoreSkippedRecallReplay(ctx, st, skippedReplays); err != nil {
		return nil, err
	}
	if explicit {

		syncKnowledgeIDSequence(ctx, st)
	}
	return oc, nil
}

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

type supersedeRefStore interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
}

type recallReplayStore interface {
	ReplayRecallEvents(ctx context.Context, items []db.RecallReplay) error
}

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

			if cycRows, cerr := wire.QueryOnce(ctx, st, db.SupersedeCycleSQL,
				[]any{oldID, newID, db.SupersedeCycleDepthLimit}); cerr == nil && len(cycRows) > 0 {
				if cyc, _ := cycRows[0][0].(bool); cyc {
					return n, fmt.Errorf(
						"import: id=%d -> id=%d would create a supersede cycle", oldID, newID)
				}
				if truncated, _ := cycRows[0][1].(bool); truncated {
					return n, fmt.Errorf(
						"import: id=%d -> id=%d lineage walk hit the depth cap — refusing conservatively", oldID, newID)
				}
			}
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
					n++
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "[flint] import: id=%d lineage restore unsupported by store — skipped (→ %d)\n", oldID, newID)
		}
	}
	return n, nil
}

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
			Kind:       "knowledge",
			Status:     "pending",
			Confidence: c.Confidence,
			Tags:       c.Tags,
			Source:     c.Source,
		}

		if c.CreatedAt != "" {
			if t, perr := time.Parse(time.RFC3339Nano, c.CreatedAt); perr == nil {
				row.CreatedAt = t
			}
		}

		if h, ok := c.Metadata["content_hash"].(string); ok && isHex64(h) {
			row.ContentHash = h
		}
		if c.Kind == "project" {

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

	if len(res.Links) > 0 {
		applied, err := RestorePAMLinks(ctx, st, func(pid string) (int, bool) {
			id, ok := pamID[pid]
			return id, ok && id > 0
		}, res.Links)
		if err != nil {
			return nil, err
		}
		oc.links = applied
	}
	return oc, nil
}

func RunImportRouted(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for _, a := range args {
		if a == "--format" || strings.HasPrefix(a, "--format=") {
			return RunImportMigrate(ctx, st, args, stdin, stdout)
		}
	}
	return RunImport(ctx, st, args, stdin, stdout)
}

func importMaxRowsLimit(ctx context.Context, st wire.Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

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
		if ParsePAMFn == nil {
			fmt.Fprintln(stdout, `{"error":"pam import unsupported"}`)
			return 3
		}
		var res *PAMResult
		res, err = ParsePAMFn(data)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 3
		}

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
	fmt.Fprintln(stdout, importMigrateSummary(oc.imported, oc.duplicated, oc.superseded, oc.remapped, oc.skipped, oc.links))
	return 0
}
