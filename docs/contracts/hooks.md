# Hook Contract — SessionStart · Stop · PostCompact (non-interfering exit 0)

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: `flint-plugin/hooks/hooks.json` · `lib/ops/sessionhook.go` · `lib/knowledge/compact.go`.

## Contract

- Three harness hooks call flint entry points (all with a 10-second
  timeout):

| harness hook | command | behavior |
|---|---|---|
| SessionStart (matcher: startup/resume/clear/compact) | `flint session-start --session …` | record session boundary → hot injection → nudge → update/maintenance guidance |
| Stop | `flint session-end --session …` | end-of-session sequence (done→nudge merged) |
| PostCompact | `flint compact` | session-knowledge conversion of the compaction summary |

- **Non-interference principle**: a hook failure never blocks the session.
  hooks.json wraps commands with `2>/dev/null || true`, and the command
  implementations also end quietly — `compact` is a no-op exit 0 when the
  DB is absent, `nudge` is always 0, and failures of session-start's
  boundary recording · labeling · guidance stay silent (injection is
  unaffected).
- Agent-facing output is one line inside the `[flint]` boundary — a
  **data block, not instructions** (see the skill's three rules).
- `session-start` · `session-end` are not exposed in human-facing usage
  (`hiddenFromUsage`, see registry.md · cli.md).

## Boundaries

- Side output on the hook path (update guidance · maintenance warnings) is
  cached for 24h to minimize round trips and stays silent on network
  failure.

## Prohibitions

- Leaking error JSON onto hook stdout — hooks stay quiet (exit code only).
- Session blocking (exit 2 · 3 · timeout-crash propagation) — no failure
  may stop the session.

## Example

```json
{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"API design done"}
```
→ `flint compact` receives it on stdin, deposits the summary as session
knowledge, exit 0.
