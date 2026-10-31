package docs

import (
	"os"
	"strings"
	"testing"
)

func TestTuningRecordsV0101Constants(t *testing.T) {
	b, err := os.ReadFile("TUNING.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"backupAgeWarn", "72", "suggestMinScore", "0.15"} {
		if !strings.Contains(s, want) {
			t.Errorf("TUNING.md에 %q 누락", want)
		}
	}
}
