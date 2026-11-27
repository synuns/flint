package wire

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/i18n"
)

func TestRegisteredEnumerates(t *testing.T) {
	before := map[string]bool{}
	for _, n := range Registered() {
		before[n] = true
	}
	Register("probe-accessor", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 })
	t.Cleanup(func() { Unregister("probe-accessor") })
	got := Registered()
	if sort.SearchStrings(got, "probe-accessor") >= len(got) || got[sort.SearchStrings(got, "probe-accessor")] != "probe-accessor" {
		t.Fatalf("Registered에 등록 항목 없음: %v", got)
	}

	if !sort.StringsAreSorted(got) {
		t.Fatalf("Registered 미정렬: %v", got)
	}
	Unregister("probe-accessor")
	for _, n := range Registered() {
		if n == "probe-accessor" {
			t.Fatal("Unregister 후에도 Registered에 잔존")
		}
	}
}

func TestMaintainUsageRegistersApplyVol(t *testing.T) {
	line := i18n.T("usage.maintain")
	if !strings.Contains(line, "--age-audit [--limit M] [--apply-vol [--yes]]") {
		t.Errorf("사용법 라인에 --apply-vol 등록 누락: %s", line)
	}
}

func TestUsageOrderCopyNotAlias(t *testing.T) {
	got := UsageOrder()
	if len(got) == 0 {
		t.Fatal("UsageOrder 빈 슬라이스")
	}
	got[0] = "mutated"
	if UsageOrder()[0] == "mutated" {
		t.Fatal("UsageOrder가 내부 슬라이스 별칭 — 사본이어야 함")
	}
}

func TestHiddenFromUsagePublic(t *testing.T) {
	for _, name := range []string{"session-start", "session-end"} {
		if !HiddenFromUsage(name) {
			t.Errorf("HiddenFromUsage(%q)=false, want true", name)
		}
	}
	for _, name := range []string{"write", "compact", "nudge"} {
		if HiddenFromUsage(name) {
			t.Errorf("HiddenFromUsage(%q)=true, want false", name)
		}
	}
}
