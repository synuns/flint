// fake_parity_test.go — the knowledge package's fake-capability
// equivalence guard (spec §1, 2026-11-11 review root ③). Asserts every
// capability interface of the package onto the fake at compile time —
// unimplemented methods are filled by the parity fake (fakeApprove)'s
// canned implementations (call-recording slices·optional capErr
// injection). Deliberate fallback-verification fakes (legacy fake
// compatibility — a non-goal of keeping production fallback code) stay
// as-is.
package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// The full set of compile-time assertions — every package capability
// interface asserted onto the parity fake (fakeApprove). When a new
// interface is added, TestFakeParityKnowledge fails with discovered list
// ≠ asserted list, forcing an update of this block (at least one
// assertion per interface).
var (
	_ approveStore           = (*fakeApprove)(nil)
	_ supersedeStore         = (*fakeApprove)(nil)
	_ readBumper             = (*fakeApprove)(nil)
	_ ReadEventStore         = (*fakeApprove)(nil)
	_ HotEventRecorder       = (*fakeApprove)(nil)
	_ UsageStore             = (*fakeApprove)(nil)
	_ DupIDStore             = (*fakeApprove)(nil)
	_ DupMetaStore           = (*fakeApprove)(nil)
	_ UsageCountStore        = (*fakeApprove)(nil)
	_ BytesRecorder          = (*fakeApprove)(nil)
	_ KnowledgeReadsRecorder = (*fakeApprove)(nil)
	_ RecallBumper           = (*fakeApprove)(nil)
	_ staleSessionStore      = (*fakeApprove)(nil)
	_ usageStore             = (*fakeApprove)(nil)
	_ sessionSummaryStore    = (*fakeApprove)(nil)
	_ supersedeRefStore      = (*fakeApprove)(nil) // the lineage-reference restoration capability
	_ TelemetryCommitter     = (*fakeApprove)(nil) // the separated metering-commit capability
	_ recallReplayStore      = (*fakeApprove)(nil) // the migration score-replay capability (R42-A)

	// Natural assertions of the existing capability fakes (already
	// implemented — documentation + a signature-drift regression guard).
	_ approveStore        = (*approveStoreFake)(nil)
	_ staleSessionStore   = (*doneFake)(nil)
	_ sessionSummaryStore = (*doneFake)(nil)
	_ staleSessionStore   = (*staleFake)(nil)
	_ ReadEventStore      = (*readEventRecorder)(nil)
	_ readBumper          = (*readBudgetRecorder)(nil)
	_ ReadEventStore      = (*readBudgetRecorder)(nil)
	_ readBumper          = (*hotBumpRecorder)(nil)
	_ HotEventRecorder    = (*hotBumpRecorder)(nil)
	_ UsageStore          = (*countRecorder)(nil)
	_ UsageCountStore     = (*countRecorder)(nil)
	_ BytesRecorder       = (*bytesRecorder)(nil)
)

// TestFakeParityKnowledge — verifies by source scan that the discovered
// interface list (every interface in the package's non-test sources) and
// the asserted list (the `var _ X =` assertions in this file) are
// equivalent. The compilation itself guarantees the assertions work;
// this guard blocks the blind spot of a new capability interface being
// added without an assertion.
func TestFakeParityKnowledge(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("테스트 파일 경로 미확보")
	}
	dir := filepath.Dir(thisFile)

	// Discovery — every interface in the non-test sources (capability or
	// not, every package interface is an assertion target — equivalence is
	// the contract).
	ifaceRe := regexp.MustCompile(`(?m)^type (\w+) interface \{`)
	srcs, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("소스 글로브 실패: %v", err)
	}
	discovered := map[string]string{} // interface name → defining file
	for _, src := range srcs {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("소스 읽기 실패 %s: %v", src, err)
		}
		for _, m := range ifaceRe.FindAllStringSubmatch(string(b), -1) {
			discovered[m[1]] = filepath.Base(src)
		}
	}
	if len(discovered) == 0 {
		t.Fatal("인터페이스 발견 0건 — 스캔 자체 오류 의심")
	}

	// Assertions — every `var _ X = (*Y)(nil)` in this file.
	self, err := os.ReadFile(thisFile)
	if err != nil {
		t.Fatalf("단언 파일 읽기 실패: %v", err)
	}
	assertRe := regexp.MustCompile(`(?m)^[ \t]*(?:var[ \t]+)?_[ \t]+(\w+)[ \t]*=[ \t]*\(\*(\w+)\)\(nil\)`)
	asserted := map[string]bool{}
	for _, m := range assertRe.FindAllStringSubmatch(string(self), -1) {
		asserted[m[1]] = true
	}
	if len(asserted) == 0 {
		t.Fatal("단언 발견 0건 — 단언 블록 파싱 오류 의심")
	}

	// Equivalence — both directional set differences are empty.
	for name, file := range discovered {
		if !asserted[name] {
			t.Errorf("인터페이스 %s(%s) 단언 없음 — 이 파일 var 블록에\n"+
				"\tvar _ %s = (*fakeApprove)(nil)\n추가(미구현 메서드는 fakeApprove canned 구현)", file, name, name)
		}
	}
	for name := range asserted {
		if _, ok := discovered[name]; !ok {
			t.Errorf("단언 %s는 패키지 비테스트 소스에 없는 인터페이스 — 단언 정리 필요", name)
		}
	}
}

// TestFakeParityKnowledgeCanned — the parity fake's canned
// implementation behavior: a no-op when unset (success·0 rows·absence) +
// call-slice recording; with capErr set, every method returns it.
func TestFakeParityKnowledgeCanned(t *testing.T) {
	ctx := context.Background()
	f := &fakeApprove{}
	calls := []func() error{
		func() error { _, err := f.Supersede(ctx, 3, 4, time.Unix(0, 0).UTC()); return err },
		func() error { return f.BumpReads(ctx, []int{1, 2}) },
		func() error { return f.RecordReadEvent(ctx, db.ReadEvent{Hits: 2}) },
		func() error { return f.RecordHotEvent(ctx, "1,2") },
		func() error { return f.RecordUsage(ctx, "write", "manual") },
		func() error { _, _, err := f.DupIDByHash(ctx, "해시"); return err },
		func() error { _, _, err := f.DupRowByHash(ctx, "해시"); return err },
		func() error { return f.RecordUsageCount(ctx, "dup", "", 3) },
		func() error { return f.RecordBytes(ctx, "hot", 1180) },
		func() error { return f.RecordKnowledgeReads(ctx, []int{1, 2}, "kr", "read") },
		func() error { _, err := f.StaleSessionRuns(ctx, "claude"); return err },
		func() error { return f.LogPlaybookUse(ctx, 9, true) },
		func() error { _, err := f.UpdateSessionSummary(ctx, 7, "요약", "[]"); return err },
		func() error { return f.Approve(ctx, 5) },
		func() error { return f.CommitTelemetry(ctx, "read") },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("%d번째 canned 호출 오류(미지정 시 성공): %v", i, err)
		}
	}
	// Recording — exactly 1 entry per capability slice.
	if len(f.approveIDs) != 1 || f.approveIDs[0] != 5 {
		t.Errorf("Approve 기록=%v, want [5]", f.approveIDs)
	}
	if len(f.superseded) != 1 || f.superseded[0] != (supersedeCall{oldID: 3, newID: 4}) {
		t.Errorf("Supersede 기록=%v, want [{3 4}]", f.superseded)
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 2 {
		t.Errorf("BumpReads 기록=%v, want 1회(ids 2건)", f.bumped)
	}
	if len(f.readEvents) != 1 || f.readEvents[0].Hits != 2 {
		t.Errorf("RecordReadEvent 기록=%v, want 1건(Hits 2)", f.readEvents)
	}
	if len(f.hotEvents) != 1 || f.hotEvents[0] != "1,2" {
		t.Errorf("RecordHotEvent 기록=%v, want [1,2]", f.hotEvents)
	}
	if len(f.recordedUses) != 1 || f.recordedUses[0] != (countCall{"write", "manual", 1}) {
		t.Errorf("RecordUsage 기록=%v, want [write manual]", f.recordedUses)
	}
	if len(f.dupIDQueries) != 1 {
		t.Errorf("DupIDByHash 기록=%v, want 1건", f.dupIDQueries)
	}
	if len(f.dupRowQueries) != 1 {
		t.Errorf("DupRowByHash 기록=%v, want 1건", f.dupRowQueries)
	}
	if len(f.countedUses) != 1 || f.countedUses[0] != (countCall{"dup", "", 3}) {
		t.Errorf("RecordUsageCount 기록=%v, want [dup \"\" 3]", f.countedUses)
	}
	if len(f.recordedBytes) != 1 || f.recordedBytes[0] != (bytesCall{"hot", 1180}) {
		t.Errorf("RecordBytes 기록=%v, want [hot 1180]", f.recordedBytes)
	}
	if len(f.kreads) != 1 || len(f.kreads[0].ids) != 2 || f.kreads[0].kind != "read" {
		t.Errorf("RecordKnowledgeReads 기록=%v, want 1회(ids 2건·kind=read)", f.kreads)
	}
	if len(f.staleAgents) != 1 || f.staleAgents[0] != "claude" {
		t.Errorf("StaleSessionRuns 기록=%v, want [claude]", f.staleAgents)
	}
	if len(f.playbookUses) != 1 || f.playbookUses[0] != (playbookUse{id: 9, ok: true}) {
		t.Errorf("LogPlaybookUse 기록=%v, want [{9 true}]", f.playbookUses)
	}
	if len(f.sessionUpdates) != 1 || f.sessionUpdates[0].id != 7 {
		t.Errorf("UpdateSessionSummary 기록=%v, want 1건(id 7)", f.sessionUpdates)
	}
	if len(f.telemetryCmds) != 1 || f.telemetryCmds[0] != "read" {
		t.Errorf("CommitTelemetry 기록=%v, want [read]", f.telemetryCmds)
	}

	// Error injection — with capErr set, every capability returns it.
	f.capErr = errors.New("canned boom")
	for i, call := range calls {
		if err := call(); err != f.capErr {
			t.Fatalf("%d번째 canned 호출 capErr 미반환: %v", i, err)
		}
	}
}
