package knowledge

import (
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func TestTierCapEnforced(t *testing.T) {
	caps := store.DefaultTierCaps()
	if imp, ok := store.TierCapFor(caps, "import"); !ok || imp != 0.8 {
		t.Fatalf("import 상한 = %v(%v), want 0.8", imp, ok)
	}
	err := store.CheckTierCap(caps, "import", 0.9)
	if err == nil {
		t.Fatal("상향 시도가 차단되지 않음")
	}
	if code := store.MapExit(err); code != 3 {
		t.Fatalf("tier 위반 exit=%d, want 3", code)
	}
	var se *store.SchemaError
	if se, _ = err.(*store.SchemaError); se == nil {
		t.Fatalf("구조화 오류 아님: %v", err)
	}
	if se.Field != "confidence" || se.Expect == "" {
		t.Fatalf("구조화 표기 부족: %+v", se)
	}
	if !strings.Contains(se.Expect, "tier") {
		t.Fatalf("tier 위반 표기 부재: %v", err)
	}
	if err := store.CheckTierCap(caps, "import", 0.7); err != nil {
		t.Fatalf("하향 허용인데 차단됨: %v", err)
	}
	if err := store.CheckTierCap(caps, "import", 0.8); err != nil {
		t.Fatalf("상한 동일값 허용이어야 함: %v", err)
	}
	if err := store.CheckTierCap(caps, "manual", 0.9); err != nil {
		t.Fatalf("manual 상한 위 반영 쓰기 차단됨: %v", err)
	}
}

func TestTierMetaOverride(t *testing.T) {
	caps := store.ParseTierCaps([][2]any{{"tier_cap_hook", "0.75"}})
	if h, _ := store.TierCapFor(caps, "hook"); h != 0.75 {
		t.Fatalf("meta 오버라이드 미적용: hook=%v", h)
	}
	if m, _ := store.TierCapFor(caps, "manual"); m != 1.0 {
		t.Fatalf("미지정 채널 기본값 훼손: manual=%v", m)
	}
	if err := store.CheckTierCap(caps, "hook", 0.9); err == nil {
		t.Fatal("오버라이드 상한 초과 미차단")
	}
	if err := store.CheckTierCap(caps, "hook", 0.75); err != nil {
		t.Fatalf("오버라이드 상한 동일값 차단됨: %v", err)
	}
	fallback := store.ParseTierCaps([][2]any{
		{"tier_cap_import", "abc"},
		{"tier_cap_import", "1.5"},
	})
	if imp, _ := store.TierCapFor(fallback, "import"); imp != 0.8 {
		t.Fatalf("무효 오버라이드 기본값 폴백 실패: %v", imp)
	}
	ignored := store.ParseTierCaps([][2]any{{"conf_floor", "0.1"}, {"tier_cap_mcp", "0.1"}})
	if !reflect.DeepEqual(ignored, store.DefaultTierCaps()) {
		t.Fatalf("미등록 키 무시 실패: %v", ignored)
	}
}

func TestTierRegistryGuard(t *testing.T) {
	want := []string{"manual", "hook", "import"}
	if got := store.TierCapKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tier 등록표 키 드리프트: %v, want %v", got, want)
	}
	caps := store.DefaultTierCaps()
	if len(caps) != len(want) {
		t.Fatalf("등록표 항목 수: %d, want %d", len(caps), len(want))
	}
	for _, k := range want {
		v, ok := caps[k]
		if !ok {
			t.Fatalf("채널 %q 누락", k)
		}
		if v < 0 || v > 1 {
			t.Errorf("채널 %q 상한이 0..1 밖: %v", k, v)
		}
	}
	if !(caps["manual"] > caps["hook"] && caps["hook"] > caps["import"]) {
		t.Errorf("tier 위계 붕괴(manual>hook>import): %v", caps)
	}
	cases := []struct{ origin, sourcePath, want string }{
		{"hook", "", "hook"},
		{"import:notes/plan.md", "", "import"},
		{"", "notes/plan.md", "import"},
		{"manual", "", "manual"},
		{"agent", "", "manual"},
	}
	for _, c := range cases {
		if got := store.TierChannel(c.origin, c.sourcePath); got != c.want {
			t.Errorf("TierChannel(origin=%q, source_path=%q) = %q, want %q", c.origin, c.sourcePath, got, c.want)
		}
	}
}

func TestTierWriteGateEnforced(t *testing.T) {
	importOver := `{"topic":"tg-import","summary":"demo import body","confidence":0.9,` +
		`"source_path":"demo/notes/tg.md"}`
	out, code := run(t, &fakeStore{}, []string{"knowledge"}, importOver)
	if code != 3 {
		t.Fatalf("import 채널 상향 시도 exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "tier") || !strings.Contains(out, `"error"`) {
		t.Fatalf("tier 위반 구조화 출력 누락: %q", out)
	}
	if fakeInserts(out) != 0 {
		t.Fatal("상향 시도 레코드 저장됨")
	}

	hookOver := `{"topic":"tg-hook","summary":"demo hook body","confidence":0.95,"origin":"hook"}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, hookOver)
	if code != 3 {
		t.Fatalf("hook 채널 상향 시도 exit=%d, want 3; out=%s", code, out)
	}

	hookUnder := `{"topic":"tg-hook2","summary":"demo hook body","confidence":0.8,"origin":"hook"}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, hookUnder)
	if code != 0 || fakeInserts(out) != 1 {
		t.Fatalf("hook 하향 허용 exit=%d out=%s", code, out)
	}
}

func TestTierWriteGateMetaOverride(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "tier_cap_") {
			return [][]any{{"tier_cap_import", "0.5"}}, nil
		}
		return nil, nil
	}}
	over := `{"topic":"tg-meta","summary":"demo meta body","confidence":0.7,` +
		`"source_path":"demo/notes/tg.md"}`
	out, code := run(t, f, []string{"knowledge"}, over)
	if code != 3 {
		t.Fatalf("meta 오버라이드 상한(0.5) 초과 exit=%d, want 3; out=%s", code, out)
	}
	under := `{"topic":"tg-meta2","summary":"demo meta body","confidence":0.5,` +
		`"source_path":"demo/notes/tg.md"}`
	out, code = run(t, f, []string{"knowledge"}, under)
	if code != 0 || fakeInserts(out) != 1 {
		t.Fatalf("meta 오버라이드 상한 이내 exit=%d out=%s", code, out)
	}
	if !strings.Contains(strings.Join(f.queries, ";"), "tier_cap_") {
		t.Fatal("meta tier_cap 조회가 store 경로를 경유하지 않음")
	}
}
