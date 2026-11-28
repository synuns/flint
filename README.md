<p align="center">
  <br/>
  ◯ ─────────── ◯
  <br/><br/>
  <img src="assets/flint-logo.svg" width="120" alt="flint logo">
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  ◯ ─────────── ◯
  <br/>
</p>

<p align="center">
  <strong>Long-term memory for AI coding agents.</strong>
  <br/>
  <sub>Your agents get smarter with every session — Claude Code, Codex, Gemini, Copilot, Cursor, and more.</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="8 harnesses">
  <a href="https://github.com/synuns/flint/actions/workflows/ci.yml"><img src="https://github.com/synuns/flint/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <img src="https://img.shields.io/badge/release-v1.42.0-blue" alt="v1.42.0">
</p>
<p align="center">
  <sub>8 counts the auto-registration (tier A) harnesses — the full support list is in the <a href="#agents--adapters">adapter table</a> below.</sub>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="#why-its-safe">Safety</a> ·
  <a href="#commands">Commands</a> ·
  <a href="#agents--adapters">Agents</a>
</p>

Every AI coding agent you've used forgets everything between sessions. It relearns your architecture, rehashes decisions you already settled, rediscovers pitfalls you documented months ago. A brilliant colleague with amnesia.

flint fixes this. When your agent learns something — a decision, a convention, a gotcha — flint stores it in Doltgres, a database where every change is a commit. Next time a session starts, flint injects that accumulated context back in. The agent picks up where it left off instead of starting from scratch.

It adds up. Week one, it learns the stack. By week four, it remembers why you chose gRPC over REST. Three months in, it knows the migration order, which tests are flaky, and the deployment checklist — because it wrote all of that down.

Humans take notes in Obsidian. Agents take notes in flint — the stone you strike to make fire and tools.

> v1.42.0 · surface-deploy — release gate sign verification (cosign verify-blob against embedded public key·skip when absent) · brand name re-sweep refreshed (GitHub+brew·flint-fyi observed) · observation threshold e2e (read_events_n ≥200/<200 data-path assertion) · [full changelog](docs/HISTORY.md)
> v1.41.1 · quality-consolidation-patch — audit fixes: TUNING and slowlock stale file references updated to consolidated filenames · [full changelog](docs/HISTORY.md)
> v1.41.0 · quality-consolidation — version-suffixed test files merge into 4 per-domain single files (36 removed·function names and counts unchanged) · report parallel residuals: collectDaily joins the errgroup (5-way)·profile meta reads parallel (golden output unchanged) · read LIKE deferred (pg_trgm unsupported) · [full changelog](docs/HISTORY.md)
> v1.40.0 · staleness-mapping — claim→vol default suggestions (decision/event→immutable·procedure/lesson→conditional·existing tags always win·no automatic exemption) · source axis (SuggestVolClassForSource) · approved apply path: maintain --age-audit --apply-vol [--yes] (dry-run default·repo-route tag merge) · [full changelog](docs/HISTORY.md)
> v1.39.1 · r143-angle2-sessionlabel patch — security: secrets scan covers session_label · work_state payloads sanitize session_label · re-report backfills the label · label-file fallback validated (control chars, 200-rune cap) · [full changelog](docs/HISTORY.md)
> v1.39.0 · knowledge growth model phase 2 — recall history feeds read ranking (fifth RRF signal) · recall-audit recommend (supersede/expire/hold, decay-audit parity) · session chain: state open records the session label, hot revives project-first snapshots · release gate runs CI checks locally first · [full changelog](docs/HISTORY.md)
> v1.38.4 · r143-angle4 patch — docs: read projection wording (10 columns, status included) · work_state retention contract (90-day purge, TUNING row added) · QUICKSTART recall view + flint state · README doctor count 30 · [full changelog](docs/HISTORY.md)
> v1.38.3 · r143-angle3 patch — performance: resolved work_state retention (default 90d) · batch resolve (one UPDATE, resolved_at recorded) · dedup query uses the partial unique index · hot meta queries halved (5→2) · testengine scratch DROP fast-fails on engine-down · [full changelog](docs/HISTORY.md)
> v1.38.2 · r143-angle2 patch — security: secrets scan covers resolved work_state rows · work_state payloads sanitized at write (chokepoint allowlist) · stale-state output through render · empty --task exits 3 (CLI=MCP) · [full changelog](docs/HISTORY.md)
> v1.38.1 · r143 patch — claim integrity: approve --claim persists the claim (promoted rows no longer vanish from recall) · claim normalized at write · invalid vocabulary rejected · hot snapshot budget invariant (total ≤ budget) · fixture schema gates · [full changelog](docs/HISTORY.md)
> v1.38.0 · knowledge growth model phase 1 — claim axis (decision/procedure/lesson/event, events out of recall) · work_state table (intentions open→resolve, unconditional recording) · 3-stage collection (bounded pending → Δ≠0 promotion → post-hoc proof) · claim gate at approve · flint state CLI + MCP flint_work · hot work-state snapshot · testengine scratch root fix · [full changelog](docs/HISTORY.md)
> v1.37.1 · r137 patch — G5 audit hardening: smoke hook failure now stops the sequence · demo-DSN and ops-DSN guards hardened (host·port·db) · regression ground gains discriminating confidence case · restore sanitization exception documented · smoke moved to --check opt-in · [full changelog](docs/HISTORY.md)
> v1.37.0 · trust foundation — synthetic regression ground (exact-match reach judging, engine-isolated) · poisoning regressions ×3 frozen · write-boundary contract with drift gate · 7-stage doctor smoke · [full changelog](docs/HISTORY.md)
> v1.36.1 · r136 patch — G5 audit hardening: derivative hash domain aligned to vault summaries (the tagging feature was dead against real notes) · contract gate hardening (exact boundary values · region markers) · tag collision fix · sentence-level evidence detection · [full changelog](docs/HISTORY.md)
> v1.36.0 · extraction contracts — echo disposition frozen as a contract with a drift gate · derivative tagging by strict source hash identity · evidence gate for rule-type knowledge (origin/status required) · trust tier registry (down-only, K2) · remediation entry decided (B-default) · [full changelog](docs/HISTORY.md)
> v1.35.1 · r135 patch — G5 audit hardening: `flint-waiver:` comment convention implemented in the LLM import gate (CI_REVIEW §6 aligned) · F3~F5 audit findings fixed (sync exit contract · engine state key · 4-hook docs) · import-block comment parsing · FLINT_SLOW_LOOPS cap · [full changelog](docs/HISTORY.md)
> v1.35.0 · work system + AX audit — 5 deterministic agent-mistake checks (test deletion/skip, LLM SDK lint, vet+lint in ci, surface contracts, structured failures) · rule↔enforcement pair registry · slow failures root-caused (native engine commit sequencing, retry wrapper) · 5-axis×44-surface AX matrix · [full changelog](docs/HISTORY.md)
> v1.34.1 · r134 patch — G5 audit hardening: atomic session once-guard (parallel same-label starts no longer double-inject) · budget unit seam unified to rune counts (Korean hot output no longer starves active recall) · zero_hit_unowned_pct row moved to the knowledge-hygiene area · [full changelog](docs/HISTORY.md)
> v1.34.0 · active recall at session start — re-inspection bursts injected once per session within the remaining hot budget (no new hooks); provenance suffix (`source=·confidence=`) as a single convention across hot and recall lines; `zero_hit_unowned_pct` abstention metric; minimal-injection principle documented · [full changelog](docs/HISTORY.md)
> v1.33.1 · r133 patch — G5 audit hardening: doctor detail path masking (hook-path · service remnant), DNS lookup deadline, plugin version parse-failure warns instead of silently passing, serve exit-code tests · [full changelog](docs/HISTORY.md)
> v1.33.0 · `flint report --serve` — localhost-only dashboard over the same diagnose source (ETag, per-request engine connection, CSS token slots); area F doctor checks ×5 (hook path · DSN class · collection freshness · version gap · service remnant); METRICS raw-data catalog + registry-sync gate; `--html/--out` deprecated via-path · [full changelog](docs/HISTORY.md)
> v1.32.4 · r131 patch — the setup exemption stated precisely (always exempt by design, `--json` mode included); the blanket wording fixed in QUICKSTART and both READMEs · [full changelog](docs/HISTORY.md)
> v1.32.3 · r130 patch — the `--json` cap sentence scoped to the actual dual-mode commands (read · pending); the other exempt commands keep the exemption on every route (setup by design; tuning · maintain · export · report have no `--json` flag) · [full changelog](docs/HISTORY.md)
> v1.32.2 · r129 patch — dual-mode commands (read · pending) exempt the 300s cap only when interactive: `--json` routes (MCP flint_read · skill scripts) keep the hung-engine guard · Run-level probe tests for both routes · the wrapped-deadline test actually wraps · RUNBOOK contradiction fixed · [full changelog](docs/HISTORY.md)
> v1.32.1 · r128 patch — QueryOnce treats a wrapped DeadlineExceeded as permanent (the v1.32.0 cap made an expired deadline retry a dead context) · user-waiting paths (TUI forms · setup downloads) exempt from the 300s cap, joining hooks · FLINT_COMMAND_TIMEOUT documented in QUICKSTART·RUNBOOK · release-doc numbers corrected · the split-string gate-bypass question declared (the dialect gate over-matches threshold keys — pattern extension is the follow-up) · [full changelog](docs/HISTORY.md)
> v1.32.0 · structure-cleanup — context deadlines where blocking was unbounded (command cap 300s with FLINT_COMMAND_TIMEOUT override · hooks session paths stay unlimited · service-manager output probe 5s) · MCP Serve reframes over-long lines in a loop instead of recursing (the whole line is skipped — remnant bytes no longer re-parse as requests) · QueryOnce returns context.Canceled immediately (retry is transient-only, 116 call sites unchanged) · thresholds single source (one thresholdMetas row drives defaults·parsing·fallback via reflect) · maintain split 2,190→446 lines into a registration slice + 6 mode files (byte-identical handler moves) · doctor split 2,050→345 + 8 files (7 checks + hookfix, registry intact) · RunWrite 535→19 lines (parse→validate→dedupe→persist→render) · schema gate stage 1 (3 canonical queries in lib/db + TestSchemaSealedOutsideDB) · errLine single path + printErr helper · seam rules documented (docs/contracts/seam.md) · [full changelog](docs/HISTORY.md)
> v1.31.3 · r126 patch — UpgradeRoundTrip gains an entry guard (partial wiring returns ErrNotWired up front — the download-success path no longer panics on a nil seam) · success-path nil-seam regression test committed · [full changelog](docs/HISTORY.md)
> v1.31.2 · r125 patch — UpgradeRoundTrip rollback guards nil service seams (a partial wiring panicked instead of returning the error) · failure-path regression test committed (setup --engine native keeps the prior mode when the pipeline fails) · unused constant removed · [full changelog](docs/HISTORY.md)
> v1.31.1 · native-service — `flint engine status|start|stop` now reach the registered service in production wiring (managed container compose · native launchd/systemd-user) · macOS stop terminates the service (bootout) instead of triggering a KeepAlive restart · `setup --engine` records the mode only after the pipeline succeeds · the CI macOS smoke passes FLINT_CONFIG_DIR/FLINT_HOME through doctor and session-start · doctor count corrected to 24 · [full changelog](docs/HISTORY.md)
> v1.31.0 · native engine — `flint engine status|start|stop` service control · `setup --engine managed-container|native|external` (opt-in mode selection — native: dedicated downloader with hash + version verification, launchd/systemd-user service registration, first-boot credential bootstrap) · container→native transition with rollback · doctor `engine` check (registration · running · version · pin) · R123 carryover (local parsers mask unknown-flag echo tokens) · [full changelog](docs/HISTORY.md)
> v1.30.2 · r122 patch — docs follow the fresh no-flag routing (port assignment on the managed path, step guide corrected) · unknown-flag echo masks `scheme://` tokens (the inline-DSN rejection no longer leaks through the parser) · `?port=` hand-edit edge declared · [full changelog](docs/HISTORY.md)
> v1.30.1 · install-audit — docs corrected to match the shipped v1.30.0 implementation (engine thresholds are code constants pending meta registration · pull/TLS/health-timeout classifiers, connection-level handshake and external-mode purge gating are this patch's additions · compose v1 is a prescription, not a fallback · port assignment on the managed setup path (bare no-flag setup included)) · native L0 spike reworded (four spec pass criteria remain open, planned at v1.31.0 G2) · doctor count corrected to 23 · [full changelog](docs/HISTORY.md)
> v1.30.0 · install-journey — `flint setup` ships the engine compose definition inside the binary (project name `flint` · digest-pinned image with a 4-way equivalence guard — no repository checkout; the first run pulls, starts and finishes ping + migrate in one go, with per-cause error prescriptions) · `--port` auto-assignment probes upward from 5434 (5499 reserved) · engine handshake verifies Doltgres and the pin · `--engine-url` external engine mode (loopback-only, DSN masked) · manual conversion procedure for pre-v1.30.0 installs (RUNBOOK §11) · native-engine L0 spike passed (v1.31.0 go-ahead) · [full changelog](docs/HISTORY.md)
> v1.28.1 · R119 patch — T() gains an en fallback for missing ko keys — new keys no longer leak raw key strings (≤24h cache note from R119-2 declared accepted, self-resolving) · [full changelog](docs/HISTORY.md)
> v1.28.0 · surface cleanup — `flint read` / `flint tuning` with no arguments on a TTY open an interactive TUI (flagged·non-TTY·FLINT_NO_TUI invocations keep the flag mode unchanged; read instrumentation preserved) · i18n KO catalog (`FLINT_LANG`, usage surface first) · test consolidation (8 domains, 56 version-suffixed files into 8, suite count unchanged at 592) · BRAND sweep · BREW tap review · signature re-verification · [full changelog](docs/HISTORY.md)
> v1.29.2 · r117 patch — isolation polish: the doctor `engine-footprint` skip detail now names the cause (an unstarted dedicated engine — "no running container publishes 5499" — is distinguishable from docker being absent) · dedicated-engine DSN container selection pinned by test in both directions · [full changelog](docs/HISTORY.md)
> v1.29.1 · r116 patch — isolation audit fixes: the Makefile `FLINT_TEST_DSN` default now points at the dedicated engine (5499 — the ops-port default defeated local isolation) · docker-compose.test.yml pins the CI image digest (with an equivalence guard) · engine container selection is port-based (`EngineMemMB`·`restarts` never read the wrong engine) · `FLINT_REALDB_REQUIRED=1` is honored in tests/ helpers (skip-0 sentinel on every path) · [full changelog](docs/HISTORY.md)
> v1.29.0 · physical isolation — dedicated test engine (`docker-compose.test.yml` on 127.0.0.1:5499 · `internal/testengine` lazy ensure + scratch lifecycle + run-end purge · the ops engine is never touched by tests) · `maintain --purge-dropped` (dry-run list of restorable dropped databases → irreversible purge, loopback-only) · doctor `dropped_dbs` / `stray_dbs` warnings (thresholds `dropped_dbs_warn`·`stray_dbs_warn`, meta `stray_dbs_allow` exemption) · engine-footprint shows container restart count · [full changelog](docs/HISTORY.md)
> v1.26.3 · r-round patch — comment gate: `internal/commentlint` (any non-directive Go comment fails `go test ./...`), `scripts/csweep.go` (the AST sweep tool, in-repo), `scripts/check_comments.py` in `make ci` (git-tracked `*.sh`·`*.py`, quote/heredoc-aware), 227 script comments removed · [full changelog](docs/HISTORY.md)
> v1.26.2 · r113 patch — comment sweep: 556 files · 23,960 narrative comments removed via a go/parser AST pass (string literals untouched; `//go:*`·`//nolint`·`// +build` directives kept) · README highlight history restored (a bump-script bug had relabeled all 37 entries) · bump script no longer relabels highlight lines (badges only) · release-gate asserts highlight labels unique · [full changelog](docs/HISTORY.md)
> v1.26.1 · r111-r112 patch — explicit check refreshes the update cache (`flint update --check` / doctor `--check update` writes latest · ETag · checked_at — the doctor Fix line now does what it says) · keyword-form DSN derivation refused (exit 3 + FLINT_DEMO_DSN guidance instead of a corrupted `_demo` DSN) · FLINT_DEMO_DSN skips the base-server ensure · offline registry fallback (cached copy + age, still not a warn) · 42P04 duplicate_database tolerated (parallel first-run seeds) · [full changelog](docs/HISTORY.md)
> v1.26.0 · surface trust — update cache three principles (ETag conditional re-fetch: a 304 keeps the cached latest · contradiction detector: a cache older than the running binary re-fetches immediately · `checked_at` surfaced in `report --text`) · `flint seed --demo` (embedded synthetic dataset into an isolated `_demo` database · `--reset --yes` deletes only demo rows) · doctor `versions` (self · cached latest · PATH · mcp-registry side by side) · threshold `update_cache_age_h` · [full changelog](docs/HISTORY.md)
> v1.25.1 · r108-r109 patch — gc-pending JSON closing fix (the `maintain --gc` pre-run notice line now parses as JSON) · dialect probe relocation (the `dolt_log_depth` SQL is lib/db-owned — `db.DoltLogDepth`; doctor calls it) · depth probe numeric fix (`commit_order::int8` + `CellToInt` pgtype.Numeric — the depth read a silent 0 on the real engine) · `db_size` documented as a standing skip on Doltgres · engine-footprint timeout 2s→5s · bare `--shallow` wording · [full changelog](docs/HISTORY.md)
> v1.25.0 · engine ops — `maintain --gc --yes` (dolt garbage collection: disconnect notice → reconnect loop → schema recheck → meta history) · doctor `dolt_log_depth` (O(1)) / `db_size` warnings + `engine-footprint` display · RUNBOOK §10/§11 · thresholds `dolt_log_depth_warn`·`db_size_warn_mb` · ANALYZE retired (no-op evidenced) · audit_log replay spike succeeded (pure PG) · [full changelog](docs/HISTORY.md)
> v1.24.2 · r106 patch — race-recheck echo coverage (the race-recheck duplicate's echo accrual now has a fake-store regression) · quiet-failure regression (an `echo_candidates` INSERT failure never fails the write) · echo scan comment (pending rows documented as pre-existing candidates) · [full changelog](docs/HISTORY.md)
> v1.24.1 · r104-r105 patch — `echo_candidates` accrual wired (write-time echo candidates persist: fresh row id, duplicate resend keyed by the existing row) · merge-audit active-only fetch (pending rows excluded) · `--merge` joins the D4 disposed counter · int4 input guards (`--recall-audit --limit`, `read --related`) · [full changelog](docs/HISTORY.md)
> v1.24.0 · curation residual — schema v19 (`kind`·`source_path`, `echo_candidates`) · `maintain --merge-audit` (duplicate pair report, cost-estimated) · `--recall-audit` (bad-recall feedback report) · `read --related` (undirected neighbor links) / `--why` (token tier · RRF signal · zero-hit evidence) · [full changelog](docs/HISTORY.md)
> v1.23.2 · r102 patch — counter digit bound (18-digit guard on the shadow counter upsert) · v1.23.0 spec alignment (boundary fires, off-mode cost) · what-if p50/p90 domain filter · [full changelog](docs/HISTORY.md)
> v1.23.0 · autotune shadow — schema v18 `tuning_predictions` (predictions recorded, nothing applied) · `auto_mode` policy switch (default off, whitelist: `suggest_min_score`) · shadow rounds on the read tail (24h gate, streak rules) · `flint tuning what-if` / `status` (reclassification · p50/p90 · drift card) · [full changelog](docs/HISTORY.md)
> v1.22.1 · r96-r98 patch — rematch stamp preservation (`content_changed_at` kept on hash-identical re-applies) · `maintain --recheck --conditions` backfill · `flint distill` file cap (`distill_max_runes`) + no same-minute overwrite · id/min-score input guards · [full changelog](docs/HISTORY.md)
> v1.21.1 · r91-r94 patch — path redaction on scan errors · vault root guard (persist after success) · tuning-key wiring · scan hardening (retry · touch refresh · symlink skip · 60k+ delete) · [full changelog](docs/HISTORY.md)
> v1.21.0 · vault-bridge-index — schema v16 `vault_files` · `flint index` (snapshot-diff, one batch commit) · `read` vault fusion (`source:"vault"`, cap 5) · scan p95 metering · [full changelog](docs/HISTORY.md)
> v1.20.3 · r89 patch — merge & import-ref join the supersede lock domain (cycle-proof) · [full changelog](docs/HISTORY.md)
> v1.20.2 · r88 patch — advisory-lock single-connection supersede · PAM self-link skip · truncation test · [full changelog](docs/HISTORY.md)
> v1.20.1 · r84-r87 patches — link accrual completeness (mixed batch · PAM round trip) · supersede race hardening · schema v15 unique link index · [full changelog](docs/HISTORY.md)
> v1.20.0 · knowledge-schema-search — schema v14 knowledge links (refines · supports · contradicts · duplicates) · read `--min-confidence` / `--origin` filters · supersede cycle guard & chain view · [full changelog](docs/HISTORY.md)
> v1.19.6 · observability-foundation patches — schema v13 (near_score · threshold snapshots · weekly rollups) · metering freshness & coverage · [full changelog](docs/HISTORY.md)
> v1.18.3 · R69 — grammar fix · [full changelog](docs/HISTORY.md)
> v1.17.2 · r65-dedup — correction hash origin exclusion · [full changelog](docs/HISTORY.md)
> v1.17.1 · r64-observability — origin tracking fix · [full changelog](docs/HISTORY.md)
> v1.17.0 · echo — write-time contradiction detection · observability keys · [full changelog](docs/HISTORY.md)
> v1.16.2 · R62 — highlight order contract tests (latest-first + EN/KO parity) · [full changelog](docs/HISTORY.md)
> v0.43.0 · auto-update · go install · end-to-end release pipeline · [full changelog](docs/HISTORY.md)
> v0.44.0 · PLAN rearm · surface contracts · command-table guard · [full changelog](docs/HISTORY.md)
> v1.7.2 · telemetry attribution · sync mitigation · probe heal (R38) · [full changelog](docs/HISTORY.md)
> v1.9.1 · R42 hardening — multi-device portability (jsonl recall replay) · recall event lifecycle · [full changelog](docs/HISTORY.md)
> v1.9.2 · R43 recovery — import recall-migration resilience · fresh-DB schema direct path · doc accuracy · [full changelog](docs/HISTORY.md)
> v1.9.3 · TUI & perf — low-height TUI chrome minimization · parallel report collection · opt-in tuning TUI · [full changelog](docs/HISTORY.md)
> v1.10.0 · Hygiene — comments in English across 424 files · slow suite 34.5% faster · test race fixes · [full changelog](docs/HISTORY.md)
> v1.10.1 · R47 — file-count corrections in v1.10.0 notes · [full changelog](docs/HISTORY.md)
> v1.10.2 · R48 — notes hygiene (dup heading · order) · [full changelog](docs/HISTORY.md)
> v1.11.0 · knowledge pipeline step 1 — user-correction channel (`--source correction`) · [full changelog](docs/HISTORY.md)
> v1.11.1 · R50 — dup_correction metering fix · [full changelog](docs/HISTORY.md)
> v1.11.2 · r52-reporting — dup_correction report exposure · [full changelog](docs/HISTORY.md)
> v1.12.0 · knowledge pipeline step 2 — recall-failure gap candidates · [full changelog](docs/HISTORY.md)
> v1.12.1 · r54-docs — RUNBOOK pipeline step 2 notes · NFKC truncation fix · [full changelog](docs/HISTORY.md)
> v1.13.0 · knowledge lifecycle — staleness scoring · volatility tiers · [full changelog](docs/HISTORY.md)
> v1.14.0 · knowledge pipeline step 3 — auto-detected corrections (A-plan hook) · survived decay relief · [full changelog](docs/HISTORY.md)
---

## Quick Start

```bash
# Install the prebuilt binary (sha256-verified) to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash

# Start Doltgres (embedded compose, digest-pinned) and migrate
flint setup

# Record your first memory
echo "The API gateway retries idempotent POSTs twice" | flint write knowledge
```

Optional demo data: `flint seed --demo` loads the embedded synthetic dataset into an isolated demo database (the DSN's database name + `_demo`; idempotent, `--reset --yes` removes only the demo rows). From a checkout, `make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint` targets an explicit DSN.

> Every release artifact ships with SLSA build provenance — verify a downloaded file with `gh attestation verify <file> --repo synuns/flint`.

### Installing the binary

Four paths — pick one. `flint setup` is the common next step for all of them: it ships the engine compose definition inside the binary, so no repository checkout is needed — only Docker.

**1 · One-line installer** — prebuilt binary, checksum-verified:

```bash
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash
```

Detects your platform (darwin/linux on arm64/amd64), downloads the matching release archive plus the release `checksums.txt`, verifies the sha256 of that one asset, and installs to `~/.local/bin` — override with `--bin-dir DIR`. Needs only `curl`, `tar`, and `shasum` or `sha256sum`; no Go toolchain. If the target directory is not on your `PATH`, the script prints the exact line to add.

**2 · go install**:

```sh
go install github.com/synuns/flint/cmd/flint@latest
```

Module path `github.com/synuns/flint` (v0.22.0+) — version comes from build info; `flint update` covers binary refresh afterwards.

**3 · Manual download + checksum verification** — the [releases page](https://github.com/synuns/flint/releases) attaches binaries, checksums, and the plugin zip automatically:

```bash
ver=$(curl -fsSL https://api.github.com/repos/synuns/flint/releases/latest \
	| sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
asset="flint_${ver#v}_darwin_arm64.tar.gz"          # adjust: darwin|linux _ arm64|amd64
base="https://github.com/synuns/flint/releases/download/$ver"
curl -fsSLO "$base/checksums.txt" "$base/$asset"
line=$(grep " $asset\$" checksums.txt) && [ -n "$line" ] && echo "$line" | shasum -a 256 -c -  # no shasum? pipe to: sha256sum -c -
tar -xzf "$asset" && install flint ~/.local/bin
```

**4 · Homebrew (cask — tap in preparation)**: flint ships prebuilt binaries only, so the Homebrew path is a **cask, not a formula** — goreleaser deprecated formula-based distribution for binary releases (2026), and a cask installs the exact release artifact with sha256 verification, the same trust chain as the manual path. Once the tap is live the command will be `brew install --cask synuns/tap/flint`; preparing it means a `homebrew-tap` repository with one Cask per release (`url` = the release asset, `sha256` from `checksums.txt`). Until then, use path 1 or 2.

### Claude Code

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

The plugin needs the `flint` binary on your `PATH`. Hooks and the MCP server both call it.

### Other agents (Codex, Gemini, Cursor, OpenCode)

```bash
flint setup --mcp
```

Detects installed harnesses (Codex, Gemini, OpenCode, Copilot, Cursor, Qwen, Devin, Cline) and registers the MCP server via each native CLI. File-based harnesses (Windsurf, Zed, Amp, VS Code, Continue, Goose, Trae) get a doctor check + adapter docs. Anything not installed gets skipped. Exit 0 regardless.

<details>
<summary>What <code>flint setup</code> does</summary>

Writes the embedded compose file (project name `flint`, Doltgres image pinned by digest — ADR-0020) to a temp path and starts the engine with `--project-name flint`, then migrates the schema — all in one run, from any directory. The managed run closes with a one-line JSON status on stdout (`{"status":"ok","mode":"managed","port":N}` — progress notes go to stderr). Host port defaults to 5434; the managed path runs for `--port N` and equally for a bare first install (no recorded DSN) — an existing `flint`-project install is adopted, and when the requested port is taken setup probes upward for a free loopback port (5499 excluded — reserved for the dedicated test engine) and records the assignment in `config.json`. Default DSN is `postgresql://flint:flint-dev@localhost:5434/flint` — override with `FLINT_DSN`. Already run your own Doltgres? `flint setup --engine-url` records it (loopback-only; DSN via `FLINT_ENGINE_URL` or `--engine-url-file`, inline values rejected, output masked). Tear down with `flint teardown`.

</details>

<details>
<summary>Configuring exposure</summary>

`flint setup --exposure normal|minimal` controls how much context `hot` injects at session start. `minimal` keeps it to a single headline. Four hooks: SessionStart (`session-start`), PostCompact (`compact`), Stop (`session-end`), UserPromptSubmit (`correction_filter.sh` — detects corrections in prompts and deposits them as pending knowledge, non-blocking).

</details>

Full walkthrough: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## How It Works

<p align="center">
  <img src="assets/flint-architecture-en.svg" width="880" alt="flint architecture diagram">
</p>

Each session cycles through four phases:

| Phase | What happens |
| :--- | :--- |
| **Inject** | Hook fires `flint hot` at session start — streams a budgeted `[flint]` context block |
| **Work** | Agent reads and writes via CLI or MCP |
| **Record** | `flint write` appends JSONL with hash dedup |
| **Verify** | `flint audit` inspects `dolt_log`; `flint restore --drill` rehearses recovery |

Three disciplines keep the memory compounding instead of rotting. **Collection is bounded** — unproven candidates land as pending with an expiry, hash-deduped, never as raw session dumps. **Promotion is judgment** — `flint approve` promotes a candidate only when a session can point at what it changes. **Value is proven after the fact** — knowledge nothing recalls decays out of ranking (decay score · dead-share audits), so the store stays lean by metabolism, not by hand-cleaning.

In one picture:

```
session start ──hook──► flint hot ──budgeted [flint] block──► agent context
     │
     ├── flint write ──hash dedup──► Doltgres (every change is a commit)
     │                                    │
     └── flint read / MCP tools ◄── RRF search over the active view
                                          ▼
                            audit · backup · restore · report
```

<p align="center">
  <img src="assets/flint-lifecycle-en.svg" width="880" alt="flint session lifecycle: memory compounds">
</p>

---

## Beyond local agents — the bridge

flint's surfaces (hooks, stdio MCP, localhost Doltgres) assume the agent runs on your machine — that's deliberate ([ADR-0013](docs/adr/0013-threat-model.md)). For AIs that don't — a cloud assistant, a phone app — use the bridge instead of a connector:

```bash
flint brief [query]        # masked, read-only context block — paste it into any chat
```

`brief` searches active knowledge only (pending never leaves the machine), masks secret patterns (`[REDACTED:…]`), and carries the `[flint]` data-block marker so the receiving AI treats it as data, not instructions ([ADR-0015](docs/adr/0015-bridge-surface.md)). To bring an assistant's answer back, feed it to `flint write knowledge` with a confidence below `conf_floor` (low-confidence writes are classified pending), or use `flint import` (always pending) — it lands in the same review queue as any unreviewed writer, and `flint approve` promotes it. Remote live writes are off the table by design.

---

## Why it's safe

flint sits inside your coding sessions, so its constraints are part of the product, not fine print ([ADR-0013 threat model](docs/adr/ADR-0013-threat-model.md)):

- **Local and offline.** flint, your harness, and Doltgres all run on your machine — no cloud service, no account, no telemetry. The maintenance paths (`doctor`, `report`, `maintain`) are offline CLIs.
- **Non-interference — five principles.** (1) Minimal injection, full search: flint never speaks first. (2) Hooks are non-blocking with timeouts — session end is never blocked. (3) A flint failure is a silent skip (exit 2) — your session is never delayed. (4) Warnings fire only on threshold breach. (5) flint data is data: the `[flint]` block is a data boundary that can never become an instruction.
- **Minimal injection — evidence principle.** Injection effects are measured, not asserted: the correlation between injected volume (hot bytes) and success rate (read hits and fallbacks) is tracked through weekly rollup consumption only — no new live queries. With no remaining session budget, flint injects nothing.
- **You own the data.** Everything lives in your Doltgres and `~/.flint` — export any time (`flint export`, PAM with optional Ed25519 signature), backups with `age` encryption, secrets scanned on write and backup, remote live writes disabled by design ([ADR-0015](docs/adr/0015-bridge-surface.md)).

---

## Commands

Two record types live side by side: **knowledge** (claims about the world — gated: a write without a claim lands pending, and `flint approve --claim` is the single promotion gate) and **work state** (intent — recorded unconditionally via `flint state`, resolved when done, composited into the `flint hot` snapshot while open). `flint read` recalls knowledge only; event rows are explicit lookup (`--claim event`).

| Command | What it does |
| :--- | :--- |
| `flint setup` | Start Doltgres (embedded compose, digest-pinned), migrate, print seed/plugin next steps — `--port N` · `--engine managed-container\|native\|external` (mode selection; native downloads the pinned engine, registers the platform service and bootstraps credentials) · `--engine-url` (external engine, loopback-only) |
| `flint engine` | Service control for the recorded engine mode — `status` · `start` · `stop` (mode × command matrix: managed-container drives the embedded compose, native drives the registered service, external advises only; undefined pairs are rejected with guidance) |
| `flint seed --demo` | Load the embedded synthetic demo dataset into an isolated demo database (database name + `_demo`, `FLINT_DEMO_DSN` override; idempotent; `--reset --yes` deletes only `demo/` rows — mismatched owner exits 3) |
| `flint update` | Self-update to the latest release (checksum-verified, `--check` dry-run) |
| `flint tuning` | Inspect/override threshold keys (`list`/`get`/`set`/`reset`; autotune surfaces `auto off\|shadow` · `what-if` · `status`); bare invocation on a TTY opens the interactive form (subcommands, flags, non-TTY and FLINT_NO_TUI keep the flag mode; `--tui` forces it) |
| `flint write <kind>` | Record knowledge from stdin (idempotent) |
| `flint import [path]` | Convert rule files (CLAUDE.md etc.) to pending knowledge (`--commit`); `--format jsonl\|pam` migrates knowledge back in (round-trip re-import · PAM v1.0 intake) |
| `flint export [--format f]` | Export knowledge as jsonl·md·txt·csv·pam (PAM v1.0 portable memory-store; `--status`·`--tag`·`--since` filters, `--out` file with `.sha256` sidecar; PAM `--sign` Ed25519 signature, spec §18) |
| `flint index [--vault p]` | Index a markdown vault into `vault_files` (snapshot-diff, one batch commit per diff; `--vault` persists the path, `--limit` caps the scan) |
| `flint read <query>` | Search active knowledge via the recall view (claim `event` rows excluded — explicit lookup is `--claim <decision\|procedure\|lesson\|event>`; vault rows appended after the knowledge block when indexed); bare invocation on a TTY opens the interactive browser (query input · result list · detail toggle); opt-in `--related N` appends undirected knowledge-link neighbors (cap 5) and `--why` appends per-row evidence (token tier · RRF signals · zero-hit rationale) — the flagged-mode output is unchanged |
| `flint hot --session <id>` | Inject context at session start |
| `flint brief [query]` | Masked, read-only brief to paste into any external AI (the bridge) |
| `flint done --session <id>` | Wrap up a session |
| `flint approve --id n` | Promote pending to active — the single claim gate: a row without a claim (or backfill `event`) requires `--claim <decision\|procedure\|lesson\|event>` (missing → exit 3); a write without a claim lands pending regardless of confidence |
| `flint state` | Work state (intent) surface — `open --task T [--detail D] [--next A] [--project N] [--session LABEL]` (server-side normalized hash absorbs re-reports of the same open task → one-line JSON `{id, status: open\|updated\|noop}`; the session label — flag first, label-file fallback — lets `flint hot` prioritize the current project's snapshot rows, unresolved keeps the output unchanged), `list [--all]`, `resolve --id N [--by K]`; open rows surface in the hot snapshot (cap `state_snapshot_max`) and stale ones are proposed for resolution by `maintain --stale-state` (approval-gated) — recorded unconditionally, no claim gate |
| `flint maintain` | Curate: check thresholds, invalidate, merge, `--merge-audit` duplicate pair report (multiset Dice, cost-estimated, listing only), `--recall-audit` bad-recall feedback report (listing only — each item carries a report-only `recommend` field, `supersede`·`expire`·`hold`; disposition always via the approval-required subcommands), `--age-audit` staleness report with `suggested_vol` (claim · source defaults, proposal-only), `--age-audit --apply-vol [--yes]` apply the vol suggestions as tag merges (dry-run without `--yes` — approval-gated), `--reaffirm` validity refresh, `--recheck` conditions review, `--gc [--shallow] --yes` dolt garbage collection (operator command — disconnects all sessions; reconnect + schema recheck in the same run), (`--tui` interactive review, opt-in) |
| `flint distill --out DIR` | Emit recall-scored unstamped insights as one aggregate markdown file (`--min-score` overrides `distill_min_score`; emitted rows stamp `distilled_at`, reruns yield the remainder — ADR-0018) |
| `flint audit --limit n` | Show last n commits |
| `flint backup` | Dump to SQL with sha256 checksum (`--encrypt` age at-rest, `--mask` redacted diagnostics) |
| `flint restore --file f` | Restore from backup (`--drill` to rehearse) |
| `flint sync` | Sync knowledge with a Doltgres remote — `remote add/list/remove` (url masked), `push`/`pull [--dry]`; a merge conflict exits 1 with manual-resolution guidance; remote urls are never logged (human operator command) |
| `flint profile` | Profile verdict: evidence + recommendation, `--apply normal\|minimal` |
| `flint status` | Schema version, pending count, hot p95 |
| `flint mcp` | MCP server (read·write·hot·pending·approve·work tools) |
| `flint pending` | Pending review queue with verdicts (promote·merge·expire·hold, `--json`) |
| `flint compact` | Record a compact summary as pending knowledge (PostCompact hook entry) |
| `flint nudge` | Session-end distillation reminder for markers left by compact (`--clear` closes) |
| `flint doctor --check c` | Health checks (30 checks, subset via `--check`, fail → exit 1) |
| `flint report --days n` | Daily intake·zero_pct·p95·stale report (`--html` static dashboard) |
| `flint diff <from> <to>` | Compare knowledge between two as-of points |
| `flint teardown` | Undo setup (hooks/config; `--purge` also removes data) |
| `flint version` | Print version |

**Exit codes:** 0 success (zero-hit `read` also exits 0 — silent in text mode, `{"hits":0}` with `--json`) · 1 error · 2 storage failure · exit 3 — invalid input (valueless trailing flags, bad `--status` / `--as-of` values, schema violations on `write`).

`flint read` uses word-by-word fallback search when the phrase query returns nothing — and the fallback re-ranking fuses each row's recall count as an additive signal, so recall-proven knowledge ranks higher on tied scores.

Memory that decays like memory: every `read` hit reinforces the entry (`recall_score`), unused entries decay daily (floor 0), and `hot`/`brief` rank well-recalled knowledge first — `flint maintain --decay-audit` lists archive candidates, and archiving always requires your approval.

### Interactive TUI (v1.5.0)

Four screens built on bubbletea: `flint setup` (no arguments, on an already-configured machine) opens the setup wizard — a bare first run with no recorded DSN takes the managed engine path instead (port assignment · engine start · migrate, closing with a one-line JSON status), and `pending`, `export`, and `report` become interactive review, preview, and diagnostics views. Interactive by default in a terminal — a bare invocation opens the TUI (since v1.5.2). Piped/non-terminal runs and any flag keep the existing output untouched — same JSON on stdout. `flint maintain --tui` (v1.6.0) adds an opt-in curation screen: suspect lists → a what-survives merge preview → merge/keep/invalidate, delegating to the existing subcommands.

Operations: [docs/RUNBOOK.md](docs/RUNBOOK.md) · Tuning: [docs/TUNING.md](docs/TUNING.md) · Metrics: [docs/METRICS.md](docs/METRICS.md) · Performance: [docs/PERF.md](docs/PERF.md) · Docs index: [docs/README.md](docs/README.md)

---

## Agents & Adapters

Every adapter below runs locally — MCP support means MCP clients that can spawn the `flint` binary. AIs without a local runtime use the [bridge](#beyond-local-agents--the-bridge) instead.

| Agent | Method |
| :--- | :--- |
| **Claude Code** | Plugin: hooks + `/flint` command |
| **Codex** | MCP ([docs/adapters/codex.md](docs/adapters/codex.md)) |
| **Gemini** | MCP ([docs/adapters/gemini.md](docs/adapters/gemini.md)) |
| **Copilot** | MCP ([docs/adapters/copilot.md](docs/adapters/copilot.md)) |
| **Cursor** | MCP ([docs/adapters/cursor.md](docs/adapters/cursor.md)) |
| **OpenCode** | MCP ([docs/adapters/opencode.md](docs/adapters/opencode.md)) |
| **Qwen Code** | MCP ([docs/adapters/qwen.md](docs/adapters/qwen.md)) |
| **Devin CLI** | MCP ([docs/adapters/devin.md](docs/adapters/devin.md)) |
| **Cline** | MCP ([docs/adapters/cline.md](docs/adapters/cline.md)) |
| **Windsurf** | MCP ([docs/adapters/windsurf.md](docs/adapters/windsurf.md)) |
| **Zed** | MCP ([docs/adapters/zed.md](docs/adapters/zed.md)) |
| **Amp** | MCP ([docs/adapters/amp.md](docs/adapters/amp.md)) |
| **VS Code** | MCP ([docs/adapters/vscode.md](docs/adapters/vscode.md)) |
| **Continue** | MCP ([docs/adapters/continue.md](docs/adapters/continue.md)) |
| **Goose** | MCP ([docs/adapters/goose.md](docs/adapters/goose.md)) |
| **Trae** | MCP ([docs/adapters/trae.md](docs/adapters/trae.md)) |
| **OpenClaw** | Adapter ([docs/adapters/openclaw.md](docs/adapters/openclaw.md)) |
| **Hermes** | Adapter ([docs/adapters/hermes.md](docs/adapters/hermes.md)) |

Adapter docs: [docs/adapters/](docs/adapters/)

---

```
flint/
├── cmd/flint/          # entry point
├── lib/                # store · query · db · cli · backup · mcp
├── flint-plugin/       # Claude Code plugin
├── docs/               # PLAN · RUNBOOK · TUNING · ADR · adapters
└── docker-compose.yml  # Doltgres on 5434
```

---

## License

flint is open-source software released under the [MIT License](LICENSE).

---

<p align="center">
  <em>Human notes in Obsidian. Agent memory in flint.</em>
  <br/><br/>
  <strong>F L I N T</strong>
</p>
