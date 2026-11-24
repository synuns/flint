package cli

import (
	"fmt"
	"io"

	"github.com/synuns/flint/lib/wire"
)

func printErr(w io.Writer, err error) {
	fmt.Fprintln(w, wire.ErrLine(err))
}
