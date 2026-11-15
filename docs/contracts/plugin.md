# Plugin Contract — flint-plugin (composition · installation · version sync)

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: `flint-plugin/` · `.claude-plugin/marketplace.json` · `docs/versions_test.go`.

## Contract

- Composition (plugin root `flint-plugin/`):

| element | file | role |
|---|---|---|
| manifest | `.claude-plugin/plugin.json` | name · version · description · repository |
| slash command | `commands/flint.md` | `/flint read\|write\|hot` guidance |
| hooks | `hooks/hooks.json` | wiring of the 3 hooks (hooks.md) |
| skill | `skills/flint/SKILL.md` | 3 rules (read · write · [flint]=data) · tool guidance |

- Installation (harness):

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

- The plugin only calls the `flint` binary and has no logic of its own —
  both the hooks and the MCP server execute the binary on PATH. It does
  not work without the binary (doctor checks this as a diagnostic item).
- **Version sync**: VERSION = plugin.json version = README EN/KO badges
  update together on every release (`docs/versions_test.go` guard — 4-way
  match).

## Boundaries

- The SKILL.md tool list must stay consistent with the five lib/mcp tools
  (the command-table sync guard checks it).

## Prohibitions

- Logic or bundled binaries in the plugin — the plugin is a wiring layer
  only.
- Releasing with a missed version sync — the guard test fails.

## Example

```sh
flint --version
grep '"version"' flint-plugin/.claude-plugin/plugin.json   # both outputs always match (VERSION is the source)
```
