// done.go — flint done: the Stop hook exit path. Only emits the
// consecutive-unrecorded warning and never blocks exit (PLAN §13 the five
// non-interference principles — exit 0 on every path. Invalid-argument
// exception: only an invalid --session value·a usage error from a missing
// session gets the invalid-arg exit 3 — the real hook wiring always passes
// a session).
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// staleSessionStore — StaleSessionRuns access. The wire.Store interface is
// v0.1-owned, so it is consumed via type assertion instead of extension
// (root.go modifications forbidden).
type staleSessionStore interface {
	StaleSessionRuns(ctx context.Context, agent string) (int, error)
}

// usageStore — LogPlaybookUse access. The wire.Store interface is
// v0.1-owned, so it is consumed via type assertion instead of extension
// (root.go modifications forbidden).
type usageStore interface {
	LogPlaybookUse(ctx context.Context, id int, ok bool) error
}

// logPlaybookUse — one use log right before exit. Recorded only when an id
// was received via --playbook; the done output·exit code are fully
// invariant and failures are quietly ignored as at most one stderr line
// (non-interference §13 — the same treatment as AutoBackupFn).
func logPlaybookUse(ctx context.Context, st wire.Store, id int, ok bool) {
	if id <= 0 {
		return
	}
	us, can := st.(usageStore)
	if !can {
		return
	}
	if err := us.LogPlaybookUse(ctx, id, ok); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] playbook use log: %v\n", err)
	}
}

// DoneResult — the done verdict result (material for the automatic session
// summary).
type DoneResult struct {
	Agent      string // the aggregated agent
	Unrecorded int    // consecutive unrecorded session count
	// The exit summary (the session-knowledgization source) — empty when
	// the Stop hook stdin delivers no summary (the fallback boilerplate is
	// SessionRecord-only — knowledgization skips empty summaries).
	Summary string
}

// sessionEndPrefix — the exit-summary marker prefix (for identifying
// session-renewal targets).
const sessionEndPrefix = "session end"

// SessionRecord — the record for the sessions upsert (kind·agent·exit
// summary·empty tasks).
func (d *DoneResult) SessionRecord() map[string]any {
	return map[string]any{
		"kind":    "session",
		"agent":   d.Agent,
		"summary": fmt.Sprintf("%s (unrecorded %d)", sessionEndPrefix, d.Unrecorded),
		"tasks":   "[]",
	}
}

// sessionSummaryStore — the single-path capability for updating the
// sessions exit summary. realStore delegates to db.Repo.UpdateSessionSummary
// — the write guard (writes_through_repo_test) forbids write tokens in
// Query/queryOnce call sites in lib/{ops,knowledge} sources.
type sessionSummaryStore interface {
	UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
}

// writeSessionRecord — the session auto-summary upsert: updates the
// agent's most recent session row with the exit summary, keeping the
// session count invariant (resolving the defect where a new row polluted
// the StaleSessionRuns unrecorded count). INSERT only when the agent has
// no session. The sessions update follows the maintain renewal path.
// Failures are quietly ignored as at most one stderr line (non-interference
// §13).
func writeSessionRecord(ctx context.Context, st wire.Store, rec map[string]any) int {
	agent, _ := rec["agent"].(string)
	// Agent-label sanitization·secret scanning — closing the blind spot
	// where a direct InsertBatch call bypassed sanitizePayload (storing
	// [flint] markers·unchecked secrets). Warnings only (storage continues
	// per the contract — §13). Via the warnSecrets helper.
	warnSecrets("session agent", agent)
	agent = store.SanitizeText(agent)
	rec["agent"] = agent
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id FROM sessions WHERE agent = $1
		 ORDER BY ts DESC, id DESC LIMIT 1`, []any{agent})
	if err == nil && len(rows) > 0 && rows[0][0] != nil {
		// pgx scans int4 as int32 — parse via a string instead of a type
		// assertion.
		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); n == 1 {
			// The renewal UPDATE has the single path
			// db.Repo.UpdateSessionSummary. A capability non-implementation
			// (legacy fake) gets a quiet one-line stderr notice and the
			// renewal is skipped (non-interference §13 — the exit
			// continues).
			us, ok := st.(sessionSummaryStore)
			if !ok {
				fmt.Fprintln(os.Stderr, "[flint] session summary: unsupported store")
				return id // one lookup per session exit, sharing the knowledgization session_id
			}
			summary, _ := rec["summary"].(string)
			tasks, _ := rec["tasks"].(string)
			if _, err := us.UpdateSessionSummary(ctx, id, summary, tasks); err != nil {
				fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
			}
			return id // one lookup per session exit, sharing the knowledgization session_id
		}
	}
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
	}
	return 0
}

// hookDefaultAgent — the default aggregated agent for UUID sessions. The
// real hook wiring passes the Claude session_id (a UUID) as --session; if
// the UUID were used as the agent verbatim, each session would split into
// its own agent and StaleSessionRuns could never exceed 1, so the stale
// warning would never fire (a reachability defect). Stabilized to the same
// harness identifier as the compact accrual row's agent (compact.go
// "claude").
const hookDefaultAgent = "claude"

// hookSessionUUIDRe — the Claude session_id format the real hook wiring
// passes (8-4-4-4-12 hex). Absorption into claude applies to this format
// only.
var hookSessionUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// agentForSession — finds the aggregated agent from the session value. If
// numeric, uses the sessions.id lookup result; if a UUID, the harness
// default agent (hookDefaultAgent — hook UUID sessions aggregate with the
// compact rows under one agent). Other uninterpreted values (e2e·another
// harness's string sessions, an unfound numeric id) are used as the agent
// verbatim — absorbing them would overwrite real claude session summaries
// and misfire the stale warning.
func agentForSession(ctx context.Context, st wire.Store, session string) string {
	if id, err := strconv.Atoi(session); err == nil {
		rows, err := wire.QueryOnce(ctx, st, `SELECT agent FROM sessions WHERE id = $1`, []any{id})
		if err == nil && len(rows) > 0 {
			if a, ok := rows[0][0].(string); ok && a != "" {
				return a
			}
		}
	}
	if hookSessionUUIDRe.MatchString(session) {
		return hookDefaultAgent
	}
	return session
}

// staleRuns — if the Store does not implement StaleSessionRuns (legacy
// fake), a warning-free 0 is the safe default.
func staleRuns(ctx context.Context, st wire.Store, agent string) (int, error) {
	if ss, ok := st.(staleSessionStore); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil
}

// RunDone — `flint done --session <id>`: a stale warning when consecutive
// unrecorded >= StaleSessions, otherwise ok. Only a warning — it never
// blocks exit (always exit 0). v1.0.1 (F3) exception: an invalid --session
// value (empty·'-'-prefixed) gets the invalid-arg exit 3 — the same shared
// guard as read·hot. The real hook wiring always passes a real session id,
// so the normal exit path is invariant (the usage hint for a missing flag
// remains exit 0).
func RunDone(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	sessionGiven := false
	playbook, playbookOK := 0, true // --playbook <id>·--playbook-ok <bool> (success by default)
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			sessionGiven = true
			if i+1 < len(args) {
				session = args[i+1]
				i++
			}
		}
		if args[i] == "--playbook" && i+1 < len(args) {
			// The hook passes the playbook id the most recent read
			// referenced (use log).
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				playbook = n
			}
			i++
		}
		if args[i] == "--playbook-ok" && i+1 < len(args) {
			if b, err := strconv.ParseBool(args[i+1]); err == nil {
				playbookOK = b
			}
			i++
		}
	}
	// The --session label guard — the shared read·hot contract (empty
	// values·flag-swallowing typos get the invalid-arg exit 3, preventing
	// DB pollution of the recorded session label).
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if session == "" {
		// A usage error (missing session) is unified to the invalid-arg
		// exit 3. The real hook wiring always passes a real session id, so
		// the normal exit path is invariant (non-interference §13 — the
		// warning·side-effect paths keep exit 0). The error type is also
		// the common usage form.
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("usage: flint done --session <id>")))
		return 3
	}
	// Threshold-loader unification — the shared WriteThresholds from
	// write.go (abolishing the byte-identical local duplicate).
	th := WriteThresholds(ctx, st)
	agent := agentForSession(ctx, st, session)
	runs, err := staleRuns(ctx, st, agent)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0
	}
	// Wire the Stop hook stdin's compact_summary as the session
	// knowledgization source. Per the Claude Code spec the Stop stdin has
	// no such field, so this path is another harness's fallback; the
	// standard path is the maybeSessionKnowledge call in PostCompact
	// (flint compact).
	res := &DoneResult{Agent: agent, Unrecorded: runs, Summary: doneStdinSummary(stdin)}
	// Flows into the common tail after the stale·recall·ok output branch
	// (preventing drift from maintaining the exit sequence twice — blocking
	// the recurrence of the blind spot where the stale early return missed
	// knowledgization).
	if runs >= th.StaleSessions {
		fmt.Fprintf(stdout, `{"status":"stale","runs":%d,"warning":"%d consecutive sessions unrecorded"}`+"\n", runs, runs)
		// The firing counter — one stale warning firing (diagnostics D
		// source, spec §2.5).
		BumpMetaCounter(ctx, st, "fired_done_stale")
	} else if w, starved := recallStarvedToday(ctx, st, th); starved {
		// Recall warning — when today has 0 recalls and records are at or
		// above the threshold, exit with a one-line warning (the same
		// pattern as the stale warning — threshold-based·never blocking
		// exit, §13). The Stop hook stdout is invisible in automatic
		// wiring, so a recall marker is also left — the next SessionStart
		// (nudge) consumes it after showing it once. A separate file
		// (recall-hint.json) — avoiding an overwrite collision with the
		// distillation-pending marker (different lifecycles: one-shot vs
		// durable).
		fmt.Fprintf(stdout, `{"status":"recall","writes":%d,"warning":"0 recalls vs %d writes today — run flint read before work next session"}`+"\n", w, w)
		// A meta counter accrues alongside the marker — even if the
		// minimal profile quietly concludes the marker, doctor observes it
		// profile-independently.
		BumpMetaCounter(ctx, st, "recall_starved_count")
		if merr := writeTypedMarker(markerKindRecall, session, nudgeReasonRecall); merr != nil {
			fmt.Fprintf(os.Stderr, "[flint] recall marker: %v\n", merr)
		}
	} else {
		fmt.Fprintln(stdout, `{"status":"ok"}`)
	}
	sessionRowID := writeSessionRecord(ctx, st, res.SessionRecord())
	// Even a stale exit knowledgizes when a summary exists — consecutive
	// unrecorded is exactly when knowledge preservation is most needed;
	// skipping it would kill the feature.
	maybeSessionKnowledge(ctx, st, res.Summary, agent, session, sessionRowID)
	logPlaybookUse(ctx, st, playbook, playbookOK)
	// The session row·knowledgization·meta counters used to exit without a
	// dolt commit and be absorbed by an unrelated next write's global
	// staging, skewing audit attribution — commit at the exit boundary
	// (failure is stderr·non-interference §13).
	if _, cerr := st.Commit(ctx, fmt.Sprintf(
		"session-end: agent=%s runs=%d (session-end audit attribution) [session:0]",
		agentLabel(agent, 24), runs)); cerr != nil {
		fmt.Fprintf(os.Stderr, "[flint] session-end commit: %v\n", cerr)
	}
	// A separate commit for the metering table (once on the done completion
	// path — after the existing audit commit·failure quiet, §13).
	commitTelemetry(ctx, st, "done")
	AutoBackupFn(ctx, st)
	return 0
}

// maybeSessionKnowledge — session knowledgization: at session end, accrues
// a knowledge candidate from the session summary as pending knowledge.
// Rule-based only (LLM verdicts excluded — §13·investigation ruling): only
// summaries meeting the minimum length·dedup (content_hash)·frequency cap
// (the session_knowledge_days threshold, once per day). Accrual is pending
// (awaiting review) — promoted via the approve path (trust gate
// inherited). Takes the source as a string — called from both the Stop
// hook stdin (another harness's fallback) and PostCompact (flint compact —
// the Claude Code standard path, where summaries actually arrive).
// Contract repairs: length gate after sanitization·topic sanitization·
// summary truncation to 500 runes (import symmetry)·tags []string (the
// "process" string is dropped to empty in normalizeTags)·storage-time
// secret-scan warnings (aligned with the write-path contract).
func maybeSessionKnowledge(ctx context.Context, st wire.Store, summary, agent, session string, sessionRowID int) {
	// Move the empty gate ahead of the threshold load — exits without a
	// summary (ordinary Stop) no longer pay the thresholds round trip.
	summary = render.TruncateRunes(store.SanitizeText(strings.TrimSpace(summary)), 500)
	// Accrue only when a real summary (the Compact result etc.) is
	// delivered — the fallback boilerplate ("session end (unrecorded N)")
	// is not knowledge and is skipped. The gate is applied after
	// sanitization·truncation (preventing control-character-only input
	// from being stored as an empty summary row).
	if summary == "" || len([]rune(summary)) < 12 {
		return
	}
	th := WriteThresholds(ctx, st)
	if th.SessionKnowledgeDays <= 0 {
		return // -1 = off
	}
	// The daily frequency cap — skip if the last accrual is within N days
	// (preventing consecutive-session spam).
	getKV, _ := metaKV(ctx, st)
	if v, err := getKV("session_knowledge_last"); err == nil && v != "" {
		// DaysSpan clamps the threshold days (the v1.19.2 overflow sweep) — a
		// session_knowledge_days override past the Duration ceiling used to wrap
		// the gate span negative (the once-per-N-days cap defeated — every
		// session accrued).
		if last, perr := time.Parse(time.RFC3339, v); perr == nil &&
			time.Since(last) < db.DaysSpan(th.SessionKnowledgeDays, time.Now()) {
			return
		}
	}
	// Summary keywords in the topic — "session[claude]" was meaningless as
	// a recall topic-match signal. Reflects the leading part of the first
	// sentence (the first delimiter or 20 runes) to improve browsing·search
	// quality (rule-based — LLM verdicts excluded, §13).
	topic := store.SanitizeText(render.TruncateRunes(
		fmt.Sprintf("session[%s] %s", agentLabel(agent, 24), summaryKeyword(summary)), 80))
	rec := map[string]any{
		"kind":       "knowledge",
		"topic":      topic,
		"summary":    summary,
		"tags":       []string{"process"}, // the string is dropped in normalizeTags
		"domain":     "main",
		"confidence": 0.5, // pending gate — promoted via the approve path
		"status":     "pending",
		"origin":     "hook", // machine-accrual identification (distinct from the default "agent")
	}
	// session_id inherits the id writeSessionRecord found (one lookup per
	// session exit). Out-of-path calls such as compact pass 0 — one more
	// lookup at most via the sessionRowIDForAgent fallback.
	if sessionRowID <= 0 {
		sessionRowID = sessionRowIDForAgent(ctx, st, agent)
	}
	if sessionRowID > 0 {
		rec["session_id"] = sessionRowID
	}
	rec = store.Normalize("knowledge", rec)
	rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
	if exists, err := st.ExistsHash(ctx, rec["content_hash"].(string)); err == nil && exists {
		return // duplicate prevention (blocking re-accrual on session re-exit)
	}
	// Storage-time secret scanning — aligned with the write-path contract
	// (warn only on dedup passers — avoiding re-warning noise for
	// permanently skipped content). Warnings only (not a storage rejection
	// — dual defense with the doctor's after-the-fact scan).
	warnSecrets("session knowledge", summary)
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		// One stderr line instead of total silence (aligned with the recall
		// marker convention) — the exit is never blocked (§13).
		fmt.Fprintf(os.Stderr, "[flint] session knowledge: %v\n", err)
		return
	}
	// Marker accrual — naturally feeding the distillation circuit (the
	// nudge budget wording) (R2). Only when the session is identifiable
	// (aligned with the WriteMarker guard adjacent to the marker-less
	// session_id fallback in compact).
	if session != "" {
		if merr := writeTypedMarker(markerKindDistill, session, "session-knowledge"); merr != nil {
			fmt.Fprintf(os.Stderr, "[flint] distill marker: %v\n", merr)
		}
	}
	_, setKV := metaKV(ctx, st)
	if setKV != nil {
		_ = setKV("session_knowledge_last", time.Now().UTC().Format(time.RFC3339))
	}
}

// summaryKeyword — the topic keyword from the summary's first sentence
// (the first delimiter·20-rune cap — rule-based).
func summaryKeyword(summary string) string {
	first := strings.SplitN(summary, "\n", 2)[0]
	// Colons excluded — in the shape "X patch: the core subject", the core
	// comes after the colon. Cut at the earliest boundary, not in list
	// order.
	cut := -1
	for _, sep := range []string{"。", "다.", ". ", "— "} {
		if i := strings.Index(first, sep); i > 4 && (cut < 0 || i < cut) {
			cut = i + len(sep)
		}
	}
	if cut > 0 {
		first = first[:cut]
	}
	return strings.TrimSpace(render.TruncateRunes(first, 20))
}

// sessionRowIDForAgent — the agent's latest sessions.id (inheriting the
// writeSessionRecord lookup). Not found·error yields 0.
func sessionRowIDForAgent(ctx context.Context, st wire.Store, agent string) int {
	if agent == "" {
		return 0
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id FROM sessions WHERE agent = $1 ORDER BY ts DESC, id DESC LIMIT 1`,
		[]any{agent})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	var id int
	if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); n == 1 {
		return id
	}
	return 0
}

// warnSecrets — the single helper for storage-time secret-scan warnings
// (converging the writeSessionRecord·session-knowledgization·import
// triple duplication). One stderr line each.
func warnSecrets(prefix, s string) {
	for _, name := range store.ScanSecrets(s) {
		fmt.Fprintf(os.Stderr, "[flint] %s: possible secret: %s\n", prefix, name)
	}
}

// agentLabel — the session label (empty-value fallback + TruncateRunes
// reuse — truncation-helper unification).
func agentLabel(agent string, max int) string {
	if agent == "" {
		return "unknown"
	}
	return render.TruncateRunes(agent, max)
}

// recallStarvedToday — the recall-absence verdict from today's daily
// counters. true only when reads are 0 && writes >= RecallWarnWrites.
// Counter lookup·corruption are quietly false (absence of the warning =
// the exit path unchanged — §13). The recording source is aligned with the
// actual producer — writes are read directly from the flat key
// `daily:write:<date>` int value (abolishing the legacy daily:<date> JSON
// fallback — RecordUsage no longer writes that key, so the fallback always
// returned 0, the counter froze forever, and the recall warning·
// recall_starved_count died). Reads are the count of same-day (UTC
// midnight or later·before tomorrow's midnight) read_events rows — the
// date severance follows the same convention as ops.mergeDerivedReadDaily
// (a Go-computed UTC-midnight cutoff parameter, no SQL interval
// arithmetic). A missing-schema·failed lookup of read_events is not
// treated as read=0 but quietly (0, false). A corrupted flat value
// (non-int) is also quietly 0 as before.
func recallStarvedToday(ctx context.Context, st wire.Store, th store.Thresholds) (writes int, starved bool) {
	if th.RecallWarnWrites <= 0 {
		return 0, false
	}
	day := time.Now().UTC().Format("2006-01-02")
	getKV, _ := metaKV(ctx, st)
	v, err := getKV("daily:write:" + day)
	if err != nil {
		return 0, false
	}
	if writes, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
		return 0, false // corrupted flat value — quietly 0
	}
	if writes < th.RecallWarnWrites {
		return writes, false // under the threshold — no warning without a read lookup
	}
	cutoff, cerr := time.Parse("2006-01-02", day)
	if cerr != nil {
		return 0, false
	}
	rows, qerr := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM read_events WHERE ts >= $1 AND ts < $2`,
		[]any{cutoff, cutoff.AddDate(0, 0, 1)})
	if qerr != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0, false
	}
	var reads int
	if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &reads); n != 1 {
		return 0, false
	}
	return writes, reads == 0
}

// AutoBackupFn — the seam. Real action by default; fast unit tests swap
// in a no-op (real-action verification is done by e2e ④ against a real
// DB·real dump). v0.39.0 T4 export: upper-cased because a seam swap must
// be a package var owned by the implementation to propagate (the residual
// cli done_test stubs knowledge.AutoBackupFn directly).
var AutoBackupFn = maybeAutoBackup

// maybeAutoBackup — the once-a-day automatic backup (spec §8 "lazy trigger
// on the first session of the day"). Runs only after the done verdict;
// the output·exit code are fully invariant and failures are quietly
// ignored as at most one stderr line (non-interference §13).
// Meta access goes through the injected st.
func maybeAutoBackup(ctx context.Context, st wire.Store) {
	if st == nil {
		return
	}
	dir := os.Getenv("FLINT_BACKUP_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".flint", "backup")
	}

	// Meta access is delegated to the GetMetaKVErr/SetMetaKV single
	// entries (keeping the key-absent ("", nil)·transport-error
	// distinction contract).
	metaGet := func(key string) (string, error) {
		v, _, err := wire.GetMetaKVErr(ctx, st, key)
		return v, err
	}
	// Automatic-backup claiming is CAS — an atomic race with parallel
	// sessions.
	claimKV := func(oldVal, newVal string) (bool, error) {
		return wire.CompareSetMetaKV(ctx, st, "last_backup", oldVal, newVal)
	}
	lf := &lazyDumpFile{dir: dir}
	// With meta backup_encrypt=true, the automatic backup is also
	// encrypted (the key lives in the config directory — auto-generated on
	// first encryption). A read failure defaults to plaintext
	// (non-interference §13 — keeping the contract that a backup failure
	// never breaks the exit).
	if v, ok := wire.GetMetaKV(ctx, st, "backup_encrypt"); ok && wire.BackupEncryptOn(v) {
		lf.encrypt = true
	}
	defer lf.close()
	auto := backup.Auto{DSN: wire.DSNFromEnv()}
	runErr := auto.RunIfNeeded(ctx, metaGet, claimKV, lf)
	// The automatic backup also rotates — the keep-N policy applies after
	// the file is finalized (sidecar included). Previously it was bound
	// only to the manual `flint backup --out` path (rotateAfterBackup), so
	// automatic files accumulated indefinitely. Failures are warnings only
	// (the successful backup is invariant — the same contract as the
	// manual path).
	if runErr == nil {
		if name := lf.closeAndPath(); name != "" {
			if _, rerr := rotateFn(dir, keepFn(metaGet)); rerr != nil {
				fmt.Fprintf(os.Stderr, "[flint] backup auto rotate: %v\n", rerr)
			}
		}
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup auto: %v\n", runErr)
	}
}

// metaKV — assembles the meta-table getKV/setKV closure pair. The lookup·
// write bodies go directly through the wire.GetMetaKVErr·wire.SetMetaKV
// single entries (the same pattern as ops.MetaKV; these closures keep only
// the error-distinction contract). getKV returns ("", nil) on key absence;
// transport errors return err as-is.
func metaKV(ctx context.Context, st wire.Store) (func(string) (string, error), func(string, string) error) {
	getKV := func(key string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return "", err
		}
		return v, nil
	}
	setKV := func(key, value string) error {
		return wire.SetMetaKV(ctx, st, key, value)
	}
	return getKV, setKV
}

// markerKindDistill·markerKindRecall — the marker kinds (same values as
// the cli/marker.go-owned constants — the pending-distill.json·
// recall-hint.json filename contract).
const (
	markerKindDistill = "distill"
	markerKindRecall  = "recall"
)

// nudgeReasonRecall — the Reason value of the done recall-absence marker
// (same as the cli/nudge.go-owned constant — diagnostics display).
const nudgeReasonRecall = "recall"

// WriteTypedMarkerFn — the marker-writing seam (the cli marker.go-owned
// implementation is injected — knowledge cannot import cli.
// cli/knowledge_curation_alias.go init injects it. Calls without injection
// (unit tests) are quietly ignored — a missing marker is the same
// treatment as the non-interference §13 paths).
var WriteTypedMarkerFn func(kind, sessionID, reason string) error

// writeTypedMarker — the marker-writing wrapper through the seam.
func writeTypedMarker(kind, sessionID, reason string) error {
	if WriteTypedMarkerFn == nil {
		return nil
	}
	return WriteTypedMarkerFn(kind, sessionID, reason)
}

// BackupKeyPathFn — the encryption-key path seam (cli BackupKeyPath — the
// dataFile infrastructure is cli-owned). cli init injects it.
var BackupKeyPathFn func() string

// Backup helper seams — the backup package's real implementations as
// defaults, identical to the cli/backup.go-owned vars (inheriting the
// test-swap convention).
var (
	rotateFn    = backup.Rotate
	keepFn      = backup.BackupKeep
	ensureKeyFn = backup.EnsureIdentity
)

// lazyDumpFile — a lazy writer that creates the dump file only on the
// first Write. Prevents leaving an empty file when RunIfNeeded is a
// no-op due to non-elapsed time. The written bytes are summed through a
// sha256 tee and close leaves a .sha256 sidecar (applying ADR-0006
// integrity verification to automatic files too). With encrypt=true, age
// encryption — the chain is plaintext→age writer→hashFile (file + sha256
// tee), so the hash·sidecar are over the ciphertext.
type lazyDumpFile struct {
	dir     string
	encrypt bool
	once    sync.Once
	f       *os.File
	h       hash.Hash
	hw      *hashFile
	aw      io.WriteCloser // age wrapping (when encrypt)
	failed  bool
}

// hashFile — file writing + a simultaneous sha256 tee (the sink).
type hashFile struct {
	f *os.File
	h hash.Hash
}

func (w *hashFile) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	if n > 0 {
		w.h.Write(p[:n])
	}
	return n, err
}

func (l *lazyDumpFile) Write(p []byte) (int, error) {
	l.once.Do(func() {
		if err := os.MkdirAll(l.dir, 0o700); err != nil { // v0.20.6 S3: dumps are sensitive plaintext — 0700
			l.failed = true
			return
		}
		ext := ".sql"
		if l.encrypt {
			ext = ".sql.age"
		}
		// Key acquisition before file creation — so a key failure leaves no
		// partial file.
		var recipient *age.X25519Recipient
		if l.encrypt {
			if BackupKeyPathFn == nil {
				l.failed = true
				return
			}
			key := BackupKeyPathFn()
			if key == "" {
				l.failed = true
				return
			}
			id, err := ensureKeyFn(key)
			if err != nil {
				l.failed = true
				return
			}
			recipient = id.Recipient()
		}
		// PID in the file name — so parallel sessions exiting in the same
		// second never mutually overwrite via O_TRUNC (mixed contents).
		name := filepath.Join(l.dir,
			"flint-"+time.Now().UTC().Format("20060102-150405")+"-"+strconv.Itoa(os.Getpid())+ext)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			l.failed = true
			return
		}
		l.f = f
		l.h = sha256.New()
		l.hw = &hashFile{f: f, h: l.h}
		if l.encrypt {
			aw, aerr := age.Encrypt(l.hw, recipient)
			if aerr != nil {
				l.failed = true
				return
			}
			l.aw = aw
		}
	})
	if l.failed {
		return 0, errors.New("backup auto: cannot create dump file")
	}
	// Mark failed on body-write failure — blocking the false-positive
	// state where a partial file received a normal sidecar 'matching its
	// contents' and pushed a sound old backup out of rotation.
	if l.aw != nil {
		n, err := l.aw.Write(p)
		if err != nil {
			l.failed = true
		}
		return n, err
	}
	n, err := l.hw.Write(p)
	if err != nil {
		l.failed = true
		return n, err
	}
	return n, nil
}

// close — writes the .sha256 sidecar after a successful dump close. If
// once never fired (a 0-byte no-op), neither file nor sidecar is created.
// Sidecar failure logs a warning and proceeds (non-blocking — done
// return·exit invariant). With age wrapping, the age writer is finalized
// (final flush) before the file close — a finalization failure makes the
// partial file a discard candidate.
func (l *lazyDumpFile) close() error {
	if l.f == nil {
		return nil
	}
	name := l.f.Name()
	if l.aw != nil {
		if aerr := l.aw.Close(); aerr != nil {
			l.failed = true // age finalization failure — partial ciphertext
			_ = l.f.Close()
			l.f = nil
			_ = os.Remove(name)
			return errors.New("backup auto: age close failed — partial dump discarded")
		}
		l.aw = nil
	}
	closeErr := l.f.Close()
	l.f = nil // idempotent — double-close defense for the deferred close after closeAndPath
	// A partial file whose body write failed is removed without a sidecar
	// — leaving no false-positive backup slot that passes the checksum.
	// Discarding takes precedence over a double-close error (already
	// closed via failure injection).
	if l.failed {
		_ = os.Remove(name)
		return errors.New("backup auto: partial dump discarded")
	}
	if closeErr != nil {
		return closeErr
	}
	sum := hex.EncodeToString(l.h.Sum(nil))
	if err := backup.WriteSidecar(name, sum); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup sidecar: %v\n", err)
		return fmt.Errorf("sidecar: %w", err)
	}
	return nil
}

// closeAndPath — finalizes the file (close·sidecar) and returns its path.
// An empty string for a no-op that wrote nothing. The interface by which
// the automatic backup rotation obtains the finalized path (the later
// deferred close is a no-op via l.f=nil).
func (l *lazyDumpFile) closeAndPath() string {
	if l.f == nil {
		return ""
	}
	name := l.f.Name()
	if err := l.close(); err != nil {
		return ""
	}
	return name
}

func init() {
	wire.Register("done", RunDone)
}

// BumpMetaCounter — increments a meta counter by 1 (a done recall-counter
// utility). Failures are quiet (§13 — metering principle). v0.34.3 T2: the
// increment body is delegated to the BumpMetaKV single entry — the
// unguarded ::bigint cast used to freeze the increment forever on a
// corrupted value, recovered by the single implementation's corrupted-
// value reset-to-1 guard. v0.39.0 T4 export: residual cli consumers
// (meta_kv_test) call it via the alias. v1.2.0 T4: a nil st (a
// network-oriented hook path — a no-store nudge call) is also quietly
// skipped (preventing a panic on a nil-interface call to BumpMetaKV).
func BumpMetaCounter(ctx context.Context, st wire.Store, key string) {
	if st == nil {
		return
	}
	_ = wire.BumpMetaKV(ctx, st, key)
}

// doneStdinSummary — extracts compact_summary from the Stop hook stdin
// JSON. Parse failure·absence yield the empty value — keeping the
// skip-fallback-boilerplate contract.
func doneStdinSummary(stdin io.Reader) string {
	if stdin == nil {
		return ""
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return ""
	}
	var h struct {
		CompactSummary string `json:"compact_summary"`
	}
	if json.Unmarshal(b, &h) != nil {
		return ""
	}
	return strings.TrimSpace(h.CompactSummary)
}
