// usage_registry_test.go — v0.34.2 R2 usage 드리프트 가드 3종
// (v0.38.0 §1: lib/cli에서 계약층으로 이동·패키지명 갱신).
// wire 패키지 단독 실행 시 registry가 비어 있으므로 seedCommands가
// 실 커맨드 표면(사람 노출 24종 + 훅 전용 2종)을 미러링해 등록한다 —
// 신규 커맨드 추가 시 이 목록과 usageOrder가 함께 갱신되어야 한다
// (기존 cli 테스트와 동일한 유지보수 계약).
package wire

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

// seedCommands — 실 커맨드 표면 미러(사람 노출 = usageOrder와 동일 집합
// 이어야 함 + 훅 전용 session-start/end). 핸들러는 검증 대상 아님.
var seedCommands = []string{
	"write", "read", "hot", "brief", "status", "profile", "backup", "restore",
	"approve", "pending", "maintain", "doctor", "report",
	"done", "compact", "nudge", "setup", "teardown", "version", "mcp",
	"audit", "diff", "update", "tuning", "import",
	"session-start", "session-end",
}

// seedRegistry — seedCommands를 stub 핸들러로 등록(테스트 후 원복).
func seedRegistry(t *testing.T) {
	t.Helper()
	stub := func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 }
	for _, name := range seedCommands {
		n := name
		Register(n, stub)
		t.Cleanup(func() { Unregister(n) })
	}
}

// TestUsageLineListsAllRegistered — usageLine은 registry에 등록된 모든
// 사용자 노출 커맨드를 나열해야 한다. session-start/end는 훅 전용
// 진입점이라 제외(wire_v08 TestUsageRestInvariant 계약과 양립 불가).
func TestUsageLineListsAllRegistered(t *testing.T) {
	seedRegistry(t)
	line := UsageLine()
	for _, name := range seedCommands {
		if hiddenFromUsage(name) {
			if strings.Contains(line, name) {
				t.Fatalf("훅 전용 %q이 usage에 노출됨", name)
			}
			continue
		}
		if !strings.Contains(line, name) {
			t.Fatalf("usage missing registered command %q", name)
		}
	}
	if !strings.Contains(line, "pending") {
		t.Fatal("pending missing — R2")
	}
}

// TestUsageOrderMatchesRegistry — usageOrder 집합 == registry 키 집합
// (훅 전용 제외). 신규 커맨드를 Register만 하고 usageOrder에 넣지 않으면
// 이 테스트가 FAIL한다 — 영구 드리프트 가드.
func TestUsageOrderMatchesRegistry(t *testing.T) {
	seedRegistry(t)
	want := map[string]bool{}
	for _, name := range seedCommands {
		if !hiddenFromUsage(name) {
			want[name] = true
		}
	}
	got := map[string]bool{}
	for _, name := range usageOrder {
		if got[name] {
			t.Fatalf("usageOrder 중복: %q", name)
		}
		got[name] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageOrder/registry 불일치\n usageOrder=%v\nregistry=%v", got, want)
	}
}

// TestUsageLineAssembledFromOrder — usageLine은 usageOrder 순서 그대로
// 조립(리터럴 금지, v0.34.2 Produces 계약).
func TestUsageLineAssembledFromOrder(t *testing.T) {
	want := `{"error":"usage: flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
	if UsageLine() != want {
		t.Fatalf("usageLine이 usageOrder 조립과 불일치:\n got=%s\nwant=%s", UsageLine(), want)
	}
}
