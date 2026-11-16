// usage_type_v154_test.go — v1.5.4 T1 회귀: 7커맨드(maintain·doctor·
// report·audit·backup·import·update)의 무효 인자(unknown flag·값 누락·
// 무효 값·무효 검사명)는 error:"usage" + 메시지에 허용 플래그 안내
// (v1.5.3 P2 read·approve 관례 확장). exit 코드 불변(무효 인자 3).
// 진짜 내부 결함(네트워크 실패 등)은 internal 유지 — 여선의 대상 아님.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// usageTypeProbes — 무효 인자 경로 구동 표: (커맨드, 인자, 기대 exit,
// 메시지에 포함되어야 할 usage 안내 접두).
type usageTypeProbe struct {
	cmd      string
	args     []string
	want     int
	usageSub string
}

var usageTypeProbes = []usageTypeProbe{
	// maintain — unknown flag·값 누락·bare usage
	{"maintain", []string{"--check", "--bogus"}, 3, "usage: flint maintain"},
	{"maintain", []string{"--limit"}, 3, "usage: flint maintain"},
	{"maintain", []string{}, 3, "usage: flint maintain"},
	// doctor — unknown flag·--check 값 누락·무효 검사명
	{"doctor", []string{"--bogus"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check", "nope"}, 3, "usage: flint doctor"},
	// report — unknown flag·--days 값 누락·무효 숫자
	{"report", []string{"--bogus"}, 3, "usage: flint report"},
	{"report", []string{"--days"}, 3, "usage: flint report"},
	{"report", []string{"--days", "abc"}, 3, "usage: flint report"},
	// audit — unknown flag·--limit 값 누락·무효 숫자
	{"audit", []string{"--bogus"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit", "abc"}, 3, "usage: flint audit"},
	// backup — unknown flag·--out 값 누락·--encrypt 단독
	{"backup", []string{"--wat"}, 3, "usage: flint backup"},
	{"backup", []string{"--out"}, 3, "usage: flint backup"},
	{"backup", []string{"--encrypt"}, 3, "usage: flint backup"},
	// restore(backup.go 소유 — 동일 계약) — unknown flag·--file 값 누락
	{"restore", []string{"--wat"}, 3, "usage: flint restore"},
	{"restore", []string{"--file"}, 3, "usage: flint restore"},
	// import — 규칙 변환 경로 unknown flag·마이그레이션 경로 unknown flag
	{"import", []string{"--verbose"}, 3, "usage: flint import"},
	{"import", []string{"--format", "jsonl", "--wat"}, 3, "usage: flint import"},
	// update — unknown flag(네트워크 접근 전 검증)
	{"update", []string{"--bogus"}, 3, "usage: flint update"},
	// v1.5.5 T2 A2 — 잔여 5사이트 unknown flag도 동일 계약:
	// pending(기본 경로 + --tui 조합)·export·profile·setup/teardown·
	// report --tui×뷰 플래그 조합.
	{"pending", []string{"--bogus"}, 3, "usage: flint pending"},
	{"pending", []string{"--tui", "--json"}, 3, "usage: flint pending"},
	{"export", []string{"--bogus"}, 3, "usage: flint export"},
	{"profile", []string{"--bogus"}, 3, "usage: flint profile"},
	{"setup", []string{"--bogus"}, 3, "usage: flint setup"},
	{"teardown", []string{"--bogus"}, 3, "usage: flint teardown"},
	{"report", []string{"--tui", "--html"}, 3, "usage: flint report"},
	// v1.5.5 T2 A5 — hot·brief·compact·nudge: unknown `-` 접두 인자 exit 3
	// (현행 조용한 무시·exit 0 폐지 — 조용한 성공 금지).
	{"hot", []string{"--bogus"}, 3, "usage: flint hot"},
	{"brief", []string{"--bogus"}, 3, "usage: flint brief"},
	{"compact", []string{"--bogus"}, 3, "usage: flint compact"},
	{"nudge", []string{"--bogus"}, 3, "usage: flint nudge"},
	// v1.5.5 T2 A5 — write --help/-h: stdin 대기 전 usage 안내 exit 0
	// (에러가 아니므로 error:"usage" 단언은 want!=0 행에만 적용).
	{"write", []string{"--help"}, 0, "usage: flint write"},
	{"write", []string{"-h"}, 0, "usage: flint write"},
}

// TestUsageTypeProbes — 각 행: exit 3 불변 + error:"usage" + 허용 플래그
// 안내 포함. 수정 전 기대치(현행 error:"internal")로 red.
func TestUsageTypeProbes(t *testing.T) {
	ctx := context.Background()
	for _, p := range usageTypeProbes {
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
			t.Errorf("%s %v: exit=%d, want %d — 출력 %q", p.cmd, p.args, code, p.want, b.String())
		}
		// write --help(exit 0) 등 안내 행은 에러 JSON이 아니므로 제외.
		if p.want != 0 && !strings.Contains(b.String(), `"error":"usage"`) {
			t.Errorf("%s %v: error:usage 아님 — %q", p.cmd, p.args, b.String())
		}
		if !strings.Contains(b.String(), p.usageSub) {
			t.Errorf("%s %v: 허용 플래그 안내(%q) 미포함 — %q", p.cmd, p.args, p.usageSub, b.String())
		}
	}
}
