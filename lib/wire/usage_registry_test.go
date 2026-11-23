package wire

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

var seedCommands = []string{
	"write", "read", "hot", "brief", "status", "profile", "backup", "restore",
	"sync",
	"approve", "pending", "maintain", "doctor", "report", "export",
	"distill",
	"done", "compact", "nudge", "seed", "engine", "setup", "teardown", "version", "mcp",
	"index",
	"audit", "diff", "update", "tuning", "import",
	"session-start", "session-end",
}

func seedRegistry(t *testing.T) {
	t.Helper()
	stub := func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 }
	for _, name := range seedCommands {
		n := name
		Register(n, stub)
		t.Cleanup(func() { Unregister(n) })
	}
}

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

func TestHelpSummaryDocumentsPAMExport(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"export", "pam", "sha256"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

func TestHelpSummaryCoversUsageOrder(t *testing.T) {
	s := HelpSummary()
	for _, name := range usageOrder {
		if !strings.Contains(s, name) {
			t.Errorf("HelpSummary에 usageOrder 항목 %q 누락: %s", name, s)
		}
	}
}

func TestHelpSummaryDocumentsReaffirm(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"reaffirm", "recheck"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

func TestHelpSummaryEnglish(t *testing.T) {
	for _, r := range HelpSummary() {
		if r >= '가' && r <= '힣' {
			t.Fatalf("HelpSummary에 한국어 잔존: %s", HelpSummary())
		}
	}
}

func TestHelpSummaryDocumentsAutoMode(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"auto", "off|shadow"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

func TestUsageLineAssembledFromOrder(t *testing.T) {
	want := `{"usage":"flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
	if UsageLine() != want {
		t.Fatalf("usageLine이 usageOrder 조립과 불일치:\n got=%s\nwant=%s", UsageLine(), want)
	}
}

func TestHelpSummaryDocumentsMergeAudit(t *testing.T) {
	s := HelpSummary()
	if !strings.Contains(s, "merge-audit") {
		t.Errorf("helpSummary에 merge-audit 누락: %s", s)
	}
}

func TestHelpSummaryDocumentsRecallAudit(t *testing.T) {
	s := HelpSummary()
	if !strings.Contains(s, "recall-audit") {
		t.Errorf("helpSummary에 recall-audit 누락: %s", s)
	}
}

func TestHelpSummaryDocumentsGC(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"--gc", "disconnect"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}
