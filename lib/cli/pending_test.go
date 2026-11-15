// pending_test.go — v1.5.0 T7: `flint pending --tui` 진입 분기 계약 테스트.
// 기본 경로(플래그 없음·--json·unknown flag)는 knowledge.RunPending에
// 완전 불변 위임(회귀), --tui만 TUI seam으로 분기. TUI 런처는 seam var
// fake 주입(기본 실구현 = tui.RunPendingTUI — SUPERLITE_REAL=1 +
// FLINT_TEST_DSN 시 withFake 위임 fake로 실DB 경로).
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// installPendingTuiFake — pendingTuiFn seam 교체(호출 기록 fake).
func installPendingTuiFake(t *testing.T) *[]wire.Store {
	t.Helper()
	var calls []wire.Store
	orig := pendingTuiFn
	pendingTuiFn = func(ctx context.Context, st wire.Store, stdout io.Writer) int {
		calls = append(calls, st)
		return 0
	}
	t.Cleanup(func() { pendingTuiFn = orig })
	return &calls
}

// pendingDemoStore — 대기 1건을 내주는 fake(pending 조회 + active 조회).
func pendingDemoStore() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{7, "demo topic alpha", "demo summary alpha", "hook", "2026-11-14T10:00:00Z"}}, nil
		}
		return nil, nil
	}}
}

// RF-protocol — 기본 경로 회귀: 플래그 없음은 RunPending과 바이트 동일.
func TestPendingEntryDefaultDelegatesUnchanged(t *testing.T) {
	var mine, ref bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &mine)
	wantCode := knowledge.RunPending(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &ref)
	if code != wantCode {
		t.Fatalf("exit = %d, want %d", code, wantCode)
	}
	if mine.String() != ref.String() {
		t.Fatalf("default path drifted:\ngot:  %q\nwant: %q", mine.String(), ref.String())
	}
}

// RF-protocol — 기존 플래그 회귀: --json은 RunPending 경로(기계 출력),
// unknown flag는 기존 exit 3. --tui 미포함 시 TUI seam 미호출.
func TestPendingEntryJSONAndUnknownFlagRegression(t *testing.T) {
	calls := installPendingTuiFake(t)

	var out bytes.Buffer
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("--json exit = %d, want 0 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("--json output = %q, want machine JSON", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("--json must not reach TUI seam, calls = %d", len(*calls))
	}

	out.Reset()
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--bogus"},
		strings.NewReader(""), &out); code != 3 {
		t.Fatalf("unknown flag exit = %d, want 3", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("unknown flag must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — --tui 분기: TUI seam 호출·exit 전파·스토어 전달.
func TestPendingEntryTUIBranch(t *testing.T) {
	calls := installPendingTuiFake(t)
	st := pendingDemoStore()
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), st, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("--tui exit = %d, want seam-returned 0", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("TUI seam calls = %d, want 1", len(*calls))
	}
}

// RF-protocol — --tui + 다른 플래그: 무효 인자 exit 3(부분 조합 금지).
func TestPendingEntryTUIRejectsExtraFlags(t *testing.T) {
	calls := installPendingTuiFake(t)
	var out bytes.Buffer
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--tui", "--json"},
		strings.NewReader(""), &out); code != 3 {
		t.Fatalf("--tui --json exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "unknown flag") {
		t.Fatalf("output = %q, want unknown flag error", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected combo must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — 등록표: "pending"은 이 진입 분기 핸들러로 최종 등록
// (knowledge.init 등록을 덮어쓴다 — wire.Register 최신값 승계).
func TestPendingEntryRegistered(t *testing.T) {
	calls := installPendingTuiFake(t)
	h, ok := wire.Lookup("pending")
	if !ok {
		t.Fatal("pending not registered")
	}
	var out bytes.Buffer
	if code := h(context.Background(), pendingDemoStore(), []string{"--tui"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("registry dispatch exit = %d, want 0", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("registry dispatch must reach TUI seam, calls = %d", len(*calls))
	}
}
