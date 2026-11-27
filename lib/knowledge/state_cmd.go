package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type workStateStore interface {
	OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error)
	ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error
	ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any
}

const stateListLimit = 50

const stateUsage = "usage: flint state open --task T [--detail D] [--next A] [--project N] [--session LABEL] | flint state list [--all] | flint state resolve --id N [--by K]"

func stateUsageErr(format string, a ...any) error {
	return wire.UsageErrorf(format+" — "+stateUsage, a...)
}

func workStateStoreOf(st wire.Store) (workStateStore, error) {
	if ws, ok := st.(workStateStore); ok {
		return ws, nil
	}
	return nil, errors.New("state: injected store does not implement work_state methods")
}

func RunState(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("state requires a subcommand")))
		return 3
	}
	switch args[0] {
	case "open":
		return stateOpen(ctx, st, args[1:], stdin, stdout)
	case "list":
		return stateList(ctx, st, args[1:], stdout)
	case "resolve":
		return stateResolve(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("unknown subcommand: %s", wire.MaskToken(args[0]))))
		return 3
	}
}

func stateOpen(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	rec := map[string]any{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--task", "--detail", "--next":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("%s requires a value", flag)))
				return 3
			}
			switch flag {
			case "--task":
				rec["task"] = args[i]
			case "--detail":
				rec["detail"] = args[i]
			case "--next":
				rec["next_action"] = args[i]
			}
		case "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("--project requires a number")))
				return 3
			}
			n, perr := strconv.Atoi(args[i])
			if perr != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("--project: invalid id %q", wire.MaskToken(args[i]))))
				return 3
			}
			rec["project_id"] = n
		case "--session":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("--session requires a value")))
				return 3
			}
			rec["session_label"] = args[i]
		default:
			fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("unknown flag: %s", wire.MaskToken(args[i]))))
			return 3
		}
	}
	if task, ok := rec["task"].(string); !ok || strings.TrimSpace(task) == "" {
		fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("open requires a non-empty --task")))
		return 3
	}
	if s, ok := rec["session_label"].(string); ok {
		if err := validateSessionLabel(true, s); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 3
		}
	} else if label, ok := wire.ReadSessionLabel(); ok {
		rec["session_label"] = label
	}
	ws, err := workStateStoreOf(st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	if agent := stateAgentFromStdin(stdin); agent != "" {
		rec["agent"] = render.Text(agent)
	}
	id, status, oerr := ws.OpenWorkState(ctx, rec)
	if oerr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(oerr))
		return store.MapExit(oerr)
	}
	fmt.Fprintf(stdout, "{\"id\":%d,\"status\":%s}\n", id, wire.QuoteJSON(status))
	return 0
}

func stateAgentFromStdin(stdin io.Reader) string {
	if stdin == nil {
		return ""
	}
	var payload map[string]any
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return ""
	}
	agent, _ := payload["agent"].(string)
	return agent
}

func stateList(ctx context.Context, st wire.Store, args []string, stdout io.Writer) int {
	all := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			all = true
		default:
			fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("unknown flag: %s", wire.MaskToken(args[i]))))
			return 3
		}
	}
	ws, err := workStateStoreOf(st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	for _, row := range ws.ListOpenWorkState(ctx, 0, stateListLimit) {
		fmt.Fprintln(stdout, stateRowLine(row))
	}
	if all {
		rows, qerr := wire.QueryOnce(ctx, st,
			`SELECT id, task, status, updated_at FROM work_state WHERE status = 'resolved' ORDER BY updated_at DESC LIMIT $1`,
			[]any{stateListLimit})
		if qerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(qerr))
			return store.MapExit(qerr)
		}
		for _, r := range rows {
			row := map[string]any{"status": "resolved"}
			if len(r) > 0 && r[0] != nil {
				row["id"] = r[0]
			}
			if len(r) > 1 && r[1] != nil {
				row["task"] = render.Text(fmt.Sprint(r[1]))
			}
			if len(r) > 3 && r[3] != nil {
				row["updated_at"] = r[3]
			}
			fmt.Fprintln(stdout, stateRowLine(row))
		}
	}
	return 0
}

func stateRowLine(row map[string]any) string {
	out := map[string]any{}
	for _, k := range []string{"id", "task", "detail", "next_action", "agent", "status", "updated_at"} {
		v, ok := row[k]
		if !ok || v == nil {
			continue
		}
		if s, isStr := v.(string); isStr {
			out[k] = render.Text(s)
		} else {
			out[k] = v
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func stateResolve(ctx context.Context, st wire.Store, args []string, stdout io.Writer) int {
	id := -1
	by := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--by":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("%s requires a number", flag)))
				return 3
			}
			n, perr := strconv.Atoi(args[i])
			if perr != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("%s: invalid number %q", flag, wire.MaskToken(args[i]))))
				return 3
			}
			if flag == "--id" {
				id = n
			} else {
				by = n
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("unknown flag: %s", wire.MaskToken(args[i]))))
			return 3
		}
	}
	if id < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(stateUsageErr("resolve requires --id")))
		return 3
	}
	ws, err := workStateStoreOf(st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	if rerr := ws.ResolveWorkState(ctx, id, by); rerr != nil {
		if errors.Is(rerr, db.ErrNotFound) {
			fmt.Fprintf(stdout, "{\"error\":\"not_found\",\"id\":%d}\n", id)
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(rerr))
		return store.MapExit(rerr)
	}
	fmt.Fprintf(stdout, "{\"id\":%d,\"status\":\"resolved\"}\n", id)
	return 0
}

func init() { wire.Register("state", RunState) }
