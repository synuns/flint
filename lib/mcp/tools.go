// tools.go — the five flint mcp tools. Dispatch assembles cli.Run arguments
// and calls it, returning the CLI output string and exit code (the 0/1/2/3
// contract) as-is (real DB and contract reuse).
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ToolDef — one MCP tools/list entry. InputSchema is a JSON Schema object;
// OutputSchema (v1.18.0, JSON Schema 2020-12) declares the actual output
// shape so modern clients can validate structuredContent.
type ToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  map[string]any  `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// Output schemas (v1.18.0 T1) — declared against the ACTUAL tool outputs,
// not the aspirational ones: write echoes {status,hash,state,id} (duplicate
// responses reuse the status value), pending is {status,items,counts,next},
// approve is {status,id}, read wraps rows in a results object (R67);
// zero-hit stays the {hits:0} object), and hot is non-JSON text (minimal
// meta only — no structuredContent for hot).
const (
	readOutputSchema    = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"topic":{"type":"string"},"summary":{"type":"string"}}}},"hits":{"type":"number"},"error":{"type":"string"}}}`
	writeOutputSchema   = `{"type":"object","properties":{"status":{"type":"string"},"hash":{"type":"string"},"state":{"type":"string"},"id":{"type":"integer"}}}`
	hotOutputSchema     = `{"type":"object","properties":{}}`
	pendingOutputSchema = `{"type":"object","properties":{"status":{"type":"string"},"items":{"type":"array"},"counts":{"type":"object"},"next":{"type":"string"}}}`
	approveOutputSchema = `{"type":"object","properties":{"status":{"type":"string"},"id":{"type":"integer"}}}`
)

// runFn — a seam. The default is the server-lifetime store-reusing runner:
// it injects the cached store (or nil when supply fails) into
// cli.RunWithStore. The nil fallback is handled quietly inside RunWithStore
// on the path equivalent to the existing cli.Run. Tests inject a fake
// (SUPERLITE_REAL=1 delegates to the real implementation).
var runFn = func(cliArgs []string, stdin io.Reader, stdout io.Writer) int {
	return runWithStoreFn(cliArgs, stdin, stdout, serverStore())
}

// runWithStoreFn — the injected-runner seam (B4). Defaults to the real cli.RunWithStore.
var runWithStoreFn = cli.RunWithStore

// storeSupply — the server-lifetime store supply seam (B4). Defaults to
// cli.OpenServerStore (one open including DSN resolution). Tests inject a
// counting fake.
var storeSupply = cli.OpenServerStore

// Server-lifetime store cache (B4). An MCP server is a long-lived process —
// instead of reopening the store per tool call, it opens once and reuses.
// storeMu guards the initialization race under concurrent tool calls.
var (
	storeMu     sync.Mutex
	cachedStore wire.Store
)

// serverStore — returns the cached server-lifetime store (opened once). On
// supply failure or nil it returns nil — the default runFn path then follows
// the existing contract via the RunWithStore(nil) fallback (open the store
// per call, propagate failure, quietly). Disconnect recovery is delegated
// to reconnection in the store's internal pgx pool (the cache never drops
// the entry).
func serverStore() wire.Store {
	storeMu.Lock()
	defer storeMu.Unlock()
	if cachedStore == nil {
		st, err := storeSupply()
		if err != nil || st == nil {
			return nil
		}
		cachedStore = st
	}
	return cachedStore
}

// ToolsManifest — the five tool definitions (read, write, hot, pending, approve).
func ToolsManifest() []ToolDef {
	return []ToolDef{
		{
			Name:        "flint_read",
			Description: "Search active knowledge (pool mode). Filter by query/tag, limit up to 50.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"tag":   map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "maximum": 50},
				},
			},
			OutputSchema: json.RawMessage(readOutputSchema),
		},
		{
			Name:        "flint_write",
			Description: "Record a decision or knowledge item (kind+payload, idempotent: identical payload returns duplicate).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"kind", "payload"},
				"properties": map[string]any{
					"kind":    map[string]any{"type": "string", "enum": []string{"knowledge", "session", "project"}},
					"payload": map[string]any{"type": "object"},
				},
			},
			OutputSchema: json.RawMessage(writeOutputSchema),
		},
		{
			// No session_id parameter — real-session filtering is excluded.
			Name:        "flint_hot",
			Description: "Hot summary for injection (2,000-char cap). All recent items (no session filter).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			OutputSchema: json.RawMessage(hotOutputSchema),
		},
		{
			// Review loop — inspect and promote waiting knowledge within the session.
			Name:         "flint_pending",
			Description:  "Pending knowledge list + rule-based suggestions. verdict is promote|merge|expire|hold. Returns JSON.",
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
			OutputSchema: json.RawMessage(pendingOutputSchema),
		},
		{
			Name:        "flint_approve",
			Description: "Promote pending knowledge. With supersede, the old active item is replaced by the new one.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        map[string]any{"type": "integer", "description": "pending id to promote"},
					"supersede": map[string]any{"type": "integer", "description": "old active id to replace (optional)"},
				},
				"required": []string{"id"},
			},
			OutputSchema: json.RawMessage(approveOutputSchema),
		},
	}
}

// DispatchTool — assembles the tool name and arguments into a cli.Run call
// and executes it. Returns (one line of CLI output, exit code). Unknown
// tools yield exit 1.
func DispatchTool(name string, args map[string]any) (string, int) {
	switch name {
	case "flint_read":
		return dispatchRead(args)
	case "flint_write":
		return dispatchWrite(args)
	case "flint_hot":
		return dispatchHot(args)
	case "flint_pending":
		return runCall([]string{"pending", "--json"}, strings.NewReader(""))
	case "flint_approve":
		return dispatchApprove(args)
	default:
		// Escape the name (prevents raw-concatenation JSON corruption).
		if b, err := json.Marshal(map[string]string{"error": "unknown tool: " + name}); err == nil {
			return string(b), 1
		}
		return `{"error":"unknown tool"}`, 1
	}
}

// dispatchRead — assembles flint read [query] --json --tag --limit. The
// limit bakes the token budget (ClampLimit cap of 50) into the tool path
// too. v1.18.0 T3: --json is assembled internally so the MCP read path
// always returns machine-readable rows (JSONL), which structuredContent then
// parses into {results:[...]}. query is external input, so it goes after
// "--" to keep it from being parsed as a flag (runRead's end-of-options
// rule).
func dispatchRead(args map[string]any) (string, int) {
	// MCP path label — metering read_events.source='mcp'.
	cliArgs := []string{"--source", "mcp"}
	if tag := strArg(args, "tag"); tag != "" {
		cliArgs = append(cliArgs, "--tag", tag)
	}
	if n, ok := intArg(args, "limit"); ok {
		cliArgs = append(cliArgs, "--limit", strconv.Itoa(query.ClampLimit(n)))
	}
	if q := strArg(args, "query"); q != "" {
		cliArgs = append(cliArgs, "--", q)
	}
	return runCall(append([]string{"read", "--json"}, cliArgs...), strings.NewReader(""))
}

// structuredContent — derives the MCP structuredContent payload (2025-06-18+)
// from a tool's text output; nil means "content text only". The rules:
//   - one line that parses as a JSON object → that object as-is (the
//     write/approve/pending responses and the read zero-hit {"hits":0});
//   - multiple lines that each parse as a JSON object → per-line parsing
//     assembled into an array and wrapped as {results:[...]} (read's JSONL
//     rows — the spec MUSTs structuredContent to be an object, hence the
//     wrap);
//   - flint_read wraps even a single row (the read outputSchema advertises
//     a results object wrapping rows), except the zero-hit line and error lines which
//     stay as-is;
//   - anything else (non-JSON text such as flint_hot, arrays, scalars,
//     parse failures, empty output) → nil.
func structuredContent(tool, out string) map[string]any {
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return nil
	}
	if len(lines) == 1 {
		var m map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &m); err != nil || m == nil {
			return nil
		}
		if tool == "flint_read" && !isZeroHitLine(m) && !isErrorLine(m) {
			return map[string]any{"results": []any{m}}
		}
		return m
	}
	results := make([]any, 0, len(lines))
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m == nil {
			return nil // one non-object line: no structured content, text only
		}
		results = append(results, m)
	}
	return map[string]any{"results": results}
}

// isZeroHitLine — the read --json zero-hit line {"hits":0} (exactly one
// "hits" key). Kept as its own object rather than wrapped in results.
func isZeroHitLine(m map[string]any) bool {
	if len(m) != 1 {
		return false
	}
	_, has := m["hits"]
	return has
}

// isErrorLine — a one-line {error:...} response (every flint error prints
// one errLine). Surfaced as-is, not wrapped in results — error responses
// stay uniform across tools.
func isErrorLine(m map[string]any) bool {
	_, has := m["error"]
	return has
}

// nonEmptyLines — splits an output into its non-empty lines (runCall already
// trims trailing newlines; JSONL rows are one object per line).
func nonEmptyLines(out string) []string {
	raw := strings.Split(strings.TrimSpace(out), "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// dispatchApprove — flint approve --id N [--supersede OLD]. Review loop:
// trust gate (the same trust level as write — local stdio, single user).
// id must be a positive integer and supersede must differ from id — exit 3
// (the validation-failure contract).
func dispatchApprove(args map[string]any) (string, int) {
	id, ok := intArgStrict(args, "id")
	if !ok || id < 1 {
		return `{"error":"schema","field":"id","expect":"positive integer"}`, 3
	}
	cliArgs := []string{"approve", "--id", strconv.Itoa(id)}
	// When the supersede key exists, type/value errors must exit 3, not be
	// silently skipped — blocks the semantic drift where an LLM caller typo
	// like the string "3" runs the promotion alone.
	if _, has := args["supersede"]; has {
		sup, ok := intArgStrict(args, "supersede")
		if !ok {
			return `{"error":"schema","field":"supersede","expect":"integer (no decimal truncation)"}`, 3
		}
		if sup < 1 {
			return `{"error":"schema","field":"supersede","expect":"positive integer"}`, 3
		}
		if sup == id {
			return `{"error":"schema","field":"supersede","expect":"different from id"}`, 3
		}
		cliArgs = append(cliArgs, "--supersede", strconv.Itoa(sup))
	}
	return runCall(cliArgs, strings.NewReader(""))
}

// intArgStrict — an integer-strictness-checking intArg: a decimal (7.9)
// fails rather than truncating — preventing mis-targeted promotions and
// supersedes (a stricter validation contract, distinct from the lenient
// truncation of the read limit).
func intArgStrict(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// writeKindExpect — the set of kinds flint write accepts. Sourced from the
// exported lib/store constant KindExpect (a single-source reference).
// Server-side blocking of invalid kinds before reaching runFn.
const writeKindExpect = store.KindExpect

// dispatchWrite — flint write <kind> + payload JSON on stdin. dedup is
// delegated to the CLI pipeline, so tool calls are idempotent too. kind is
// whitelist-validated server-side (B5).
func dispatchWrite(args map[string]any) (string, int) {
	kind := strArg(args, "kind")
	if kind == "" {
		return `{"error":"schema","field":"kind","expect":"required"}`, 3
	}
	switch kind {
	case "knowledge", "session", "project":
	default:
		return `{"error":"schema","field":"kind","expect":"` + writeKindExpect + `"}`, 3
	}
	payload, ok := args["payload"]
	if !ok {
		return `{"error":"schema","field":"payload","expect":"required"}`, 3
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"error":"schema","field":"payload","expect":"valid JSON: %s"}`, err), 3
	}
	return runCall([]string{"write", kind, "--source", "mcp"}, bytes.NewReader(b))
}

// dispatchHot — assembles flint hot. Unknown arguments are ignored (the
// tool contract takes no arguments).
func dispatchHot(args map[string]any) (string, int) {
	return runCall([]string{"hot"}, strings.NewReader(""))
}

// runCall — runs runFn with the assembled arguments, returning the output with trailing newlines removed.
func runCall(cliArgs []string, stdin io.Reader) (string, int) {
	var out bytes.Buffer
	code := runFn(cliArgs, stdin, &out)
	return strings.TrimRight(out.String(), "\n"), code
}

// strArg — extracts a string argument from the map (JSON strings only).
func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// intArg — extracts an integer argument from the map. JSON decoding yields float64.
func intArg(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}
