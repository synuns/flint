package db

import (
	"os"
	"strings"
	"testing"
)

type slowStep struct {
	name      string
	fn        func(*testing.T, *Repo)
	freshRepo bool
}

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

		{"TestFreshSchemaHasColumn", slowFreshSchemaHasColumn, false},
		{"TestV10FreshSchemaHasArtifacts", slowV10FreshSchemaHasArtifacts, false},
		{"TestV12FreshSchemaHasRecallEvents", slowV12FreshSchemaHasRecallEvents, false},
		{"TestV11GateFreshDirectToV12", slowV11GateFreshDirectToV12, false},
	}
	runSlowSteps(t, r, steps)
}

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

func TestSlowConsolidatedTransitions(t *testing.T) {
	r := newTestRepo(t)
	steps := []slowStep{

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

		{"TestStaleSessionRuns", slowStaleSessionRuns, false},
		{"TestAllGatesArtifacts", slowAllGatesArtifacts, false},

		{"TestDupGroupsSameTopic", slowDupGroupsSameTopic, true},
	}
	runSlowSteps(t, r, steps)
}

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

		{"TestRecordReadEventColumns", slowRecordReadEventColumns, false},
		{"TestRecordKnowledgeReads", slowRecordKnowledgeReads, false},
	}
	runSlowSteps(t, r, steps)
}

var consolidatedOrigins = []string{

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

	"TestApproveIdempotent", "TestApproveMissing",
	"TestInvalidateSetsInvalidAt", "TestMergeMarksAndInvalidates",
	"TestInvalidateSetsStatus", "TestApproveClearsInvalidAt",
	"TestMergeRecordsSupersededBy", "TestMergeTwiceRejected",
	"TestMergePendingRejected", "TestMergeActiveInvalidAtRejected",

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

func TestSlowConsolidationInheritance(t *testing.T) {
	self, err := os.ReadFile("slow_consolidated_test.go")
	if err != nil {
		t.Fatalf("read consolidated source: %v", err)
	}
	srcs := map[string]string{}
	for _, f := range []string{
		"admin_test.go", "admin_versions_test.go",
		"usage_test.go", "usage_bytes_test.go", "usage_flatkey_test.go",
		"usage_versions_test.go",
		"admin_merge_test.go", "reads_v1200_test.go",
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

		if !strings.Contains(string(self), `{"`+name+`", slow`) {
			t.Errorf("%s: this file does not register a consolidated subtest (ledger drift)", name)
		}

		body := "slow" + strings.TrimPrefix(name, "Test") + "(t *testing.T, r *Repo)"
		found := false
		for f, src := range srcs {
			if strings.Contains(src, "func "+body) {
				found = true

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
