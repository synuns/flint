// registry — the subcommand registration table (the contract layer). So each file of
// the command-implementation package (lib/cli) only needs to Register in init (open-closed).
package wire

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// Handler — the subcommand execution function. Run calls it after connecting the store.
type Handler func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int

// registry — command name -> handler. Registration happens only at init/startup, so no locking is needed.
var registry = map[string]Handler{}

// Register — registers a command. Re-registering the same name overwrites with the latest value (for test swaps).
func Register(name string, h Handler) {
	registry[name] = h
}

// Lookup — looks up a registered handler. false when unregistered.
func Lookup(name string) (Handler, bool) {
	h, ok := registry[name]
	return h, ok
}

// Unregister — for test cleanup (not a production path).
func Unregister(name string) {
	delete(registry, name)
}

// usageOrder — the command order listed in the usage sentence (assembled from this slice).
// A new command must be added here as well
// as Register to be exposed in usage
// (TestUsageOrderMatchesRegistry catches the drift).
// Constraints: "audit|diff|update|tuning|import" keep their item order and fixed tail
// (the wire_v08 TestUsageRestInvariant contract); the "done|compact|nudge",
// "approve|pending", and "hot|brief" orders are likewise held by the existing wiring tests.
// sync (a human-operator command — the data-portability group,
// right after backup/restore. The remote url is never recorded in output or logs, by contract).
var usageOrder = []string{
	"write", "read", "hot", "brief", "status", "profile", "backup", "restore",
	"sync",
	"approve", "pending", "maintain", "doctor", "report", "export",
	"done", "compact", "nudge", "setup", "teardown", "version", "mcp",
	"audit", "diff", "update", "tuning", "import",
}

// hiddenFromUsage — the hook-only entry points registered in the registry but not
// exposed in the usage sentence (called by hooks, not human-facing commands).
func hiddenFromUsage(name string) bool {
	return name == "session-start" || name == "session-end"
}

// UsageLine — the shared one-line JSON for no-args/help. Assembled in usageOrder order.
// The error key is abolished — usage is guidance, not an error, so it is
// delivered only as `{"usage":...}` (the informational content and usageOrder assembly unchanged).
func UsageLine() string {
	return `{"usage":"flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
}

// UnknownCommandLine — the unknown-command guidance as one line of JSON:
// in the guidance form without an error key, it keeps the unknown-command fact
// (message) and the usage information (the command list). Since cmd is a
// caller-controlled string, it is assembled via json.Marshal escaping, not raw
// concatenation (the same rationale as the ErrLine marshaling convention).
func UnknownCommandLine(cmd string) string {
	b, _ := json.Marshal(map[string]string{
		"message": "unknown command: " + cmd,
		"usage":   "flint <" + strings.Join(usageOrder, "|") + "> [args]",
	})
	return string(b)
}

// Registered — enumerates the registered command names (sorted — for stable bridge-guard comparisons).
// A read-only accessor for the cli-side homogeneity guard
// (TestRegisteredMatchesUsage) to read the actually registered surface.
func Registered() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UsageOrder — a copy of the usageOrder slice (read access for external guards).
func UsageOrder() []string {
	return append([]string(nil), usageOrder...)
}

// HiddenFromUsage — public access for the hook-only-entry determination (the usage-exposure-control contract).
func HiddenFromUsage(name string) bool {
	return hiddenFromUsage(name)
}
