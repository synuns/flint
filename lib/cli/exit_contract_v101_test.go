// exit_contract_v101_test.go — v1.0.1(F3) 회귀: 전 커맨드 무효 인자 exit
// 코드 표. 계약(docs/PLAN.md §3 — exit 3 = 검증 실패·에이전트 자가수정):
// unknown flag·플래그 값 누락·무효 값은 exit 3으로 통일하고, 쿼리 텍스트
// 흡수(read·brief 등)·훅 비간섭(compact·nudge)은 문서화된 예외로 둔다.
// 표는 wire.UsageOrder 전 커맨드를 강제 덮는다(TestExitContractCoversUsage
// Order) — 신규 커맨드 추가 시 미분류면 이 테스트가 실패한다(구동 행
// 추가 또는 quietAbsorb 명단 분류 필수).
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// exitProbe — 표 행: (커맨드, 인자) 호출의 무효 인자 exit 기대.
type exitProbe struct {
	cmd  string
	args []string
	want int
	note string
}

// exitContractProbes — 무효 인자 경로 구동 표. v1.0.1(F3) 변경분(1→3)과
// 기존 3 계약(read·brief·import·maintain·doctor·tuning·diff)을 한 표에 둔다.
var exitContractProbes = []exitProbe{
	// unknown flag — 무효 인자 exit 3(v1.0.1 F3: 8종 1→3 통일)
	{"approve", []string{"--wat"}, 3, "F3 unknown flag"},
	{"pending", []string{"--wat"}, 3, "F3 unknown flag"},
	{"backup", []string{"--wat"}, 3, "F3 unknown flag"},
	{"restore", []string{"--wat"}, 3, "F3 unknown flag"},
	{"audit", []string{"--bogus"}, 3, "F3 unknown flag"},
	{"report", []string{"--bogus"}, 3, "F3 unknown flag"},
	{"profile", []string{"--verbose"}, 3, "F3 unknown flag"},
	{"setup", []string{"--bogus"}, 3, "F3 unknown flag"},
	{"update", []string{"--bogus"}, 3, "F3 unknown flag"},
	{"teardown", []string{"--bogus"}, 3, "F3 unknown flag(인접 동류)"},
	{"maintain", []string{"--check", "--bogus"}, 3, "v0.36.1 관례(기존)"},
	{"import", []string{"--verbose"}, 3, "v0.36.1 관례(기존)"},
	{"doctor", []string{"--bogus"}, 3, "전역 계약(기존)"},
	{"tuning", []string{"--bogus"}, 3, "서브커맨드 아님 → usage 3"},
	{"diff", []string{"--bogus"}, 3, "위치 인자 흡수 → 인자 수 usage 3"},
	{"export", []string{"--bogus"}, 3, "v1.3.0 T7 unknown flag"},
	// 플래그 값 누락 — 무효 인자 exit 3
	{"approve", []string{"--id"}, 3, "F3 값 누락"},
	{"audit", []string{"--limit"}, 3, "F3 값 누락"},
	{"audit", []string{"--session"}, 3, "F3 값 누락"},
	{"backup", []string{"--out"}, 3, "F3 값 누락"},
	{"restore", []string{"--file"}, 3, "F3 값 누락"},
	{"report", []string{"--days"}, 3, "F3 값 누락"},
	{"profile", []string{"--apply"}, 3, "F3 값 누락"},
	{"setup", []string{"--src"}, 3, "F3 값 누락"},
	{"maintain", []string{"--limit"}, 3, "F3 값 누락"},
	{"setup", []string{"--exposure"}, 3, "기존 3 계약"},
	// 무효 값 — 무효 인자 exit 3
	{"approve", []string{"--id", "abc"}, 3, "F3 무효 id(mcp 정합)"},
	{"audit", []string{"--limit", "abc"}, 3, "F3 무효 숫자(read·brief 정합)"},
	{"report", []string{"--days", "abc"}, 3, "F3 무효 숫자"},
	{"maintain", []string{"--limit", "abc"}, 3, "F3 무효 숫자"},
	{"profile", []string{"--apply", "bogus"}, 3, "F3 무효 값(setup --exposure 정합)"},
	{"export", []string{"--format", "xml"}, 3, "v1.3.0 T7 무효 format"},
	{"export", []string{"--since", "not-a-date"}, 3, "v1.3.0 T7 무효 since"},
	{"export", []string{"--out"}, 3, "v1.3.0 T7 값 누락"},
	{"read", []string{"--limit", "abc"}, 3, "기존 3 계약"},
	{"brief", []string{"--limit", "abc"}, 3, "기존 3 계약"},
	// --session 라벨 가드 — read 계약의 hot·done 승격(v1.0.1 F3·4번)
	{"read", []string{"--session", "--json"}, 3, "세션 가드(기존)"},
	{"hot", []string{"--session", "--full"}, 3, "F3 세션 가드 승격"},
	{"hot", []string{"--session"}, 3, "F3 세션 가드(빈 값)"},
	{"done", []string{"--session", "--json"}, 3, "F3 세션 가드 승격"},
	{"done", []string{"--session"}, 3, "F3 세션 가드(빈 값)"},
}

// quietAbsorb — unknown flag를 쿼리 텍스트·무시 경로로 조용히 흡수하는
// 문서화된 예외 명단(F7 docs/contracts/cli.md 문서화 분담). 값은 근거.
var quietAbsorb = map[string]string{
	"read":    "쿼리 텍스트 흡수(N9 '--' 관례) — 제로히트 침묵 exit 1",
	"brief":   "쿼리 텍스트 흡수(N9 승계)",
	"hot":     "미인식 인자 무시 — --session 가드는 표 구동 행",
	"status":  "미인식 인자 무시(F1 소유 — 문서화 분담)",
	"done":    "미인식 인자 무시 — --session 미지정 사용법 안내 exit 0",
	"write":   "위치 인자(kind) 흡수 — 무효 kind는 검증 3",
	"version": "인자 무시",
	"mcp":     "플래그 없음(STDIO 서버)",
	"compact": "훅 진입 — 인자 무시·비간섭",
	"nudge":   "훅 진입 — 인자 무시·언제나 exit 0",
}

// TestExitContractTable — 표 구동: 각 행의 무효 인자 exit 코드가 표값.
// maintain은 인자 파싱 앞에서 MaintStore 역량 검사를 하므로 fakeMaint를
// 쓴다(나머지는 fakeStore — 무효 인자 경로는 검증 즉시 반환이라 store
// 상태와 무관).
func TestExitContractTable(t *testing.T) {
	ctx := context.Background()
	for _, p := range exitContractProbes {
		h, ok := wire.Lookup(p.cmd)
		if !ok {
			t.Fatalf("%s: registry 미등록 커맨드", p.cmd)
		}
		var st wire.Store = &fakeStore{}
		if p.cmd == "maintain" {
			st = &fakeMaint{fakeStore: &fakeStore{}}
		}
		var b strings.Builder
		code := h(ctx, st, p.args, strings.NewReader(""), &b)
		if code != p.want {
			t.Errorf("%s %v: exit=%d, want %d (%s) — 출력 %q",
				p.cmd, p.args, code, p.want, p.note, b.String())
		}
		if !strings.Contains(b.String(), `"error"`) {
			t.Errorf("%s %v: 에러 JSON 아님 — %q", p.cmd, p.args, b.String())
		}
	}
}

// TestExitContractCoversUsageOrder — usageOrder 전 커맨드가 표(구동 행
// 또는 quietAbsorb)에 분류됐는지 강제. 신규 커맨드 추가 시 여기서
// 실패한다 — 무효 인자 exit 분류(3 통일 또는 흡수 문서화)를 강제한다.
func TestExitContractCoversUsageOrder(t *testing.T) {
	classified := map[string]bool{}
	for _, p := range exitContractProbes {
		classified[p.cmd] = true
	}
	for _, cmd := range wire.UsageOrder() {
		if !classified[cmd] && quietAbsorb[cmd] == "" {
			t.Errorf("%s: 무효 인자 exit 분류 누락 — exitContractProbes 또는 quietAbsorb에 추가", cmd)
		}
	}
	// 역방향 드리프트: 명단의 커맨드는 실제 등록 표면에 존재해야 한다.
	registered := map[string]bool{}
	for _, cmd := range wire.Registered() {
		registered[cmd] = true
	}
	for cmd := range quietAbsorb {
		if !registered[cmd] {
			t.Errorf("quietAbsorb의 %s: 미등록 커맨드", cmd)
		}
	}
}
