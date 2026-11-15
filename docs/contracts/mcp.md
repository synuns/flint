# MCP Contract — `flint mcp` (5 tools · local stdio)

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: `lib/mcp/tools.go` · `lib/mcp/serve.go` · ADR-0015.

## Contract

- Transport is **stdio**. The client spawns the `flint mcp` process on the
  same machine (JSON-RPC: initialize → tools/list → tools/call; EOF is a
  normal exit 0).
- Five tools (`ToolsManifest`, v0.32.0):

| tool | behavior |
|---|---|
| `flint_read` | search active knowledge (query/tag filters, limit cap 50) |
| `flint_write` | record kind(knowledge/session/project)+payload; identical payloads are duplicate (idempotent) |
| `flint_hot` | hot summary for injection (2,000-char cap). All recent items (no session filter) |
| `flint_pending` | pending knowledge list + rule-based recommendations (verdict: promote/merge/expire/hold) |
| `flint_approve` | promote pending (optional supersede — merged replacement of an old active item) |

- Tool calls assemble cli.Run arguments and execute — returning the CLI
  output string and exit code (0/1/2/3) as-is (real DB · contract reuse, no
  separate implementation).
- The server version (`cli.VersionString`, ldflags-injected) is embedded in
  the initialize response.

## Boundaries

- External query input is placed after `--` so it is never parsed as a
  flag (end-of-options — injection defense).
- Undefined tools return `{"error":"unknown tool: <name>"}` with exit 1
  (name escaped).

## Prohibitions

- Adding a surface beyond the five tools without updating the manifest and
  this document in the same release (the SKILL tool-list guard checks it).
- Claiming support for remote (non-local) clients — violates the stdio
  premise (see bridge.md).

## Example

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"index strategy","limit":10}}}
```
