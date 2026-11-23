package wire

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

type Handler func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int

var registry = map[string]Handler{}

func Register(name string, h Handler) {
	registry[name] = h
}

func Lookup(name string) (Handler, bool) {
	h, ok := registry[name]
	return h, ok
}

func Unregister(name string) {
	delete(registry, name)
}

var usageOrder = []string{
	"write", "read", "hot", "brief", "status", "profile", "backup", "restore",
	"sync",
	"approve", "pending", "maintain", "doctor", "report", "export",
	"distill",
	"done", "compact", "nudge", "seed", "setup", "teardown", "version", "mcp",
	"index",
	"audit", "diff", "update", "tuning", "import",
}

func hiddenFromUsage(name string) bool {
	return name == "session-start" || name == "session-end"
}

func UsageLine() string {
	return `{"usage":"flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
}

func UnknownCommandLine(cmd string) string {
	b, _ := json.Marshal(map[string]string{
		"message": "unknown command: " + cmd,
		"usage":   "flint <" + strings.Join(usageOrder, "|") + "> [args]",
	})
	return string(b)
}

func Registered() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func UsageOrder() []string {
	return append([]string(nil), usageOrder...)
}

func HiddenFromUsage(name string) bool {
	return hiddenFromUsage(name)
}
