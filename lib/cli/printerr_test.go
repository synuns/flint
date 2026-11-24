package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func TestPrintErrByteIdentical(t *testing.T) {
	cases := []error{
		errors.New("boom"),
		wire.UsageErrorf("flag %s unknown", "--x"),
		errors.New("setup: --engine requires managed-container|native|external"),
	}
	for _, err := range cases {
		var got, want bytes.Buffer
		printErr(&got, err)
		fmt.Fprintln(&want, wire.ErrLine(err))
		if got.String() != want.String() {
			t.Fatalf("printErr 출력 불일치 err=%v: got %q want %q", err, got.String(), want.String())
		}
	}
}
