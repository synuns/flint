// schema_gates.go — the promotion-gate registry. Composes admin.go's
// EnsureSchemaV* methods as a single source — the sequential execution
// of cli/root.go ensureSchemas and the latest schema version
// (LatestSchemaVersion) all derive from here. Adding a gate is just an
// admin.go function + one registry line; a missing registration is
// caught by TestSchemaGatesContiguous·TestSchemaGatesCoverAdmin
// (abolishing the silent skip of adding a function but forgetting the
// call).
package db

import "context"

// LatestSchemaVersion — the latest schema version this CLI knows (the
// ceiling of the status·doctor schema compatibility check. Must equal
// the registry's last element).
const LatestSchemaVersion = 17

// SchemaGate — one version-promotion gate. Version is the target
// schema_version; Ensure is the admin.go gate promoting losslessly to
// that version (idempotent — the numeric-comparison short circuit makes
// it a transaction-free no-op on higher-version DBs).
type SchemaGate struct {
	Version int
	Ensure  func(ctx context.Context, r *Repo) error
}

// SchemaGates — the contiguous promotion order from v2 through
// LatestSchemaVersion (order invariant: a v1 DB skips to the latest
// without missing intermediate columns·indexes·tables). Method-value
// references expose a missing gate function at compile time.
func SchemaGates() []SchemaGate {
	return []SchemaGate{
		{2, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV2(ctx) }},   // sessions.tasks·projects.note columns
		{3, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV3(ctx) }},   // 3 filter-path indexes (ADR-0008)
		{4, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV4(ctx) }},   // reference-metering columns (read_count·last_read_at)
		{5, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV5(ctx) }},   // lineage column (superseded_by)
		{6, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV6(ctx) }},   // effectiveness-metering table (read_events)
		{7, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV7(ctx) }},   // metering refinement (ids·session_starts·hot_events)
		{8, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV8(ctx) }},   // hit-quality approximation (first_rank)
		{9, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV9(ctx) }},   // 4 metering·derived-query indexes (3 ts·pending partial)
		{10, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV10(ctx) }}, // observability extension: read_events bytes·zero_reason·source·knowledge_reads
		{11, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV11(ctx) }}, // decay/reinforcement scores: knowledge recall_score·last_recalled_at
		{12, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV12(ctx) }}, // recall append-only: recall_events table + 2 knowledge columns dropped (derived switch)
		{13, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV13(ctx) }}, // observability foundation: read_events.near_score·threshold_snapshots·weekly_rollups
		{14, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV14(ctx) }}, // knowledge links: knowledge_links table + 2 link indexes + knowledge (superseded_by) index
		{15, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV15(ctx) }}, // link uniqueness seal: uq_knowledge_links_triple (race-proof idempotent accrual)
		{16, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV16(ctx) }}, // vault bridge: vault_files pointer-snapshot table + (content_hash) index
		{17, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV17(ctx) }}, // vault staleness II: vault_files.content_changed_at + knowledge.distilled_at·conditions (3 ALTER ADDs)
	}
}
