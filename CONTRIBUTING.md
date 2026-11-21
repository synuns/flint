# Contributing to flint

Thanks for contributing. This page covers the practical path from clone
to PR.

## Development setup

1. **Go 1.26+** and Docker (for Doltgres).
2. Clone and build:

   ```bash
   git clone https://github.com/synuns/flint.git && cd flint
   make install        # builds flint into GOPATH/bin
   ```

3. Start Doltgres and migrate:

   ```bash
   flint setup         # docker compose up -d (port 5434) + schema migration
   ```

4. Optional demo data (synthetic, safe):

   ```bash
   make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint
   ```

## Verify before opening a PR

Run the same gates CI runs:

```bash
make ci          # YAML structure + personal-data scan + gofmt + shell contract tests + full go test
make docs-sync   # docs contract tests (README/tables must stay in sync with code)
make lint        # staticcheck (pinned version)
```

Slow suites (`db`, `backup`, `regression`) need a live Doltgres — the
`FLINT_TEST_DSN` variable points them at your test database. Start with
`flint setup` if you have not already.

## Conventions

- **Plans.** Substantial changes go through the plan workflow in
  [docs/plans/](docs/plans/) — a dated plan file paired with a `.spec.md`
  (rulings, contracts, review focus). Small fixes can go straight to PR.
  Architecture and principles: [docs/PLAN.md](docs/PLAN.md).
- **Tests are contracts.** Many tests in [docs/](docs/) assert that the
  README, RUNBOOK, and TUNING docs stay in sync with the code (command
  tables, thresholds, badges). If your change moves a documented fact,
  update the doc in the same PR — `make docs-sync` will fail otherwise.
- **TDD.** Write the failing test first, then the implementation.
- **Comments in English**, throughout the codebase.
- **No personal data.** This repository is the open-source package only:
  never commit real project names, paths, DB rows, emails, or private
  tuning values (`scripts/check_personal.py` enforces the common
  patterns; tuning values belong in the meta table at runtime, not in
  code).
- **Commits.** Contribution identity comes from each contributor's local
  git configuration; see [CLAUDE.md](CLAUDE.md) for repository rules.

## Bugs, features, security

- Bugs and feature requests: use the issue templates in
  [.github/ISSUE_TEMPLATE/](.github/ISSUE_TEMPLATE/).
- Security reports: see [SECURITY.md](SECURITY.md) — never a public
  issue.
