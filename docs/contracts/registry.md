# Registry Contract — mcp-registry publishing · signing

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: `mcp-registry/server.json.template` · `scripts/publish_mcp.sh` · ADR-0014.

## Contract

- Artifact: the MCP Registry schema manifest (name
  `io.github.synuns/flint` · description · package) — the publishing source
  is **template + CI render**, and the rendered server.json does not live
  in the repo (residue removed v1.2.3).
- Publishing path: on a tag push, CI runs `scripts/publish_mcp.sh`
  (manual local runs possible — after mcp-publisher login). The script
  computes the SHA256 of the `.mcpb` in dist/, substitutes the template's
  `{{URL}}` · `{{SHA256}}` · `{{VERSION}}`, renders, and publishes via
  `mcp-publisher publish`. The URL points at the GitHub release asset.
- Transport declaration: stdio (`command: flint`, `args: ["mcp"]`) — same
  local premise as mcp.md.
- **Signing (ADR-0014)**: at release, CI signs checksums.txt with
  `cosign sign-blob` (tlog-upload=false) and uploads it as a `.sig` asset.
  `flint update` verifies the signature with the embedded public key
  (`lib/cli/cosign.pub`) **before** checksum comparison — a compromised
  account can upload a forged checksum but cannot produce a signature.

## Boundaries

- Transition mode: with no secret configured and no embedded key, signing
  is skipped/warned and publishing proceeds (hard verification begins
  after key registration).

## Prohibitions

- Editing server.json directly without the template — the next render
  overwrites it.
- Providing a default path that bypasses verification without a signature
  (`--insecure-skip-signature` is an explicit flag for the pre-key-rolling
  period only).

## Example

```sh
./scripts/publish_mcp.sh   # with no tag argument, the latest tag is detected via git describe
# dist/ .mcpb SHA256 → template render → mcp-publisher publish
```
