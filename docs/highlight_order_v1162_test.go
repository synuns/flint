package docs

import (
	"os"
	"regexp"
	"testing"
)

// TestReadmeHighlightLatestFirst — the newest version's highlight line must
// be the FIRST "> v" line in both READMEs (latest-first convention since
// v1.15.0, R61 ruling). Guard against mid-list insertion regression.
func TestReadmeHighlightLatestFirst(t *testing.T) {
	raw, err := os.ReadFile("../VERSION")
	if err != nil {
		t.Skipf("VERSION unreadable: %v", err)
	}
	ver := string(regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`).Find(raw))
	for _, path := range []string{"../README.md", "../README.ko.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		hl := regexp.MustCompile(`(?m)^> v[0-9]+\.[0-9]+\.[0-9]+ `)
		lines := hl.FindAllString(string(raw), -1)
		if len(lines) == 0 {
			t.Fatalf("%s: no highlight lines", path)
		}
		want := "> v" + ver + " "
		if lines[0] != want {
			t.Errorf("%s: first highlight = %q, want %q (latest-first)", path, lines[0], want)
		}
	}
}

// TestReadmeHighlightParityENKO — the version column sequence must be
// identical between README.md and README.ko.md (line-for-line parity).
func TestReadmeHighlightParityENKO(t *testing.T) {
	hl := regexp.MustCompile(`(?m)^> v([0-9]+\.[0-9]+\.[0-9]+) `)
	ext := func(path string) []string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		m := hl.FindAllStringSubmatch(string(raw), -1)
		out := make([]string, len(m))
		for i, x := range m {
			out[i] = x[1]
		}
		return out
	}
	en := ext("../README.md")
	ko := ext("../README.ko.md")
	if len(en) != len(ko) {
		t.Fatalf("highlight count EN=%d KO=%d — parity broken", len(en), len(ko))
	}
	for i := range en {
		if en[i] != ko[i] {
			t.Fatalf("highlight[%d] EN=v%s KO=v%s — parity broken", i, en[i], ko[i])
		}
	}
}
