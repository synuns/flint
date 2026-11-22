// run.go — the contract-layer dispatch.
// Store-factory injected: the cli wrapper passes the cli.newStore seam.
package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/store"
)

// DSNFromEnv — the DSN resolution entry point. Delegates to ResolveDSN:
// FLINT_DSN > config.json (LoadConfigDSN) > the default.
func DSNFromEnv() string {
	return ResolveDSN()
}

// Run executes the command from args, writes to stdout, and returns the exit code.
func Run(args []string, stdin io.Reader, stdout io.Writer, newStore func(ctx context.Context, dsn string) (Store, error)) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, UsageLine())
		return 1
	}
	cmd, rest := args[0], args[1:]
	// the --version/-v global flag alias — rewrites `flint --version` and
	// `flint -v` up front to the same output and exit 0 as `flint version`.
	// Being a pre-dispatch step rather than a command-table registration, it does not
	// conflict with the unknown-command exit 3 contract (a typo like `--versio` is still unknown 3).
	if cmd == "--version" || cmd == "-v" {
		cmd = "version"
	}
	// help is also a store-free fast path: printed directly before connecting,
	// not via a registry handler. Arguments (`help foo`) are ignored.
	// the -h/--help global alias — the same
	// output and exit 0 as --version/-v. A typo (--hel) is not an alias but unknown command 3.
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		return RunHelp(stdout)
	}
	run, ok := Lookup(cmd)
	if !ok {
		// name escaping — raw concatenation could corrupt the JSON structure itself
		// with a caller-controlled string (ErrLine also goes through marshaling —
		// UnknownCommandLine inherits this via json.Marshal).
		// unknown command is also unified to the invalid-arg contract exit 3 —
		// so an agent can self-correct a command typo. The output uses the
		// guidance form without an error key (message + usage, informational content kept) — the
		// contract that usage guidance is not an error (the same context as the UsageLine
		// transition). Exit 3 and the ErrLine usage classification (the command-argument-error path) are unchanged.
		fmt.Fprintln(stdout, UnknownCommandLine(cmd))
		return 3
	}
	ctx := context.Background()
	// version, teardown, and mcp are store-free — they respond immediately even
	// without a running DB (§13: for mcp, each tool call connects its own store
	// via cli.Run). nudge goes through the store — nudgeProfile looks up
	// exposure_profile to honor the minimal contract (a legacy store-free version
	// could not auto-conclude minimal).
	if cmd == "version" || cmd == "teardown" || cmd == "mcp" || cmd == "update" {
		return run(ctx, nil, rest, stdin, stdout)
	}
	// setup also responds without a store, since DB-down is its main scenario —
	// but with a normally running DB, the ping and migrate steps must actually run.
	// Only after a connection attempt fails does it proceed with
	// nil — setup's nil guard remains valid as the DB-down guidance path.
	if cmd == "setup" {
		var st Store
		if s, err := newStore(ctx, DSNFromEnv()); err == nil {
			st = s
		}
		return run(ctx, st, rest, stdin, stdout)
	}
	st, err := newStore(ctx, DSNFromEnv())
	if err != nil {
		// compact is a hook-entry command — even without a DB it silently exits 0 (§3 non-interference).
		// Run with st nil makes runCompact return a no-op 0.
		if cmd == "compact" {
			return run(ctx, nil, rest, stdin, stdout)
		}
		// nudge is also a hook-entry command — it does not leak an error JSON to stdout
		// and ends with only the quiet MapExit code (server down = 2, the hook quiet contract).
		if cmd == "nudge" {
			return store.MapExit(err)
		}
		fmt.Fprintln(stdout, ErrLine(err))
		return store.MapExit(err)
	}
	// closing the store after the command — a one-shot CLI had process exit to
	// reclaim it, but this closes the leak where flint mcp (calling cli.Run per
	// tools/call) accumulated 2 pgx pools permanently in a long-lived process.
	defer st.Close()
	return run(ctx, st, rest, stdin, stdout)
}

// RunHelp — the `flint help` output. 1 usage JSON line + 1 command-summary line, exit 0.
// help takes no arguments — leftover arguments are ignored.
func RunHelp(w io.Writer) int {
	fmt.Fprintln(w, UsageLine())
	fmt.Fprintln(w, HelpSummary())
	return 0
}

// HelpSummary — the per-command 1-line summaries (including the keywords of
// every usageOrder item — the TestHelpSummaryCoversUsageOrder completeness guard).
func HelpSummary() string {
	return "write capture · read search · hot injection · brief external-AI briefing · status state · profile exposure · pending review · doctor diagnostics · report observability · export extraction (4 formats · pam standard · --out sha256 sidecar) · distill insight extraction (recall-scored markdown · --out local folder · row-level idempotence) · import migration (jsonl·pam) · backup/restore backups · sync remote knowledge sync (push/pull · operator command) · maintain upkeep (--supersede handover · --decay-audit archive candidates · --reaffirm validity refresh · --recheck conditions review) · approve promotion · tuning thresholds · update self-update · done/compact/nudge hooks · setup/teardown install · audit/diff time travel · mcp server · version · index vault indexing (markdown vault snapshot diff · --vault/--limit)"
}

// UsageError — the invalid-argument (unknown flag, missing value, etc.)
// classification marker (extending the ErrLine error value from "internal" to "usage" —
// structure unchanged, downstream-parser compatible). Wraps the original; Error and Unwrap delegate.
type UsageError struct{ err error }

func (u *UsageError) Error() string { return u.err.Error() }
func (u *UsageError) Unwrap() error { return u.err }

// UsageErrorf — assembles a UsageError (the fmt.Errorf convention — %w wrapping supported).
func UsageErrorf(format string, a ...any) *UsageError {
	return &UsageError{err: fmt.Errorf(format, a...)}
}

// ErrLine — the error as one line of JSON. Server down is silent, one line only (server_down).
func ErrLine(err error) string {
	m := map[string]string{"error": "internal", "message": err.Error()}
	var se *store.SchemaError
	var ue *UsageError
	switch {
	case errors.As(err, &se):
		m = map[string]string{"error": "schema", "field": se.Field, "expect": se.Expect}
	case errors.As(err, &ue):
		m = map[string]string{"error": "usage", "message": ue.Error()}
	case errors.Is(err, store.ErrServerDown):
		m = map[string]string{"error": "server_down"}
	case errors.Is(err, store.ErrRetriesExhausted):
		m = map[string]string{"error": "retries_exhausted"}
	case errors.Is(err, store.ErrDuplicate):
		m = map[string]string{"error": "duplicate"}
	}
	b, _ := json.Marshal(m)
	return string(b)
}
