// slow_consolidated_test.go — T8 slow real-DB test consolidation.
//
// Each converted test used to call newTestRepo individually, paying the
// per-test scratch-DB creation cost (CREATE DATABASE + full migration +
// a baseline commit, ~0.2s fixed) for a schema state its group recreates
// anyway. The scenarios below create one repo per *group* and share it
// across the original test bodies, which were renamed to slow-prefixed
// helpers taking (t, r) with their bodies — and therefore every
// assertion — unchanged (merge-only consolidation: no test was deleted,
// no assertion dropped).
//
// Grouping safety rules:
//   - Every gate-family body starts from resetVersionToV1 (or drops and
//     re-migrates), so sequential execution on one repo is equivalent to
//     the per-test fresh state; per-subtest t.Cleanup helpers
//     (resetVersionToV1·dropV3Indexes) restore state at subtest end.
//   - The usage family is keyed off meta daily:* / pb_* keys that each
//     body wipes via cleanUsageKeys / cleanPBKeys before asserting.
//   - The transition family seeds its own rows and asserts by row id
//     (no whole-table count assertions), so rows from sibling subtests
//     cannot skew a verdict.
//   - The reads family wipes recall_events / metering tables at entry
//     (t6ClearTelemetry-style), restoring a clean event history per
//     subtest.
//
// The inheritance map (original Test name → consolidated subtest) is
// itself guarded by TestSlowConsolidationInheritance below — the mapping
// table and the subtest names actually registered must stay in sync.
//
// Bodies whose assertions count a whole table (recall_events totals that
// a prior sibling's score-bearing rows would skew — e.g. V11GateAdditive
// leaves recall_score=7 behind, which the V12 gate backfills into 7 more
// synthetic events) run on a dedicated fresh repo inside the scenario
// (freshRepo flag): the body and its assertion stay byte-identical, and
// the original per-test fresh-DB precondition is preserved for exactly
// the tests that need it.
//
// Measured effect: see docs/HISTORY.md (v1.10.0) for the before/after
// per-group runtime table this consolidation was validated with.
package db

import (
	"os"
	"strings"
	"testing"
)

// slowStep — one original test body in a consolidated scenario. fn runs
// the body with the group's shared repo; freshRepo marks bodies whose
// whole-table assertions require the original fresh-repo precondition.
type slowStep struct {
	name      string
	fn        func(*testing.T, *Repo)
	freshRepo bool
}

// runSlowSteps — runs the scenario steps. Shared-repo steps take the
// group repo; freshRepo steps create a dedicated scratch (their
// original isolated precondition).
func runSlowSteps(t *testing.T, r *Repo, steps []slowStep) {
	for _, s := range steps {
		s := s
		t.Run(s.name, func(t *testing.T) {
			if s.freshRepo {
				s.fn(t, newTestRepo(t))
				return
			}
			s.fn(t, r)
		})
	}
}

// TestSlowConsolidatedSchemaGates — the schema-gate family on one repo:
// legacy-deployment walk (v2→v12, each step's promote + numeric no-op
// contract) first, then the fresh-install paths (destructive re-migrate
// scenarios) last so they never disturb the walk's preconditions.
func TestSlowConsolidatedSchemaGates(t *testing.T) {
	r := newTestRepo(t)
	steps := []slowStep{
		{"TestMigrateAcceptsV2", slowMigrateAcceptsV2, false},
		{"TestEnsureSchemaV2", slowEnsureSchemaV2, false},
		{"TestEnsureSchemaV3Idempotent", slowEnsureSchemaV3Idempotent, false},
		{"TestEnsureSchemaV3FromV1", slowEnsureSchemaV3FromV1, false},
		{"TestEnsureSchemaV4Idempotent", slowEnsureSchemaV4Idempotent, false},
		{"TestEnsureSchemaV4FromV1", slowEnsureSchemaV4FromV1, false},
		{"TestEnsureSchemaGatesShortCircuitAbove", slowEnsureSchemaGatesShortCircuitAbove, false},
		{"TestV5PromoteIdempotent", slowV5PromoteIdempotent, false},
		{"TestV5GateNumeric", slowV5GateNumeric, false},
		{"TestV4GateNoDowngradeFromV5", slowV4GateNoDowngradeFromV5, false},
		{"TestV6PromoteIdempotent", slowV6PromoteIdempotent, false},
		{"TestV6GateNumeric", slowV6GateNumeric, false},
		{"TestV7PromoteIdempotent", slowV7PromoteIdempotent, false},
		{"TestV7GateNumeric", slowV7GateNumeric, false},
		{"TestV9PromoteIdempotent", slowV9PromoteIdempotent, false},
		{"TestV9GateNumeric", slowV9GateNumeric, false},
		{"TestV10GateAdditive", slowV10GateAdditive, false},
		{"TestV10GateNumeric", slowV10GateNumeric, false},
		{"TestV11GateAdditive", slowV11GateAdditive, false},
		{"TestV11GateNumeric", slowV11GateNumeric, false},
		{"TestV12GateRecallAppendOnly", slowV12GateRecallAppendOnly, true},
		{"TestV12GateNumeric", slowV12GateNumeric, false},
		// Fresh-install paths — destructive (drop tables + re-Migrate), so
		// they run after the walk.
		{"TestFreshSchemaHasColumn", slowFreshSchemaHasColumn, false},
		{"TestV10FreshSchemaHasArtifacts", slowV10FreshSchemaHasArtifacts, false},
		{"TestV12FreshSchemaHasRecallEvents", slowV12FreshSchemaHasRecallEvents, false},
		{"TestV11GateFreshDirectToV12", slowV11GateFreshDirectToV12, false},
	}
	runSlowSteps(t, r, steps)
}

// TestSlowConsolidatedUsage — the usage/metering family on one repo:
// every body wipes its meta keys (cleanUsageKeys·cleanPBKeys) before
// asserting, so key-state assertions cannot see sibling subtests' rows.
func TestSlowConsolidatedUsage(t *testing.T) {
	r := newTestRepo(t)
	steps := []slowStep{
		{"TestPlaybookStats", slowPlaybookStats, false},
		{"TestPlaybookStatsMultiple", slowPlaybookStatsMultiple, false},
		{"TestPlaybookStatsEmpty", slowPlaybookStatsEmpty, false},
		{"TestRecordUsageCountBatches", slowRecordUsageCountBatches, false},
		{"TestRecordUsageCountDupSources", slowRecordUsageCountDupSources, false},
		{"TestRecordUsageCountBrief", slowRecordUsageCountBrief, false},
		{"TestRecordUsageDailyAndPrune", slowRecordUsageDailyAndPrune, false},
		{"TestRecordUsagePrunesOnlyOnNewDayKey", slowRecordUsagePrunesOnlyOnNewDayKey, false},
		{"TestRecordUsageCorruptDailyRewrites", slowRecordUsageCorruptDailyRewrites, false},
		{"TestRecordUsageWriteEmptySource", slowRecordUsageWriteEmptySource, false},
		{"TestRecordUsageInvalidKind", slowRecordUsageInvalidKind, false},
		{"TestRecordUsageLegacyJSONIgnoredByWrite", slowRecordUsageLegacyJSONIgnoredByWrite, false},
		// Fresh repo: 8 concurrent writers racing the same daily key on a
		// repo whose meta state prior siblings exercised is not the
		// original precondition — on the shared repo the race hits real
		// 40001 retries with 0/1/3s backoff (16.9s measured); isolated it
		// resolves via LWW in well under a second.
		{"TestRecordUsageConcurrentNoError", slowRecordUsageConcurrentNoError, true},
		{"TestRecordBytesDaily", slowRecordBytesDaily, false},
		{"TestRecordBytesCorruptRewrite", slowRecordBytesCorruptRewrite, false},
		{"TestFlatKeyAtomicIncrement", slowFlatKeyAtomicIncrement, false},
		{"TestReadDailyFlatLegacyFallback", slowReadDailyFlatLegacyFallback, false},
		{"TestPruneDailyBothKeyForms", slowPruneDailyBothKeyForms, false},
		{"TestRecordReadEventAndPrune", slowRecordReadEventAndPrune, false},
		{"TestPruneRetentionOverride", slowPruneRetentionOverride, false},
		{"TestSessionStartAndHotEventRecords", slowSessionStartAndHotEventRecords, false},
	}
	runSlowSteps(t, r, steps)
}

// TestSlowConsolidatedTransitions — the knowledge state-transition
// family (approve·invalidate·merge) on one repo. All bodies seed their
// own rows and assert by row id — no whole-table count assertions — so
// shared-repo execution preserves every verdict.
func TestSlowConsolidatedTransitions(t *testing.T) {
	r := newTestRepo(t)
	steps := []slowStep{
		// TestCounts asserts exact global Pending/DupSuspect counts and
		// so runs FIRST, while the scenario's fresh scratch still has an
		// empty knowledge table — its original clean-table precondition.
		{"TestCounts", slowCounts, false},
		{"TestApproveIdempotent", slowApproveIdempotent, false},
		{"TestApproveMissing", slowApproveMissing, false},
		{"TestInvalidateSetsInvalidAt", slowInvalidateSetsInvalidAt, false},
		{"TestMergeMarksAndInvalidates", slowMergeMarksAndInvalidates, false},
		{"TestInvalidateSetsStatus", slowInvalidateSetsStatus, false},
		{"TestApproveClearsInvalidAt", slowApproveClearsInvalidAt, false},
		{"TestMergeRecordsSupersededBy", slowMergeRecordsSupersededBy, false},
		{"TestMergeTwiceRejected", slowMergeTwiceRejected, false},
		{"TestMergePendingRejected", slowMergePendingRejected, false},
		{"TestMergeActiveInvalidAtRejected", slowMergeActiveInvalidAtRejected, false},
		// Admin aggregation leftovers — same family, one repo.
		{"TestStaleSessionRuns", slowStaleSessionRuns, false},
		{"TestAllGatesArtifacts", slowAllGatesArtifacts, false},
		// DupGroupsSameTopic asserts the exact global group set
		// (DeepEqual), so the knowledge table may hold only its own rows
		// — TestCounts' dup-topic seed would add a second group. It keeps
		// the original isolated-table precondition (freshRepo).
		{"TestDupGroupsSameTopic", slowDupGroupsSameTopic, true},
	}
	runSlowSteps(t, r, steps)
}

// TestSlowConsolidatedReads — the read/recall metering family on one
// repo. Every body clears recall_events (and its own probe rows) before
// asserting event counts, restoring a clean history per subtest.
func TestSlowConsolidatedReads(t *testing.T) {
	r := newTestRepo(t)
	steps := []slowStep{
		{"TestBumpReadsIncrements", slowBumpReadsIncrements, false},
		{"TestBumpReadsDuplicateIDs", slowBumpReadsDuplicateIDs, false},
		{"TestBumpRecallScoresIncrements", slowBumpRecallScoresIncrements, false},
		{"TestBumpRecallScoresDuplicateIDs", slowBumpRecallScoresDuplicateIDs, false},
		{"TestBumpRecallScoresInsertsEvents", slowBumpRecallScoresInsertsEvents, false},
		{"TestRecallEventsAuditStaged", slowRecallEventsAuditStaged, false},
		{"TestReplayRecallEventsSynthesizesEvents", slowReplayRecallEventsSynthesizesEvents, false},
		{"TestPruneInvalidatedCleansRecallOrphans", slowPruneInvalidatedCleansRecallOrphans, false},
		{"TestPruneRecallEventsRetentionOnlyExpired", slowPruneRecallEventsRetentionOnlyExpired, true},
		// reads_v1200 pair — both bodies normalize state via
		// ensureV1200Schema (version reset + full gate walk) at entry.
		{"TestRecordReadEventColumns", slowRecordReadEventColumns, false},
		{"TestRecordKnowledgeReads", slowRecordKnowledgeReads, false},
	}
	runSlowSteps(t, r, steps)
}

// consolidatedOrigins — the inheritance ledger: every original Test name
// whose body now runs as a consolidated subtest (T8 merge-only
// consolidation). The guard below pins three invariants: (1) each name
// is registered as a subtest in this file, (2) its body still exists as
// a slow-prefixed helper in the package sources, (3) no Test function of
// that name remains in the per-test files (it moved here, not forked).
var consolidatedOrigins = []string{
	// Schema gate family
	"TestMigrateAcceptsV2", "TestEnsureSchemaV2",
	"TestEnsureSchemaV3Idempotent", "TestEnsureSchemaV3FromV1",
	"TestEnsureSchemaV4Idempotent", "TestEnsureSchemaV4FromV1",
	"TestEnsureSchemaGatesShortCircuitAbove", "TestV5PromoteIdempotent",
	"TestV5GateNumeric", "TestV4GateNoDowngradeFromV5",
	"TestV6PromoteIdempotent", "TestV6GateNumeric",
	"TestV7PromoteIdempotent", "TestV7GateNumeric",
	"TestV9PromoteIdempotent", "TestV9GateNumeric",
	"TestV10GateAdditive", "TestV10GateNumeric",
	"TestV11GateAdditive", "TestV11GateNumeric",
	"TestV12GateRecallAppendOnly", "TestV12GateNumeric",
	"TestFreshSchemaHasColumn", "TestV10FreshSchemaHasArtifacts",
	"TestV12FreshSchemaHasRecallEvents", "TestV11GateFreshDirectToV12",
	// Usage/metering family
	"TestPlaybookStats", "TestPlaybookStatsMultiple", "TestPlaybookStatsEmpty",
	"TestRecordUsageCountBatches", "TestRecordUsageCountDupSources",
	"TestRecordUsageCountBrief", "TestRecordUsageDailyAndPrune",
	"TestRecordUsagePrunesOnlyOnNewDayKey", "TestRecordUsageCorruptDailyRewrites",
	"TestRecordUsageWriteEmptySource", "TestRecordUsageInvalidKind",
	"TestRecordUsageLegacyJSONIgnoredByWrite", "TestRecordUsageConcurrentNoError",
	"TestRecordBytesDaily", "TestRecordBytesCorruptRewrite",
	"TestFlatKeyAtomicIncrement", "TestReadDailyFlatLegacyFallback",
	"TestPruneDailyBothKeyForms", "TestRecordReadEventAndPrune",
	"TestPruneRetentionOverride", "TestSessionStartAndHotEventRecords",
	// Transition family
	"TestApproveIdempotent", "TestApproveMissing",
	"TestInvalidateSetsInvalidAt", "TestMergeMarksAndInvalidates",
	"TestInvalidateSetsStatus", "TestApproveClearsInvalidAt",
	"TestMergeRecordsSupersededBy", "TestMergeTwiceRejected",
	"TestMergePendingRejected", "TestMergeActiveInvalidAtRejected",
	// Reads/recall family
	"TestBumpReadsIncrements", "TestBumpReadsDuplicateIDs",
	"TestBumpRecallScoresIncrements", "TestBumpRecallScoresDuplicateIDs",
	"TestBumpRecallScoresInsertsEvents", "TestRecallEventsAuditStaged",
	"TestReplayRecallEventsSynthesizesEvents",
	"TestPruneInvalidatedCleansRecallOrphans",
	"TestPruneRecallEventsRetentionOnlyExpired",
	"TestRecordReadEventColumns", "TestRecordKnowledgeReads",
	"TestCounts", "TestStaleSessionRuns", "TestAllGatesArtifacts",
	"TestDupGroupsSameTopic",
}

// TestSlowConsolidationInheritance — the assertion-inheritance ledger
// guard (pure, no DB): every original name is registered as a subtest of
// a consolidated scenario, its body helper still exists in the package
// sources, and no per-test file still declares the old Test function
// (moved, not duplicated).
func TestSlowConsolidationInheritance(t *testing.T) {
	self, err := os.ReadFile("slow_consolidated_test.go")
	if err != nil {
		t.Fatalf("read consolidated source: %v", err)
	}
	srcs := map[string]string{}
	for _, f := range []string{
		"admin_test.go", "admin_v0190_test.go", "admin_v0200_test.go",
		"admin_v0201_test.go", "admin_v0420_test.go", "admin_v06_test.go",
		"admin_v08_test.go", "admin_v10_test.go", "admin_v1200_test.go",
		"usage_test.go", "usage_v092_test.go", "usage_v0102_test.go",
		"usage_v0341_test.go", "usage_bytes_test.go", "usage_flatkey_test.go",
		"usage_v06_test.go", "usage_v0190_test.go", "usage_v0200_test.go",
		"admin_merge_test.go", "admin_v093_test.go", "reads_v1200_test.go",
		"reads_v06_test.go", "reads_v1700_test.go",
		"reads_v1900_test.go", "prune_recall_v1910_test.go",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		srcs[f] = string(b)
	}
	for _, name := range consolidatedOrigins {
		// ① registered as a subtest here
		if !strings.Contains(string(self), `{"`+name+`", slow`) {
			t.Errorf("%s: this file does not register a consolidated subtest (ledger drift)", name)
		}
		// ② body helper exists somewhere in the package sources
		body := "slow" + strings.TrimPrefix(name, "Test") + "(t *testing.T, r *Repo)"
		found := false
		for f, src := range srcs {
			if strings.Contains(src, "func "+body) {
				found = true
				// ③ no old Test function remains (moved, not forked)
				if strings.Contains(src, "func "+name+"(t *testing.T)") {
					t.Errorf("%s: %s still declares the old Test func (forked, not moved)", name, f)
				}
				break
			}
		}
		if !found {
			t.Errorf("%s: body helper %q not found in package sources (assertions lost)", name, body)
		}
	}
}
