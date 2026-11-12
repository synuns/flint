// fake_parity_test.go — v0.43.0 T1: knowledge 패키지 fake-capability 동질
// 가드(스펙 §1, 2026-11-11 리뷰 병인 ③). 패키지의 모든 capability
// 인터페이스를 컴파일 시점에 fake에 단언한다 — 미구현 메서드는 패리티
// fake(fakeApprove)의 canned 구현(호출 기록 슬라이스·선택적 capErr
// 주입)이 채운다. 의도적 폴백 검증 fake(레거시 fake 호환 — 비목표로
// 프로덕션 폴백 코드 유지)는 그대로 둔다.
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

// 컴파일 시점 단언 전수 — 패키지 capability 인터페이스 전부를 패리티
// fake(fakeApprove)에 단언한다. 신규 인터페이스 추가 시
// TestFakeParityKnowledge가 발견 목록 ≠ 단언 목록으로 실패해 이 블록
// 갱신을 강제한다(인터페이스마다 최소 1단언).
var (
	_ approveStore        = (*fakeApprove)(nil)
	_ supersedeStore      = (*fakeApprove)(nil)
	_ readBumper          = (*fakeApprove)(nil)
	_ ReadEventStore      = (*fakeApprove)(nil)
	_ HotEventRecorder    = (*fakeApprove)(nil)
	_ UsageStore          = (*fakeApprove)(nil)
	_ DupIDStore          = (*fakeApprove)(nil)
	_ DupMetaStore        = (*fakeApprove)(nil)
	_ UsageCountStore     = (*fakeApprove)(nil)
	_ staleSessionStore   = (*fakeApprove)(nil)
	_ usageStore          = (*fakeApprove)(nil)
	_ sessionSummaryStore = (*fakeApprove)(nil)

	// 기존 capability fake의 자연 단언(이미 구현 — 문서화 + 시그니처
	// 드리프트 회귀 가드).
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
)

// TestFakeParityKnowledge — Review Focus: 인터페이스 발견 목록(패키지
// 비테스트 소스의 interface 전수)과 단언 목록(이 파일의 `var _ X =`
// 단언)의 동질을 소스 스캔으로 검증. 단언 전수 자체는 컴파일이 보장
// 하고, 이 가드는 신규 capability 인터페이스가 단언 없이 추가되는
// 사각을 막는다.
func TestFakeParityKnowledge(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("테스트 파일 경로 미확보")
	}
	dir := filepath.Dir(thisFile)

	// 발견 — 비테스트 소스의 interface 전수(capability 여부와 무관하게
	// 패키지 인터페이스는 전부 단언 대상 — 동질이 계약).
	ifaceRe := regexp.MustCompile(`(?m)^type (\w+) interface \{`)
	srcs, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("소스 글로브 실패: %v", err)
	}
	discovered := map[string]string{} // 인터페이스명 → 정의 파일
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

	// 단언 — 이 파일의 `var _ X = (*Y)(nil)` 전수.
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

	// 동질 — 양방향 차집합 0.
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

// TestFakeParityKnowledgeCanned — 패리티 fake canned 구현 행위: 미지정 시
// 영(零) 동작(성공·0행·부재) + 호출 슬라이스 기록, capErr 지정 시 전
// 메서드 오류 반환.
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
		func() error { _, err := f.StaleSessionRuns(ctx, "claude"); return err },
		func() error { return f.LogPlaybookUse(ctx, 9, true) },
		func() error { _, err := f.UpdateSessionSummary(ctx, 7, "요약", "[]"); return err },
		func() error { return f.Approve(ctx, 5) },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("%d번째 canned 호출 오류(미지정 시 성공): %v", i, err)
		}
	}
	// 기록 — 각 capability 슬라이스에 정확히 1건.
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
	if len(f.staleAgents) != 1 || f.staleAgents[0] != "claude" {
		t.Errorf("StaleSessionRuns 기록=%v, want [claude]", f.staleAgents)
	}
	if len(f.playbookUses) != 1 || f.playbookUses[0] != (playbookUse{id: 9, ok: true}) {
		t.Errorf("LogPlaybookUse 기록=%v, want [{9 true}]", f.playbookUses)
	}
	if len(f.sessionUpdates) != 1 || f.sessionUpdates[0].id != 7 {
		t.Errorf("UpdateSessionSummary 기록=%v, want 1건(id 7)", f.sessionUpdates)
	}

	// 오류 주입 — capErr 지정 시 전 capability가 이를 반환.
	f.capErr = errors.New("canned boom")
	for i, call := range calls {
		if err := call(); err != f.capErr {
			t.Fatalf("%d번째 canned 호출 capErr 미반환: %v", i, err)
		}
	}
}
