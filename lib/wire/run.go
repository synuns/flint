package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/store"
)

func DSNFromEnv() string {
	return ResolveDSN()
}

func Run(args []string, stdin io.Reader, stdout io.Writer, newStore func(ctx context.Context, dsn string) (Store, error)) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, UsageLine())
		return 1
	}
	cmd, rest := args[0], args[1:]

	if cmd == "--version" || cmd == "-v" {
		cmd = "version"
	}

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		return RunHelp(stdout)
	}
	run, ok := Lookup(cmd)
	if !ok {

		fmt.Fprintln(stdout, UnknownCommandLine(cmd))
		return 3
	}
	ctx := context.Background()

	if cmd == "version" || cmd == "teardown" || cmd == "mcp" || cmd == "update" || cmd == "seed" {
		return run(ctx, nil, rest, stdin, stdout)
	}

	if cmd == "setup" {
		var st Store
		if s, err := newStore(ctx, DSNFromEnv()); err == nil {
			st = s
		}
		return run(ctx, st, rest, stdin, stdout)
	}
	st, err := newStore(ctx, DSNFromEnv())
	if err != nil {

		if cmd == "compact" {
			return run(ctx, nil, rest, stdin, stdout)
		}

		if cmd == "nudge" {
			return store.MapExit(err)
		}
		fmt.Fprintln(stdout, ErrLine(err))
		return store.MapExit(err)
	}

	defer st.Close()
	return run(ctx, st, rest, stdin, stdout)
}

func RunHelp(w io.Writer) int {
	fmt.Fprintln(w, UsageLine())
	fmt.Fprintln(w, HelpSummary())
	return 0
}

func HelpSummary() string {
	return "write capture · read search · hot injection · brief external-AI briefing · status state · profile exposure · pending review · doctor diagnostics · report observability · export extraction (4 formats · pam standard · --out sha256 sidecar) · distill insight extraction (recall-scored markdown · --out local folder · row-level idempotence) · import migration (jsonl·pam) · backup/restore backups · sync remote knowledge sync (push/pull · operator command) · maintain upkeep (--supersede handover · --decay-audit archive candidates · --merge-audit duplicate pair report · --recall-audit bad-recall feedback report · --reaffirm validity refresh · --recheck conditions review · --gc engine garbage collection [--shallow] --yes — disconnects sessions · --purge-dropped dropped-database purge (dry-run default · loopback-only · --yes applies — irreversible) · approve promotion · tuning thresholds (auto mode off|shadow — shadow predict-only) · update self-update · seed demo data (--demo · isolated _demo database · --reset --yes) · done/compact/nudge hooks · setup/teardown install · audit/diff time travel · mcp server · version · index vault indexing (markdown vault snapshot diff · --vault/--limit)"
}

type UsageError struct{ err error }

func (u *UsageError) Error() string { return u.err.Error() }
func (u *UsageError) Unwrap() error { return u.err }

func UsageErrorf(format string, a ...any) *UsageError {
	return &UsageError{err: fmt.Errorf(format, a...)}
}

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
