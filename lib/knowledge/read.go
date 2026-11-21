// read.go — flint read [query]: forced query on the active_knowledge view +
// token budget. The as-of spec parser is cli asof.go ownership, wired
// through the injection seam (ParseAsOfSpecFn·ErrInvalidAsOf) — cli init
// injects the real implementation (cycle prevention: knowledge cannot
// import cli).
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseAsOfSpecFn — the injection point for as-of spec → commit hash
// normalization (cli asof.go ParseAsOfSpec). cli init (knowledge_alias.go)
// injects the real implementation.
var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

// ErrInvalidAsOf — the injection point for the errors.Is source of the
// invalid as-of spec verdict (cli init injects the same instance as cli
// asof.go ErrInvalidAsOf).
var ErrInvalidAsOf error

// readBumper — the item reference metering seam (BumpReads).
type readBumper interface {
	BumpReads(ctx context.Context, ids []int) error
}

// ReadEventStore — the interface for recording one effectiveness-metering
// event row (v0.19.0, docs/METRICS.md §4). realStore implements it via
// db.Repo.RecordReadEvent (cli/realstore_read.go); a non-implementing Store
// (fake) skips this metering (unlike the counter fallback there is no
// alternate path — without read_events the remaining metrics still derive
// from daily·knowledge). v0.20.0: ids (returned knowledge id CSV, top 10)
// added — the source for usage-chain computation. v0.42.0 T3: scattered
// argument calls converged into one db.ReadEvent struct value (behavior
// unchanged).
type ReadEventStore interface {
	RecordReadEvent(ctx context.Context, ev db.ReadEvent) error
}

// BytesRecorder — the G5 byte-metering interface (v1.1.0, spec §1).
// realStore implements it via db.Repo.RecordBytes (cli/realstore_read.go);
// a non-implementing Store (fake) skips this metering (inheriting the
// ReadEventStore consumption pattern).
type BytesRecorder interface {
	RecordBytes(ctx context.Context, kind string, n int) error
}

// KnowledgeReadsRecorder — the interface for recording per-item recall
// history (v1.2.0 T2, spec §2.1 G4 alignment·§2.6 recall fate). realStore
// implements it via db.Repo.RecordKnowledgeReads; a non-implementing Store
// (fake) skips this metering (inheriting the ReadEventStore consumption
// pattern). Leaves the full read-returned id set (kind=read)·the full hot
// injection/headline-exposed id set (kind=hot) in the knowledge_reads
// table — a history layer separate from the read_count cache (BumpReads).
type KnowledgeReadsRecorder interface {
	RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error
}

// RecallBumper — the recall reinforcement interface (v1.7.0 T2). realStore
// implements it via db.Repo.BumpRecallScores (since v1.9.0 T3 an accrual of
// schema v12 recall_events INSERTs — the v11 knowledge.recall_score·
// last_recalled_at column increment is abolished); a non-implementing Store
// (fake) quietly skips it (inheriting the readBumper consumption pattern).
type RecallBumper interface {
	BumpRecallScores(ctx context.Context, ids []int) error
}

// recordKnowledgeReads — recall history recording (metering — failures are
// quiet, §13). kind=read (recall-return hits) couples to reinforcement on
// this path (accruing recall events into recall_events) as well. kind=hot
// (injection exposure) is excluded from reinforcement — it would create an
// exposure→accrual→re-exposure cycle (the recall-event source is actual
// recall-return hits only). The history recording itself is invariant on
// both sides.
func recordKnowledgeReads(ctx context.Context, st wire.Store, ids []int, session, kind string) {
	if kr, ok := st.(KnowledgeReadsRecorder); ok {
		_ = kr.RecordKnowledgeReads(ctx, ids, session, kind)
	}
	if kind == "read" {
		if rb, ok := st.(RecallBumper); ok {
			_ = rb.BumpRecallScores(ctx, ids)
		}
	}
}

// TelemetryCommitter — the interface for the separate metering-table commit
// (v1.7.0 T6). realStore implements it via db.Repo.CommitTelemetry
// (cli/realstore_read.go); a non-implementing Store (fake) skips this
// commit (inheriting the ReadEventStore consumption pattern). Independent
// of the audit commit (Commit) — the read/hot/done completion paths call it
// once.
type TelemetryCommitter interface {
	CommitTelemetry(ctx context.Context, cmd string) error
}

// commitTelemetry — the metering commit (metering — failures are quiet,
// §13. dolt_add has already staged everything, so the failed-commit portion
// is recovered by the next call).
func commitTelemetry(ctx context.Context, st wire.Store, cmd string) {
	if tc, ok := st.(TelemetryCommitter); ok {
		_ = tc.CommitTelemetry(ctx, cmd)
	}
}

// defaultZeroOwnedFloor — the default zero-hit decomposition threshold
// (spec §2.2: with active≥1 it is owned_miss). The meta override
// (zero_owned_floor) is read via a per-key direct-read stopgap (before
// registry-key registration — only n>0 accepted. The same convention's
// knowledge_reads_retention_days has converged onto the registry).
const defaultZeroOwnedFloor = 1

// zeroOwnedFloorKey — the meta override key for the zero-hit decomposition
// threshold.
const zeroOwnedFloorKey = "zero_owned_floor"

// zeroOwnedFloor — the zero-hit decomposition threshold (absent·corrupt·0
// or below yield the default — quiet, §13).
func zeroOwnedFloor(ctx context.Context, st wire.Store) int {
	if v, ok := wire.GetMetaKV(ctx, st, zeroOwnedFloorKey); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return defaultZeroOwnedFloor
}

// activeKnowledgeCount — the current point-in-time active knowledge count
// (the zero-hit decomposition decision source, spec §2.2). Approval clears
// invalid_at, so status='active' AND invalid_at IS NULL is the single
// definition of active. Query failure·empty result yield 0 (quiet, §13).
func activeKnowledgeCount(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	if n, ok := render.CellToInt(rows[0][0]); ok {
		return n
	}
	return 0
}

// ZeroReason — the zero-hit decomposition pure function (spec §2.2 CRAG):
// zero=1 && active knowledge count < floor → "unowned" (effectively
// unowned), zero=1 && ≥floor → "owned_miss" (owned-but-search-missed),
// zero=0 → "" (not a decomposition target). The query body is not recorded
// (the no-personal-wording principle).
func ZeroReason(zero bool, active, floor int) string {
	if !zero {
		return ""
	}
	if active < floor {
		return "unowned"
	}
	return "owned_miss"
}

// gapQueryMaxRunes — the query-body truncation bound of the gap signal
// (v1.12.0 T2; v1.12.1 — the bound is 80 runes of the NFKC-normalized
// form, applied after normalization). Only a truncated·sanitized·
// normalized digest is ever stored — the raw query body is never
// persisted (the query non-persistence principle).
const gapQueryMaxRunes = 80

// NormalizeGapQuery — the gap-signal normalization pipeline (v1.12.0 T2,
// truncation order corrected v1.12.1): NFKC (the lib/query/suggest.go
// normalization basis, applied here directly — composed·decomposed
// spellings converge) → TruncateRunes(80) (v1.12.1: the bound is 80 runes
// of the *normalized* form — truncating before NFKC split a decomposed
// spelling mid-sequence and hashed it apart from its composed twin, so
// the pre-normalization truncation branch is abolished) → render.Text
// sanitization (control characters·[flint] markers neutralized — the
// sanitization chokepoint) → lowercase (locale-invariant
// strings.ToLower) → whitespace fold (runs collapse to one space, edges
// trimmed). The same recall failure then hashes to one key regardless of
// representation noise. T3 reuses this helper for the candidate topic.
func NormalizeGapQuery(query string) string {
	s := norm.NFKC.String(query)
	s = render.TruncateRunes(s, gapQueryMaxRunes)
	s = render.Text(s)
	s = strings.ToLower(s)
	return strings.Join(strings.Fields(s), " ")
}

// GapQueryHash — the sha256 lowercase-hex digest of a normalized query
// (the meta key fragment — 64 chars, [0-9a-f]).
func GapQueryHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// BumpGapQuery — increments the daily gap counter of the normalized query
// and returns the new count (the T3 threshold input). The key
// daily:gapq:<sha256>:<UTC date> is a meta flat counter (the
// BumpDailyFlatKey convention): PruneDaily expires it at the date
// rollover, so only same-day repetition accumulates (a same-day N-fold
// failure is the strong signal — cross-day spread is weak by design,
// gate review ruling). The bump·readback pair is non-atomic under
// parallel sessions (the plan-accepted risk — the T3 content_hash dedup
// is the final defense). Any failure is quiet (§13): (0, false).
func BumpGapQuery(ctx context.Context, st wire.Store, sanitizedQuery string) (count int64, ok bool) {
	day := time.Now().UTC().Format("2006-01-02")
	hash := GapQueryHash(NormalizeGapQuery(sanitizedQuery))
	if err := db.BumpDailyFlatKey(ctx, st, "gapq:"+hash, day, 1); err != nil {
		return 0, false
	}
	// The readback goes through the meta single-entry delegate
	// (GetMetaKVErr — no meta SQL literals here, the TestMetaKVSingleSource
	// guard). Absent·corrupt yields (0, false) quietly.
	val, _, err := wire.GetMetaKVErr(ctx, st, "daily:gapq:"+hash+":"+day)
	if err != nil || val == "" {
		return 0, false
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if perr != nil {
		return 0, false
	}
	return n, true
}

// recordBytes — records len (final output) as kind bytes right after the
// surface output (UTF-8 bytes — the total written to io.Writer). A
// non-implementing store is a no-op; failures are quiet (§13 metering
// principle — a lost byte log beats a failed call).
func recordBytes(ctx context.Context, st wire.Store, kind string, n int) {
	if br, ok := st.(BytesRecorder); ok {
		_ = br.RecordBytes(ctx, kind, n)
	}
}

// idsCSV — an integer id slice to CSV (storage budget cap of 10 — the hits
// column keeps the full hit set). Serialization of the usage-chain source.
func idsCSV(ids []int) string {
	if len(ids) > 10 {
		ids = ids[:10]
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

// FirstExactRank — the 1-based rank of the first topic match (lower-case
// equality, inheriting the RankOrder definition) among returned rows. No
// match is 0 (v0.30.0 R2 — the quality approximation source).
func FirstExactRank(rows [][]any, q string) int {
	// The RankOrder source is SQL lower(original q) — no trim.
	// The quality record uses the same definition (preventing rank
	// distortion on whitespace-bearing q).
	lq := strings.ToLower(q)
	if lq == "" {
		return 0 // an empty query (full listing) has no notion of a match
	}
	for i, r := range rows {
		if len(r) < 2 {
			continue
		}
		if strings.ToLower(fmt.Sprint(r[1])) == lq { // column 1 = topic
			return i + 1
		}
	}
	return 0
}

// ParseIDsCSV — the deserialization of idsCSV (for report consumption).
// Empty string·empty cells are blanks; unparseable cells are quietly
// skipped (metering convention).
func ParseIDsCSV(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// RowIDs — collects the id column (position 0) from query result rows as
// ints. Unmappable cells are quietly skipped (a metering gap never breaks
// the query). Cell conversion is the single render.CellToInt implementation
// (v0.39.0 T3 debt convergence).
func RowIDs(rows [][]any) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if id, ok := render.CellToInt(row[0]); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// InstrumentReads — reference metering (T62): increments the full id set of
// result items + records one intake. v0.42.0 T2: the read-kind intake
// metering (daily read counter·read_stats blob) is abolished — the metering
// source is unified into read_events (RecordReadEvent), cutting one read
// from 4→2 writes (BumpReads·RecordReadEvent). The zero·near
// classifications are passed only as RecordReadEvent arguments (parameters
// kept for signature compatibility). Other kinds (hot) keep the existing
// RecordUsage path unchanged. Being metering, failures are quietly ignored.
func InstrumentReads(ctx context.Context, st wire.Store, kind string, ids []int, zero, near bool) {
	if rb, ok := st.(readBumper); ok {
		_ = rb.BumpReads(ctx, ids)
	}
	if kind == "read" {
		return // intake metering has the single read_events source
	}
	RecordUsage(ctx, st, kind, "")
}

// RunRead — a free-text query with --tag --entity --domain --limit
// --min-confidence --origin filters.
// --kind is not accepted because active_knowledge is a single-kind
// knowledge view, making it meaningless. --status
// pending|active|invalidated (v0.12.0 R1) — when given, queries the base
// table knowledge + status WHERE instead (the view does not expose
// invalidated rows).
func RunRead(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// (100% semantic preservation — N9 "--"·unconditional value
	// consumption·last value wins·unrecognized '-' prefix is an unknown-flag
	// exit 3). Value validation (numeric·empty·allowed values) stays exactly
	// the call-site contract below.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--json"},
			{Name: "--tag", Value: true},
			{Name: "--entity", Value: true},
			{Name: "--domain", Value: true},
			{Name: "--limit", Value: true},
			{Name: "--status", Value: true},
			{Name: "--as-of", Value: true},
			{Name: "--fields", Value: true},
			// v1.20.0 T2 — the confidence floor (0..1 float) and the origin
			// (agent|human) filters: both are plain SQL filters with no
			// thresholds registry involvement (TUNING §2).
			{Name: "--min-confidence", Value: true},
			{Name: "--origin", Value: true},
			// Session label for --session effectiveness metering (optional —
			// METRICS §4). --source channel label (read_events.source
			// metering — MCP-path distinction is passed by the caller,
			// inheriting the write --source convention).
			{Name: "--session", Value: true},
			{Name: "--source", Value: true},
		},
		Usage: "usage: flint read [query] [--json] [--fields F] [--limit N] [--status S] [--as-of T] [--session ID] [--source X] [--tag T] [--entity E] [--domain D] [--min-confidence N] [--origin agent|human]",
		// N9: everything after "--" is positional — even if a query from
		// external input such as MCP looks like "--json", it is not parsed
		// as a flag.
		DashDash:        true,
		AllowPositional: true,
	})
	if perr != nil {
		// An unrecognized '-'-prefixed argument is the invalid-arg exit 3;
		// the error type is usage + an allowed-flag example in the message
		// (typo self-correction).
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	f := query.Filters{
		Tag:    res.Value("--tag"),
		Entity: res.Value("--entity"),
		Domain: res.Value("--domain"),
	}
	jsonMode := res.Given("--json")
	fields, asOfSpec, statusSpec := res.Value("--fields"), res.Value("--as-of"), res.Value("--status")
	session, sourceSpec := res.Value("--session"), res.Value("--source")
	asOfGiven, statusGiven := res.Given("--as-of"), res.Given("--status")
	sessionGiven, sourceGiven := res.Given("--session"), res.Given("--source")
	fieldsGiven := res.Given("--fields")
	if res.Given("--limit") {
		n, err := strconv.Atoi(res.Value("--limit"))
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "limit", Expect: "integer"}))
			return 3
		}
		f.Limit = n
	}
	// --min-confidence N — a 0..1 float (inclusive boundaries). Out-of-range,
	// non-numeric, and valueless trailing flags are invalid-value exit 3
	// (the N10 empty-value convention — a quietly ignored floor would return
	// unfiltered results).
	if res.Given("--min-confidence") {
		v, err := strconv.ParseFloat(res.Value("--min-confidence"), 64)
		if err != nil || v < 0 || v > 1 {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "min-confidence", Expect: "0..1 float"}))
			return 3
		}
		f.MinConfidence = &v
	}
	// --origin agent|human — an unregistered value (and a valueless trailing
	// flag) is invalid-value exit 3 (the status enum convention).
	if res.Given("--origin") {
		switch res.Value("--origin") {
		case "agent", "human":
			f.Origin = res.Value("--origin")
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "origin", Expect: "agent|human"}))
			return 3
		}
	}
	q := strings.Join(res.Positional(), " ")
	// A valueless trailing --tag/--entity/--domain/--fields (N10) also gets
	// exit 3 under the same rule as a valueless --status/--as-of (F1) — if
	// empty values were quietly ignored, results would come back filtered
	// differently than intended. --limit already exits 3 on Atoi failure.
	// (Empty·'-'-prefixed --session values go through the shared
	// validateSessionLabel guard, below.)
	for _, g := range []struct {
		given bool
		val   string
		field string
	}{
		{res.Given("--tag"), f.Tag, "tag"},
		{res.Given("--entity"), f.Entity, "entity"},
		{res.Given("--domain"), f.Domain, "domain"},
		{fieldsGiven, fields, "fields"},
	} {
		if g.given && g.val == "" {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: g.field, Expect: "non-empty"}))
			return 3
		}
	}
	// Without --session, adopt the current session label automatically
	// (recorded by the session-start hook — G4 label automation, quiet).
	if !sessionGiven {
		if label, ok := wire.ReadSessionLabel(); ok {
			session = label
		}
	}
	// The --session value is a label stored in read_events for 14 days —
	// unlike other value-bearing flags, a typo (swallowing a flag like
	// `--session --json`) lingers as DB pollution. A flag-shaped value
	// exits 3 immediately (preventing query-result pollution). The guard is
	// promoted to the shared validateSessionLabel implementation — hot·done
	// use the same validation (exit 3).
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	// Fix the source label (read_events.source — spec §2.3 channel mix).
	// An explicit --source (hook|cli|mcp) wins; without one, the
	// entry-point rule — 'hook' if a session label exists (the session-hook
	// injection path), otherwise 'cli'. Invalid values·valueless get the
	// invalid-arg exit 3 (inheriting the write --source convention —
	// preventing a stored label typo from lingering as DB pollution).
	source := sourceSpec
	if sourceGiven {
		switch source {
		case "hook", "cli", "mcp":
		case "":
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "non-empty"}))
			return 3
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "hook|cli|mcp"}))
			return 3
		}
	} else if session != "" {
		source = "hook"
	} else {
		source = "cli"
	}
	// --fields names accept known columns only — turning the distortion
	// where a typo ("sumary") quietly filled a different column's value via
	// FormatRows positional fallback into a validation error. Fields outside
	// read such as hot's kind·ts are unaffected because hot does not accept
	// --fields.
	if fieldsGiven {
		for _, name := range strings.Split(fields, ",") {
			if name = strings.TrimSpace(name); name == "" || !query.ReadFieldKnown(name) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
					Field:  "fields",
					Expect: "known columns: " + query.ReadColumnsHint(),
				}))
				return 3
			}
		}
	}
	asOfHash, asOfDate := "", ""
	// A valueless --as-of (last argument) also gets the invalid-spec exit 3 —
	// quietly returning present-point data would produce wrong results (the
	// same rule as status).
	if asOfGiven && asOfSpec == "" {
		fmt.Fprintln(stdout, asOfErrLine("invalid_as_of",
			fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf).Error()))
		return 3
	}
	// A valueless --status (last argument) also gets the invalid-spec exit 3
	// — the same rule as the valueless --as-of guard: quietly ignoring it
	// would return results other than intended. Invalid status value
	// validation always runs regardless of --as-of presence. Under a
	// combined --as-of, as-of takes precedence·status is ignored (without a
	// warning) applies only to valid values, and Filters.Status is set only
	// when specified alone.
	if statusGiven || statusSpec != "" {
		switch statusSpec {
		case "pending", "active", "invalidated":
			if asOfSpec == "" {
				f.Status = statusSpec
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
				Field: "status", Expect: "pending|active|invalidated"}))
			return 3
		}
	}
	var sql string
	var qargs []any
	if f.Status != "" {
		sql, qargs = query.BuildReadStatus(q, f)
	} else {
		sql, qargs = query.BuildRead(q, f)
	}
	var rows [][]any
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			if errors.Is(err, ErrInvalidAsOf) {
				fmt.Fprintln(stdout, asOfErrLine("invalid_as_of", err.Error()))
				return 3
			}
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		// Dialect severance: AS OF inlining (strings.Replace) and hash
		// charset revalidation are transferred to Store.QueryAsOf (the db
		// implementation) — no raw AS OF clause remains at the caller.
		// Columns·filter where·RankOrder·LIMIT are severed and inherited
		// from the query assembler's output (single source — preventing
		// reassembly duplication). View-incompatibility guidance wrapping is
		// also handled on the implementation side (inheriting the C3
		// contract, original-equivalent — no base retry).
		aq := splitReadAsOf(sql, qargs)
		aq.Hash, aq.Table = hash, "active_knowledge"
		asOfHash = hash
		// Attach the commit time — metadata query failure is quiet (empty
		// value, §13).
		if les, lerr := st.LogEntries(ctx, wire.LogQuery{Hash: hash}); lerr == nil && len(les) == 1 {
			asOfDate = FmtAsOfDate(les[0].Date)
		}
		rows, err = st.QueryAsOf(ctx, aq)
		if err != nil {
			// View-column incompatibility (a column absent from past rows)
			// guidance wrapping is also transferred to the implementation
			// boundary — the caller only propagates.
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	} else {
		var err error
		rows, err = wire.QueryOnce(ctx, st, sql, qargs)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	// Zero-hit fallback: only when the phrase match has no hits, re-query
	// with word OR then rerank via RRF fusion (the phrase-hit path is fully
	// invariant). ReorderByFallbackCount is a 4-signal RRF of match
	// count·read_count·initial consonants·topic. The rrf_k override
	// injection moves **before** the rerank. 1-token queries are
	// homogeneous with the phrase and skip it. --as-of (a past point)
	// skips it because a present-point fallback breaks point-in-time
	// consistency. --status is itself a state filter, not a retry target,
	// so no fallback·suggestion fire. A fallback attempt increments
	// meta read_fallback_calls once (quiet). If the fallback is also
	// zero-hit, rows stay as-is (the existing zero-hit output). Whether a
	// fallback was attempted is passed into the read_events record
	// (METRICS §4 G1).
	fallbackTried := false
	// F6 (performance): the zero-hit path loads thresholds once — the rrf_k
	// injection below and the suggestion-threshold injection used to issue
	// the meta IN query twice in one run; now they share one load (load
	// failure keeps defaults — quiet §13, same as before).
	var th store.Thresholds
	thOK := false
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {
		// rrf_k injection hoist — the fallback rerank reflects the override
		// in the same run (failure keeps defaults — quiet §13).
		if loaded, thErr := wire.LoadThresholdsFn(ctx, st); thErr == nil {
			th, thOK = loaded, true
			query.SetRRFK(th.RrfK)
		}
		if fsql, fargs, ok := query.BuildReadFallback(q, f); ok {
			BumpReadFallbackToMeta(ctx, st)
			fallbackTried = true
			if frows, ferr := wire.QueryOnce(ctx, st, fsql, fargs); ferr == nil && len(frows) > 0 {
				rows = frows
				query.ReorderByFallbackCount(rows, query.TokenizeQuery(q))
			}
		}
	}
	// Phrase-hit token rerank (v1.16.0 T1): the phrase-hit rows are reranked
	// in-memory by per-token tier scores (topic prefix > summary prefix >
	// contains, + a cover bonus per matched token — both sides NFKC·lower·
	// whitespace normalized) before the output. The SQL LIKE gives no
	// within-window similarity signal, so the window (the SQL LIMIT stays —
	// reorder happens inside it only) is reordered app-side. Gated on
	// !fallbackTried: fallback rows already carry ReorderByFallbackCount's
	// RRF fusion order and are never re-reordered (the double-reorder
	// prohibition). Ties keep the SQL RankOrder order. hot/brief are out of
	// scope by the research ruling; a tokenless q (full listing) is a no-op.
	if !fallbackTried {
		rows = query.ReorderByTokenScore(rows, q, query.ReadColumnIndexMap())
	}
	// zero_near classification: only when both phrase·fallback are zero-hit
	// && at the present point, a full scan of active_knowledge (filter-free)
	// computes near candidates. The suggestion scan is inherited without
	// output — the result feeds only the internal classification
	// (zero_near metering) invisible to the agent. Scan failure·zero
	// candidates are quiet (§13).
	var suggestions []query.Suggestion
	// scanMax — the suggestion scan's best bigramDice score over ALL
	// scanned candidates, computed before the min_score filter (schema
	// v13 read_events.near_score). Zero rows keep their scan best even
	// when every candidate sits below min_score (near=false rows are not
	// masked to 0 — the min_score-boundary distribution is the
	// counterfactual-analysis core); hit rows stay 0 (the scan runs on
	// the zero-hit path only). Scan failure keeps 0 — quiet (§13).
	scanMax := 0.0
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {
		// Suggestion thresholds (min_score·scan_cap) prefer the thresholds
		// registry (meta override → SetSuggestThresholds injection). Lookup
		// failure keeps the query-package defaults (=DefaultThresholds) —
		// quiet (§13). F6 (performance): the values reuse the single load
		// from the fallback block above (no re-query).
		if thOK {
			query.SetSuggestThresholds(th.SuggestMinScore, th.SuggestScanCap)
			query.SetRRFK(th.RrfK) // already injected above — idempotent re-assertion (suggestion path)
		}
		ssql, sargs := query.BuildSuggestScan()
		if srows, serr := wire.QueryOnce(ctx, st, ssql, sargs); serr == nil {
			suggestions, scanMax = query.SuggestSimilarWithScanMax(suggestQuery(q), srows, 3)
		}
	}
	// Output neutralization (ADR-0006): re-applies the storage-stage
	// sanitization to reads as well — so control characters·[flint] markers
	// introduced after storage or along the path cannot forge the output
	// boundary. Sanitization converges on the render choke point
	// (render.Cells).
	rows = render.Cells(rows)
	// Silence contract: an eligible (present-point·stateless-filter)
	// zero-hit is fully silent — FormatRows returns "" for empty rows
	// anyway, so no separate branch is needed. Suggestion lines are
	// abolished (zero hit rows 0 + silent scan).
	out := query.FormatRows(rows, fields, jsonMode)
	fmt.Fprint(stdout, out)
	// With --as-of, append a one-line as_of{hash,commit_date} (JSON) after
	// the results. Without it, only the existing fields.
	byteLen := len(out) // G5: total written to io.Writer (UTF-8 bytes)
	// A --json zero-hit is a one-line {"hits":0} — the caller
	// machine-reads the hit count (text mode keeps the existing full
	// silence). Byte metering inherits the as_of line convention (included
	// in the total written).
	if len(rows) == 0 && jsonMode {
		b, _ := json.Marshal(ZeroHitLine{Hits: 0})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1 // the hits line (+newline) is also part of the total written
	}
	// --limit cap clamp notice — when the request exceeds ClampLimit (50),
	// disclose it as one additive-field line (limit_requested·
	// limit_applied) in the --json result so the caller machine-reads the
	// truncation fact (inheriting the zero-hit hits line·as_of line
	// attachment convention). Text mode keeps the existing format.
	if jsonMode && query.ClampLimit(f.Limit) < f.Limit {
		b, _ := json.Marshal(LimitClampLine{
			LimitRequested: f.Limit,
			LimitApplied:   query.ClampLimit(f.Limit),
		})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1 // the clamp line (+newline) is also part of the total written
	}
	if asOfHash != "" {
		b, _ := json.Marshal(AsOfLine{AsOf: AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1 // the as_of line (+newline) is also part of the total written
	}
	// G5 byte metering: one read — a zero-hit is recorded as out="" 0 bytes
	// (call-count denominator consistency, spec §1). Quiet (§13).
	recordBytes(ctx, st, "read", byteLen)
	// Metering: increments references for the full id set of result items.
	// The read-intake read_stats blob·daily counter writes are abolished
	// (writes 4→2: only BumpReads·RecordReadEvent remain). The full id set
	// is also reused for the knowledge_reads record. Quiet (§13).
	rowIDs := RowIDs(rows)
	InstrumentReads(ctx, st, "read", rowIDs, len(rows) == 0, len(suggestions) > 0)
	// Leave the full returned id set as per-item recall history (kind=read)
	// (G4 alignment·recall-fate source — the full set, independent of the
	// top-10 truncation in the ids CSV). Quiet (§13).
	recordKnowledgeReads(ctx, st, rowIDs, session, "read")
	// Effectiveness metering (docs/METRICS.md §4): record one read into
	// read_events. A non-implementing Store (fake) skips it; failures are
	// quiet (§13 metering principle). The returned id CSV accompanies it
	// (the usage-chain source).
	if res, ok := st.(ReadEventStore); ok {
		// First-match rank — fallback-mediated reads are excluded from the
		// quality metric (0). --as-of (a past-point inspection)·--status (a
		// state-filter query) are also excluded from the present search
		// quality metric — the quality source is the ordinary recall path
		// only (along with the METRICS §2 contract documentation).
		rank := 0
		if !fallbackTried && asOfSpec == "" && f.Status == "" {
			rank = FirstExactRank(rows, q)
		}
		// Zero-hit rows are recorded split into unowned/owned_miss by
		// active-knowledge ownership (spec §2.2 — a zero=0 row is ''). Quiet
		// (§13).
		zeroReason := ""
		if len(rows) == 0 {
			zeroReason = ZeroReason(true, activeKnowledgeCount(ctx, st), zeroOwnedFloor(ctx, st))
		}
		_ = res.RecordReadEvent(ctx, db.ReadEvent{
			Hits:       len(rows),
			Zero:       len(rows) == 0,
			Near:       len(suggestions) > 0,
			Fallback:   fallbackTried,
			Session:    session,
			IDs:        idsCSV(rowIDs),
			FirstRank:  rank,
			Bytes:      byteLen,
			ZeroReason: zeroReason,
			Source:     source,
			NearScore:  scanMax,
		})
		// v1.12.0 T2·T3 — the recall-gap signal: a zero-hit on owned
		// ground (owned_miss) bumps the normalized-query daily counter;
		// reaching gap_query_threshold accrues a pending gap candidate
		// (quietly — §13). unowned is DB-void noise (research ruling) —
		// not counted; an as-of read (a past-point snapshot inspection) is
		// not a present gap — the guard skips the count. The hot path is
		// out of scope by the same convention.
		if zeroReason == "owned_miss" && asOfSpec == "" {
			if n, ok := BumpGapQuery(ctx, st, q); ok {
				maybeAccrueGapCandidate(ctx, st, q, n)
			}
		}
	}
	// The read completion-path audit commit — so the recall bump (INSERT
	// into recall_events)·meta increments are not absorbed into the
	// metering commit (with the -A sweep of telemetry abolished, metering
	// staging holds only the metering originals), commit at the audit
	// boundary (the done session-end commit convention. Failure is a
	// one-line stderr·non-interference §13 — the remainder is recovered by
	// the next audit commit, no loss).
	if _, cerr := st.Commit(ctx, "read: recall bump (read audit attribution)"); cerr != nil {
		fmt.Fprintf(os.Stderr, "[flint] read audit commit: %v\n", cerr)
	}
	// A separate commit for the metering table (once on the read completion
	// path — independent of the audit commit). Failure is quiet (§13 —
	// staging survives, so the next call recovers it).
	commitTelemetry(ctx, st, "read")
	// Zero-hit is exit 0 — text silence·--json {"hits":0} is a normal
	// response, not an error, so the agent's error-abort loop is not broken
	// (exit 0 regardless of eligibility on all paths).
	return 0
}

// validateSessionLabel — the shared guard for the --session label (read
// contract promotion — applied identically in hot·done). given=false (flag
// absent) passes. An empty value (trailing --session)·a '-' prefix (a typo
// swallowing a flag like `--session --json` — session labels are values
// stored in read_events·hot_view, so a typo lingers as DB pollution)·an
// embedded newline (the session label file holds a one-line value — \n·\r
// break both the file contract and the stored value) is an invalid-value
// SchemaError — the caller prints it then exits 3.
func validateSessionLabel(given bool, val string) error {
	if !given {
		return nil
	}
	if val == "" {
		return &store.SchemaError{Field: "session", Expect: "non-empty"}
	}
	if strings.HasPrefix(val, "-") {
		return &store.SchemaError{Field: "session", Expect: "label, not a flag"}
	}
	if strings.ContainsAny(val, "\n\r") {
		return &store.SchemaError{Field: "session", Expect: "single-line label without newlines"}
	}
	return nil
}

// ValidateSessionLabel — the export of validateSessionLabel (v1.0.2 R2-B1:
// the shared guard used by the session-hook entry points session-start·
// session-end before label recording·session-start recording — the
// implementation stays a single source).
func ValidateSessionLabel(given bool, val string) error {
	return validateSessionLabel(given, val)
}

// ReadSuggestEligible — the eligible condition shared by the fallback gate·
// suggestion scan gate: true only for a zero-hit at the present point
// (--as-of absent)·without state filters (--status absent). The exit-code
// consumer was abolished by v1.5.3 P2 (exit 0 unified).
func ReadSuggestEligible(rows int, asOfSpec, status string) bool {
	return rows == 0 && asOfSpec == "" && status == ""
}

// suggestQueryMaxRunes — the suggestion (SuggestSimilar) input cap: 4096
// runes. bigramDice rebuilds the query bigram set for every scanned row, so
// a huge query (100KB-class) once delayed read by ~17s from the suggestion
// alone — only the suggestion input is truncated while the body search
// query stays verbatim (the similarity signal suffices with the leading 4KB
// — ranking did-you-mean candidates).
const suggestQueryMaxRunes = 4096

// suggestQuery — applies the suggestion input cap (multibyte-safe —
// inheriting the single TruncateRunes implementation). ClampLimit
// convention: truncate quietly, no separate report.
func suggestQuery(q string) string {
	return render.TruncateRunes(q, suggestQueryMaxRunes)
}

// BumpReadFallbackToMeta — increments one fallback attempt (v0.9.6).
// Inherits the existing metering pattern (BumpReadStatsToMeta) — failures
// are quietly ignored (non-interference §13). v0.34.3 T2: the
// SELECT→INSERT non-atomic round trip converged into a single atomic
// bumpMetaKV upsert (preventing concurrent-run loss mid-increment).
func BumpReadFallbackToMeta(ctx context.Context, st wire.Store) {
	_ = wire.BumpMetaKV(ctx, st, query.ReadFallbackCountKey)
}

// AsOfInfo / AsOfLine — the one-line JSON {as_of:{hash,commit_date}}
// appended to --as-of results.
type AsOfInfo struct {
	Hash       string `json:"hash"`
	CommitDate string `json:"commit_date"`
}

type AsOfLine struct {
	AsOf AsOfInfo `json:"as_of"`
}

// ZeroHitLine — the one-line --json zero-hit JSON {"hits":0} (v1.5.3 P2 —
// inheriting the AsOfLine attachment convention. Not printed when there are
// hit rows).
type ZeroHitLine struct {
	Hits int `json:"hits"`
}

// LimitClampLine — the one-line --limit cap clamp notice JSON (v1.5.4 T2 P3
// — additive schema: existing output keys unchanged, printed only when the
// request exceeds the cap (50)).
type LimitClampLine struct {
	LimitRequested int `json:"limit_requested"`
	LimitApplied   int `json:"limit_applied"`
}

// asOfErrLine — assembles the one-line {error,message} JSON (a local
// variant of ErrLine — wire.ErrLine classifies only store-layer errors).
func asOfErrLine(code, msg string) string {
	b, _ := json.Marshal(map[string]string{"error": code, "message": msg})
	return string(b)
}

// FmtAsOfDate — a dolt_log date cell into an RFC3339 string. Anything not a
// time.Time is stringified as-is.
func FmtAsOfDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

// asOfHashRe — the strict charset of the AS OF inline hash (lowercase
// base32-family — the dolt_log commit_hash format). A double check of the
// engine-returned value. The inline revalidation on the read path moved to
// the db implementation (QueryAsOf); this definition remains the single
// source of the exported ValidAsOfHash symbol (consumed by ops status).
var asOfHashRe = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

// ValidAsOfHash — the exported asOfHashRe charset check for AS OF inline
// hashes (the same rule as v0.28.0 H③ — single source). Lets inline callers
// outside read (the ops status count query) revalidate with the same rule.
func ValidAsOfHash(hash string) bool {
	return asOfHashRe.MatchString(hash)
}

// splitReadAsOf — severs the SQL/arguments assembled by BuildRead into
// AsOfQuery fragments and passes them to QueryAsOf (single source —
// columns·filter where·RankOrder are inherited verbatim from the query
// assembler's output, preventing reassembly duplication). Where is the raw
// tail clause after the table reference ("WHERE ... ORDER BY ..." —
// including $N placeholders; "ORDER BY ..." when filterless), Args is the
// rest minus the trailing LIMIT argument (filters + ranking parameters),
// and Limit is that value. The implementation side assembles
// "SELECT cols FROM tbl AS OF 'hash' {Where} LIMIT {Limit}".
func splitReadAsOf(sql string, args []any) wire.AsOfQuery {
	const from = " FROM active_knowledge"
	var q wire.AsOfQuery
	i := strings.Index(sql, from)
	head := strings.TrimPrefix(sql[:i], "SELECT ")
	tail := sql[i+len(from):]
	// Sever the trailing " LIMIT $n" — inherited into the Limit int field.
	if j := strings.LastIndex(tail, " LIMIT $"); j >= 0 {
		tail = tail[:j]
	}
	q.Columns = strings.Split(head, ", ")
	q.Where = strings.TrimSpace(tail)
	q.Args = args
	if n, ok := args[len(args)-1].(int); ok {
		q.Limit, q.Args = n, args[:len(args)-1]
	}
	return q
}
