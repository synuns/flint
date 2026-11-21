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
	"os"
	"sort"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
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

// rejectedTagPrefix — the store.CheckVocabulary rejection message prefix
// (the policy.go colon guard: vol:* exists only as dictionary-registered
// tags, so a user token containing ':' is refused). Matched by prefix —
// the store package owns the exact wording; the plain unknown-tag warning
// class has no prefix check (stays non-blocking).
const rejectedTagPrefix = "rejected tag: "

// sourceRegistry — the single source of truth of the allowed metering
// sources (spec §1: hook via hook · human/default manual · MCP mcp ·
// v1.11.0 correction · v1.12.0 gap — recall-failure gap candidates, a
// machine channel whose payload T3 accrues; no write-time defaults, the
// gap-candidate tag is attached by the candidate payload itself). The
// usage hint·the ErrInvalidSource message·the membership check are all
// derived from this registry (no duplicate hardcoded lists).
var sourceRegistry = []string{"hook", "manual", "mcp", "correction", "gap"}

// sourceList — the pipe-joined display form of the registry
// (hook|manual|mcp|correction|gap) shared by usage·error messages.
func sourceList() string { return strings.Join(sourceRegistry, "|") }

// ErrInvalidSource — an invalid --source value (exit 3). The allowed list
// is derived from sourceRegistry (registry-drift breaks the guard test).
var ErrInvalidSource = fmt.Errorf("write: invalid --source (%s)", sourceList())

// validSources — the membership view derived from sourceRegistry
// (1:1 — the derivation guard test enforces sync).
var validSources = func() map[string]bool {
	m := make(map[string]bool, len(sourceRegistry))
	for _, s := range sourceRegistry {
		m[s] = true
	}
	return m
}()

// correctionTodayCount — today's committed correction count (the
// daily:correction_write:<UTC date> flat key — the BumpDailyFlatKey
// convention; PruneDaily cleans daily:* beyond the retention window). A
// lookup·parse failure is a quiet 0 (§13 — the cap never breaks the
// write).
func correctionTodayCount(ctx context.Context, st wire.Store) int {
	key := "daily:correction_write:" + time.Now().UTC().Format("2006-01-02")
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}

// applyCorrectionDefaults — the --source correction payload defaults
// (v1.11.0 T2, knowledge only): a missing confidence gets def (the
// correction_confidence threshold — default 0.7, below conf_floor so a
// correction lands pending for review); the "correction" tag is merged
// (an absent tags becomes ["correction"]; an existing list is appended
// unless already present — idempotent). An explicitly given confidence
// (including 0) is respected — the manual contract unchanged.
func applyCorrectionDefaults(p map[string]any, def float64) {
	if _, ok := p["confidence"]; !ok {
		p["confidence"] = def
	}
	switch tags := p["tags"].(type) {
	case []any:
		for _, t := range tags {
			if s, ok := t.(string); ok && s == "correction" {
				return
			}
		}
		p["tags"] = append(tags, "correction")
	case []string:
		for _, s := range tags {
			if s == "correction" {
				return
			}
		}
		p["tags"] = append(tags, "correction")
	default:
		p["tags"] = []any{"correction"}
	}
}

// ── v1.12.0 T3 — the gap-candidate accrual (the knowledge pipeline
// step 2): a recall-failure count reaching gap_query_threshold accrues
// one pending candidate on the `gap` channel. ──

// gapCandidateConfidence — the fixed payload confidence of a gap
// candidate (spec): below conf_floor, so a candidate always lands
// pending for review (the promotion gate — a Letta-style self-
// reinforcement trap is blocked by the explicit approve).
const gapCandidateConfidence = 0.4

// gapCandidateTag — the candidate tag (source/channel distinction is
// metering-only; the tag is the row-level filter surface).
const gapCandidateTag = "gap-candidate"

// gapCandidateTopicPrefix — the topic prefix of a candidate row.
const gapCandidateTopicPrefix = "격차 후보: "

// gapCandidateTopicRunes — the normalized-query truncation bound of the
// candidate topic (the original-text exposure happens only here — the
// truncated·sanitized·normalized form, the non-persistence principle).
const gapCandidateTopicRunes = 60

// gapCandidateSummaryFmt — the summary template (N = the same-day
// normalized-query failure count at accrual time).
const gapCandidateSummaryFmt = "회상 실패 %d회 — 지식 격차 후보"

// gapWriteDailyKind — the daily flat-key kind counting committed
// candidate ADDs (the gap_daily_max cap source — new ADDs only, updates
// never consume it, the correction convention).
const gapWriteDailyKind = "gap_write"

// pendingSummaryStore — the pending-row summary refresh seam (the T3
// UPDATE). realStore delegates to db.UpdatePendingSummary; a
// non-implementing Store quietly skips the refresh (the NOOP fallback).
// The returned rows count lets the caller commit only on an applied
// update (a 0-row NOOP — an active·raced row — makes no empty commit).
type pendingSummaryStore interface {
	UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error)
}

// gapCandidateHash — the N-invariant content digest of a normalized
// query's candidate: the hash input is the topic·tag·confidence shape
// with the summary omitted (the N lives only in the stored summary — a
// later accrual at another N must find the same row for succession).
func gapCandidateHash(normalized string) string {
	topic := gapCandidateTopicPrefix + render.TruncateRunes(normalized, gapCandidateTopicRunes)
	n := store.Normalize("knowledge", map[string]any{
		"topic":      topic,
		"tags":       []any{gapCandidateTag},
		"confidence": gapCandidateConfidence,
	})
	return store.ContentHash(store.HashCanonical(n))
}

// gapTodayCount — today's committed candidate ADD count (the
// daily:gap_write:<UTC date> flat key — the correctionTodayCount
// convention). A lookup·parse failure is a quiet 0 (§13).
func gapTodayCount(ctx context.Context, st wire.Store) int {
	key := "daily:" + gapWriteDailyKind + ":" + time.Now().UTC().Format("2006-01-02")
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}

// maybeAccrueGapCandidate — attempts one gap-candidate accrual when the
// count has reached gap_query_threshold (the read completion path calls
// this quietly after BumpGapQuery — §13: an accrual failure never breaks
// the read). Threshold·cap load via LoadThresholds (meta overrides); an
// unset/zero value falls back to the registered defaults (the 0-fallback
// clamp). Dedup: the N-invariant content_hash succeeds — an existing
// pending row gets its summary N refreshed (an UPDATE, not a supersede);
// an active row is a NOOP (the promotion-fixed integrity ruling); no
// row ADDs one pending candidate (the gap_daily_max cap gates ADDs only
// — a capped accrual is quietly skipped with one stderr warning line).
func maybeAccrueGapCandidate(ctx context.Context, st wire.Store, sanitizedQuery string, count int64) {
	th := WriteThresholds(ctx, st)
	def := store.DefaultThresholds()
	threshold := th.GapQueryThreshold
	if threshold <= 0 {
		threshold = def.GapQueryThreshold
	}
	if count < int64(threshold) {
		return
	}
	normalized := NormalizeGapQuery(sanitizedQuery)
	topic := gapCandidateTopicPrefix + render.TruncateRunes(normalized, gapCandidateTopicRunes)
	summary := fmt.Sprintf(gapCandidateSummaryFmt, count)
	hash := gapCandidateHash(normalized)
	meta, exists, err := lookupHash(ctx, st, hash)
	if err != nil {
		return // quiet §13 — the next bump retries
	}
	if exists {
		// The pending refresh (summary N) — an UPDATE via the seam; the
		// active·invalidated states are NOOPs (integrity ruling).
		if meta.State == "pending" {
			if ps, ok := st.(pendingSummaryStore); ok {
				if n, err := ps.UpdatePendingSummary(ctx, meta.ID, summary); err == nil && n > 0 {
					if _, cerr := st.Commit(ctx, "knowledge: gap candidate refresh"); cerr != nil {
						fmt.Fprintf(os.Stderr, "[flint] gap candidate refresh commit: %v\n", cerr)
					}
				}
			}
		}
		return
	}
	max := th.GapDailyMax
	if max <= 0 {
		max = def.GapDailyMax
	}
	if gapTodayCount(ctx, st) >= max {
		fmt.Fprintf(os.Stderr, "[flint] gap daily max reached (gap_daily_max=%d) — candidate skipped\n", max)
		return
	}
	rec := store.Normalize("knowledge", map[string]any{
		"topic":      topic,
		"summary":    summary,
		"tags":       []any{gapCandidateTag},
		"confidence": gapCandidateConfidence,
	})
	rec["kind"] = "knowledge"
	rec["content_hash"] = hash
	rec["status"] = "pending"
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		return // quiet — a concurrent insert (race) succeeds; the next bump refreshes
	}
	_ = db.BumpDailyFlatKey(ctx, st, gapWriteDailyKind, time.Now().UTC().Format("2006-01-02"), 1)
	RecordUsage(ctx, st, "write", "gap")
	if _, err := st.Commit(ctx, "knowledge: gap candidate (recall failure)"); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] gap candidate commit: %v\n", err)
	}
}

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
			fmt.Fprintf(stdout, "usage: flint write <kind> [--source %s] [--session ID] — kind: knowledge|session|project, payload: JSON/JSONL on stdin\n", sourceList())
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
	// v1.11.0 T3 — the correction daily cap: correction lines still
	// accrualable today (correction_daily_max minus the committed count in
	// the daily:correction_write:<date> flat key). -1 = unlimited (not a
	// correction knowledge write). The quota is consumed only by lines that
	// reach accrual (finalizeRec) — duplicate-skipped lines never burn it;
	// a count lookup failure is a quiet 0 (§13 — the cap never breaks the
	// write).
	correctionQuota := -1
	if source == "correction" && kind == "knowledge" {
		correctionQuota = th.CorrectionDailyMax - correctionTodayCount(ctx, st)
		if correctionQuota < 0 {
			correctionQuota = 0
		}
	}
	capWarned := false
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
	// tagging·non-column key cleanup·recs accrual. The correction daily
	// cap gates here (single choke point covering the single-shot·batched
	// accrual paths alike): a line past the quota is quietly skipped with
	// one warning line (exit 0 — independent of the dup mechanism).
	finalizeRec := func(n map[string]any) {
		if correctionQuota == 0 {
			if !capWarned {
				warnings = append(warnings, fmt.Sprintf(
					"correction daily max reached (correction_daily_max=%d) — line skipped", th.CorrectionDailyMax))
				capWarned = true
			}
			return
		}
		if correctionQuota > 0 {
			correctionQuota--
		}
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
		// v1.11.0 T2 — the --source correction defaults (knowledge only —
		// the sole kind with confidence·tags columns): a missing confidence
		// gets the correction_confidence threshold default·the "correction"
		// tag is merged in. These two defaults are the whole correction
		// branch — validate→sanitize→hash dedup→classify→approve circuits
		// are reused unchanged below.
		if source == "correction" && kind == "knowledge" {
			applyCorrectionDefaults(p, th.CorrectionConfidence)
		}
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
			// v1.13.0 T2 guard wiring: the colon-token rejection class
			// (store.CheckVocabulary "rejected tag:" — vol:* is
			// dictionary-registered only) hard-blocks the write at exit 3
			// (the schema-error channel); the plain "unknown tag:" warning
			// class stays non-blocking.
			if raw, ok := p["tags"].([]any); ok {
				tags := make([]string, 0, len(raw))
				for _, tag := range raw {
					if s, ok := tag.(string); ok {
						tags = append(tags, s)
					}
				}
				domain, _ := n["domain"].(string)
				for _, w := range store.CheckVocabulary(tags, domain) {
					if strings.HasPrefix(w, rejectedTagPrefix) {
						fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tags", Expect: w}))
						return 3
					}
					warnings = append(warnings, w)
				}
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
	// The correction daily-cap count — bumped only by lines that actually
	// committed (the cap's count source, independent of the dup metering).
	if source == "correction" && kind == "knowledge" && len(recs) > 0 {
		_ = db.BumpDailyFlatKey(ctx, st, "correction_write", time.Now().UTC().Format("2006-01-02"), len(recs))
	}
	fmt.Fprintln(stdout, out)
	return 0
}

// RecordDups — meters dup intake by the number of skipped duplicate lines
// (0 is a no-op). The total "dup" key is invariant (ruling); per-source
// "dup_"+source (hook·manual·mcp·correction·gap) is additionally incremented (additive —
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
