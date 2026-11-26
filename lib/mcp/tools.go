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

type ToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  map[string]any  `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

const (
	readOutputSchema    = `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"topic":{"type":"string"},"summary":{"type":"string"}}}},"hits":{"type":"number"},"error":{"type":"string"}}}`
	writeOutputSchema   = `{"type":"object","properties":{"status":{"type":"string"},"hash":{"type":"string"},"state":{"type":"string"},"id":{"type":"integer"}}}`
	hotOutputSchema     = `{"type":"object","properties":{}}`
	pendingOutputSchema = `{"type":"object","properties":{"status":{"type":"string"},"items":{"type":"array"},"counts":{"type":"object"},"next":{"type":"string"}}}`
	approveOutputSchema = `{"type":"object","properties":{"status":{"type":"string"},"id":{"type":"integer"}}}`
	workOutputSchema    = `{"type":"object","properties":{"status":{"type":"string"},"id":{"type":"integer"},"results":{"type":"array"}}}`
)

var runFn = func(cliArgs []string, stdin io.Reader, stdout io.Writer) int {
	return runWithStoreFn(cliArgs, stdin, stdout, serverStore())
}

var runWithStoreFn = cli.RunWithStore

var storeSupply = cli.OpenServerStore

var (
	storeMu     sync.Mutex
	cachedStore wire.Store
)

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

func ToolsManifest() []ToolDef {
	return []ToolDef{
		{
			Name:        "flint_read",
			Description: "Search active knowledge (pool mode). Filter by query/tag/origin, confidence floor, limit up to 50.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"tag":   map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "maximum": 50},

					"min_confidence": map[string]any{"type": "number", "minimum": float64(0), "maximum": float64(1)},
					"origin":         map[string]any{"type": "string", "enum": []string{"agent", "human"}},
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

			Name:        "flint_hot",
			Description: "Hot summary for injection (2,000-char cap). All recent items (no session filter).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			OutputSchema: json.RawMessage(hotOutputSchema),
		},
		{

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
					"claim":     map[string]any{"type": "string", "enum": []string{"decision", "procedure", "lesson", "event"}, "description": "required to promote a row whose claim is unset (backfill=event)"},
					"supersede": map[string]any{"type": "integer", "description": "old active id to replace (optional)"},
				},
				"required": []string{"id"},
			},
			OutputSchema: json.RawMessage(approveOutputSchema),
		},
		{
			Name:        "flint_work",
			Description: "Track in-progress work state. action=open records task progress (server-side hash absorbs re-reports into the same open row), action=update is an alias, action=resolve closes by id (optional by=knowledge id), action=list returns open states (all=true includes resolved).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"action"},
				"properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"open", "update", "resolve", "list"}},
					"task":    map[string]any{"type": "string", "description": "required for open/update"},
					"detail":  map[string]any{"type": "string", "description": "attempt/failure context"},
					"next":    map[string]any{"type": "string", "description": "next action"},
					"project": map[string]any{"type": "integer", "description": "project id"},
					"id":      map[string]any{"type": "integer", "description": "work state id, required for resolve"},
					"by":      map[string]any{"type": "integer", "description": "knowledge id linking the resolution"},
					"all":     map[string]any{"type": "boolean", "description": "list resolved rows too"},
				},
			},
			OutputSchema: json.RawMessage(workOutputSchema),
		},
	}
}

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
	case "flint_work":
		return dispatchWork(args)
	default:

		if b, err := json.Marshal(map[string]string{"error": "unknown tool: " + name}); err == nil {
			return string(b), 1
		}
		return `{"error":"unknown tool"}`, 1
	}
}

func dispatchRead(args map[string]any) (string, int) {

	cliArgs := []string{"--source", "mcp"}
	if tag := strArg(args, "tag"); tag != "" {
		cliArgs = append(cliArgs, "--tag", tag)
	}
	if n, ok := intArg(args, "limit"); ok {
		cliArgs = append(cliArgs, "--limit", strconv.Itoa(query.ClampLimit(n)))
	}
	if v, ok := args["min_confidence"].(float64); ok {
		cliArgs = append(cliArgs, "--min-confidence", strconv.FormatFloat(v, 'f', -1, 64))
	}
	if o := strArg(args, "origin"); o != "" {
		cliArgs = append(cliArgs, "--origin", o)
	}
	if q := strArg(args, "query"); q != "" {
		cliArgs = append(cliArgs, "--", q)
	}
	return runCall(append([]string{"read", "--json"}, cliArgs...), strings.NewReader(""))
}

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
			return nil
		}
		results = append(results, m)
	}
	return map[string]any{"results": results}
}

func isZeroHitLine(m map[string]any) bool {
	if len(m) != 1 {
		return false
	}
	_, has := m["hits"]
	return has
}

func isErrorLine(m map[string]any) bool {
	_, has := m["error"]
	return has
}

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

func dispatchApprove(args map[string]any) (string, int) {
	id, ok := intArgStrict(args, "id")
	if !ok || id < 1 {
		return `{"error":"schema","field":"id","expect":"positive integer"}`, 3
	}
	cliArgs := []string{"approve", "--id", strconv.Itoa(id)}

	if claim := strArg(args, "claim"); claim != "" {
		cliArgs = append(cliArgs, "--claim", claim)
	}

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

const writeKindExpect = store.KindExpect

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

func dispatchHot(args map[string]any) (string, int) {
	return runCall([]string{"hot"}, strings.NewReader(""))
}

func dispatchWork(args map[string]any) (string, int) {
	switch action := strArg(args, "action"); action {
	case "open", "update":
		task := strArg(args, "task")
		if task == "" {
			return `{"error":"schema","field":"task","expect":"required for action ` + action + `"}`, 3
		}
		cliArgs := []string{"state", "open", "--task", task}
		if d := strArg(args, "detail"); d != "" {
			cliArgs = append(cliArgs, "--detail", d)
		}
		if n := strArg(args, "next"); n != "" {
			cliArgs = append(cliArgs, "--next", n)
		}
		if id, ok := intArgStrict(args, "project"); ok && id > 0 {
			cliArgs = append(cliArgs, "--project", strconv.Itoa(id))
		}
		return runCall(cliArgs, strings.NewReader(""))
	case "resolve":
		id, ok := intArgStrict(args, "id")
		if !ok || id < 1 {
			return `{"error":"schema","field":"id","expect":"positive integer"}`, 3
		}
		cliArgs := []string{"state", "resolve", "--id", strconv.Itoa(id)}
		if by, ok := intArgStrict(args, "by"); ok && by > 0 {
			cliArgs = append(cliArgs, "--by", strconv.Itoa(by))
		}
		return runCall(cliArgs, strings.NewReader(""))
	case "list":
		cliArgs := []string{"state", "list"}
		if b, ok := args["all"].(bool); ok && b {
			cliArgs = append(cliArgs, "--all")
		}
		return runCall(cliArgs, strings.NewReader(""))
	default:
		return `{"error":"schema","field":"action","expect":"open|update|resolve|list"}`, 3
	}
}

func runCall(cliArgs []string, stdin io.Reader) (string, int) {
	var out bytes.Buffer
	code := runFn(cliArgs, stdin, &out)
	return strings.TrimRight(out.String(), "\n"), code
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func intArg(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}
