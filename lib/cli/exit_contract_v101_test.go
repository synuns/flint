package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

type exitProbe struct {
	cmd  string
	args []string
	want int
	note string
}

var exitContractProbes = []exitProbe{

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

	{"maintain", []string{"--merge-audit", "--bogus"}, 3, "v1.24.0 T3 unknown flag"},
	{"maintain", []string{"--merge-audit", "--limit", "abc"}, 3, "v1.24.0 T3 무효 숫자"},
	{"import", []string{"--verbose"}, 3, "v0.36.1 관례(기존)"},
	{"doctor", []string{"--bogus"}, 3, "전역 계약(기존)"},
	{"tuning", []string{"--bogus"}, 3, "서브커맨드 아님 → usage 3"},
	{"diff", []string{"--bogus"}, 3, "위치 인자 흡수 → 인자 수 usage 3"},
	{"export", []string{"--bogus"}, 3, "v1.3.0 T7 unknown flag"},
	{"index", []string{"--bogus"}, 3, "v1.21.0 T3 unknown flag"},
	{"distill", []string{"--bogus"}, 3, "v1.22.0 T6 unknown flag"},
	{"seed", []string{"--bogus"}, 3, "v1.26.0 T2 unknown flag"},
	{"seed", []string{}, 3, "v1.26.0 T2 --demo 필수 bare usage"},

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

	{"read", []string{"--session", "--json"}, 3, "세션 가드(기존)"},
	{"hot", []string{"--session", "--full"}, 3, "F3 세션 가드 승격"},
	{"hot", []string{"--session"}, 3, "F3 세션 가드(빈 값)"},
	{"done", []string{"--session", "--json"}, 3, "F3 세션 가드 승격"},
	{"done", []string{"--session"}, 3, "F3 세션 가드(빈 값)"},

	{"read", []string{"--jsonn"}, 3, "T1 unknown flag('-' 접두 흡수 폐지)"},
	{"done", []string{}, 3, "T1 bare usage(기존 0)"},
	{"maintain", []string{}, 3, "T1 bare usage(기존 1)"},

	{"sync", []string{}, 3, "v1.7.0 T4 bare usage"},
	{"sync", []string{"push"}, 3, "v1.7.0 T4 unknown sub(T5 전 미지원)"},
	{"sync", []string{"remote", "add"}, 3, "v1.7.0 T4 인자 수 부족"},
	{"sync", []string{"remote", "add", "demo", "noscheme.example/x"}, 3, "v1.7.0 T4 스킴 없는 url"},

	{"sync", []string{"remote", "add", "demo", "postgres://u:p@db.example.invalid/db"}, 3, "v1.7.2 T3 postgres 스킴 거부(S5)"},
	{"sync", []string{"reset"}, 3, "v1.7.2 T3 reset 대상 부재(S2)"},

	{"engine", []string{}, 3, "v1.31.0 T5 bare usage"},
	{"engine", []string{"bogus"}, 3, "v1.31.0 T5 무효 서브커맨드"},
}

var quietAbsorb = map[string]string{
	"read":    "positional 쿼리 흡수(N9 '--' 관례) — '-' 접두 미인식 플래그는 v1.5.3 T1 exit 3, 제로히트 침묵 exit 1",
	"brief":   "쿼리 텍스트 흡수(N9 승계)",
	"hot":     "미인식 인자 무시 — --session 가드는 표 구동 행",
	"status":  "미인식 인자 무시(F1 소유 — 문서화 분담)",
	"write":   "위치 인자(kind) 흡수 — 무효 kind는 검증 3",
	"version": "인자 무시",
	"mcp":     "플래그 없음(STDIO 서버)",
	"compact": "훅 진입 — 인자 무시·비간섭",
	"nudge":   "훅 진입 — 인자 무시·언제나 exit 0",
}

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
