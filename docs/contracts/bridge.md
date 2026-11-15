# Bridge Contract — Non-local AI Access (ADR-0015 summary · no remote writes)

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: ADR-0015 (`docs/adr/0015-bridge-surface.md`) · `flint brief`.

## Contract

- Non-local AIs (chat assistants etc.) access flint **through the bridge
  only**: `flint brief [query]` emits a masked, read-only block that the user
  pastes into any chat (one-way export surface).
- Leak defenses (everything that leaves the machine): MaskSecretsStrict
  (secret-pattern substitution + masking of allowlist-registered values —
  v1.0.11 pattern-first · nested registrations in descending order) →
  SanitizeText (neutralizing control characters and `[flint]` markers). The
  query source is restricted to the active_knowledge view — pending and
  invalidated unreviewed knowledge never leaves the machine.
- **Recovery is pending-inbox only**: inflow paths for non-local AI output
  are ① `flint write knowledge` (confidence < conf_floor → classified
  pending) and ② `flint import` (always pending), both passing through the
  existing pending→approve review circuit unchanged. The only trusted
  writer is the local agent.

## Boundaries

- flint's four local couplings (ADR-0015): transport (stdio) · data
  (Doltgres localhost binding, ADR-0010) · injection (local harness hooks)
  · trust (ADR-0013 single local user). Non-local connections are
  pull-only at best.

## Prohibitions

- **No remote live-write path — in any form (remote MCP write · HTTP API ·
  bidirectional sync), never build a write that bypasses pending.**
- Reopening the remote MCP surface only when three conditions hold (observed
  real-world bridge usage · identified access targets · accepted user
  infrastructure exposure) + an ADR-0013 revision first.
- Productizing the bridge into a server — flint's identity is a single Go
  binary + local Doltgres.

## Example

```sh
flint brief "deployment pipeline decisions"
# → masked read-only [flint] block — paste to hand over; recovery via write/import → pending
```
