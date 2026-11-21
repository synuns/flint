# Security Policy

## Reporting a vulnerability

Report security vulnerabilities through **GitHub Security Advisories** —
the "Report a vulnerability" button on the
[Security tab](https://github.com/synuns/flint/security/advisories).

- Do **not** open a public issue, PR, or discussion for a security report.
- Reports stay private; fixes are coordinated in a private fork and
  published with an advisory once a patched release is out.
- Credit is given in the advisory on request.

## Supported versions

Only the latest minor release receives security fixes. Releases are
frequent and `flint update` keeps a binary current (checksum-verified),
so upgrading is a single command.

| Version | Supported |
| :--- | :--- |
| latest minor release | yes |
| anything older | no — update first, then re-test |

## Design posture

flint runs inside your coding sessions, so its security posture is part
of the product, not an afterthought. Structural guarantees (details in
[ADR-0013 threat model](docs/adr/ADR-0013-threat-model.md)):

- **Local by default.** flint, the agent harness, and Doltgres all run
  on your machine. There is no cloud service, no account, and no
  telemetry. The only surface meant for external AIs is `flint brief`,
  which is read-only, masks secret patterns (`[REDACTED:...]`), and
  carries the `[flint]` data-block marker so the content is treated as
  data, never instructions ([ADR-0015](docs/adr/0015-bridge-surface.md)).
- **Remote live writes are off by design.** Nothing outside your machine
  writes to your knowledge base; remote input arrives only through the
  pending review queue (`flint approve` promotes it).
- **Non-interference.** Hooks are non-blocking with timeouts, a flint
  failure is a silent skip (exit 2) that never delays or blocks your
  session, and injected context is always a marked data block.
- **Data ownership.** Your knowledge lives in your Doltgres and
  `~/.flint` — export any time (`flint export`, PAM format with optional
  Ed25519 signature). Backups support `age` encryption; secrets are
  scanned on write and backup; files are created `0600`.
- **Release integrity.** Every release artifact ships with
  `checksums.txt` (sha256) and SLSA build provenance under an existing
  signing regime ([ADR-0014](docs/adr/)). Verify a downloaded file:

  ```bash
  shasum -a 256 -c --ignore-missing checksums.txt   # or the install.sh one-liner, which does this for you
  gh attestation verify <file> --repo synuns/flint
  ```

## What is in scope

The flint binary, its hooks and MCP server, the release pipeline, and
the published plugin. Compromises of your own machine, your Doltgres
password hygiene, or third-party harnesses are out of scope for this
repository.
