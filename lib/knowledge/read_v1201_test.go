// read_v1201_test.go — v1.20.1 (r86) read regressions: NaN·Inf
// --min-confidence values are invalid (exit 3) — ParseFloat accepts
// them and every NaN comparison is false, so a NaN floor previously
// passed the 0..1 range gate unfiltered.
package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestRunReadMinConfidenceNaNInfRefusedV1201(t *testing.T) {
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf"} {
		st := &fakeStore{}
		var out strings.Builder
		code := RunRead(context.Background(), st,
			[]string{"query", "--min-confidence", v}, strings.NewReader(""), &out)
		if code != 3 {
			t.Errorf("--min-confidence %s exit=%d want 3 (out=%s)", v, code, out.String())
		}
	}
}
