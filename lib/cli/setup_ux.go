// setupUX — setup progress display. Uses only stdlib fmt and ANSI escapes
// (ADR-0003 forbids external dependencies). On a TTY it renders step indicators (◯... -> ✓/✗) with
// color; on non-TTY it renders plain single-line results without color codes.
package cli

import (
	"fmt"
	"io"
	"os"
)

// ANSI color constants — ✓ green, ✗ red, details dim, headings bold.
const (
	ansiGreen = "\033[32m✓\033[0m"
	ansiRed   = "\033[31m✗\033[0m"
	ansiDim   = "\033[2m%s\033[0m"
	ansiBold  = "\033[1m%s\033[0m"
)

// setupUX — progress indicator. lastStep is the name of the most recent step set via Step;
// Success/Fail overwrites that line with \r to complete it.
type setupUX struct {
	w        io.Writer
	isTTY    bool
	lastStep string
}

// newSetupUX — writes to w. If forceColor is true, treat as TTY (test injection);
// otherwise detect via *os.File Stat (mode & ModeCharDevice). Buffers, pipes,
// and writers that are not *os.File are non-TTY.
func newSetupUX(w io.Writer, forceColor bool) *setupUX {
	isTTY := forceColor
	if !isTTY {
		if f, ok := w.(*os.File); ok {
			if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
				isTTY = true
			}
		}
	}
	return &setupUX{w: w, isTTY: isTTY}
}

// Step — shows an in-progress step ("  ◯ name…"). On a TTY it prints without a newline so the
// completion mark overwrites the same line; on non-TTY it prints nothing, leaving only the one result line.
func (u *setupUX) Step(name string) {
	u.lastStep = name
	if u.isTTY {
		fmt.Fprintf(u.w, "  ◯ %s…", name)
	}
}

// Success — step succeeded. TTY: "\r  ✓ name — detail" (green + dim detail),
// non-TTY: "name: ok (detail)".
func (u *setupUX) Success(detail string) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiGreen, u.lastStep, fmt.Sprintf(ansiDim, detail))
		return
	}
	fmt.Fprintf(u.w, "%s: ok (%s)\n", u.lastStep, detail)
}

// Fail — step failed. TTY: "\r  ✗ name — err" (red), non-TTY: "name: failed (err)".
func (u *setupUX) Fail(err error) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiRed, u.lastStep, err)
		return
	}
	fmt.Fprintf(u.w, "%s: failed (%v)\n", u.lastStep, err)
}

// Summary — next-step guidance after completion (③seed ④plugins — guidance only, not execution).
func (u *setupUX) Summary(next []string) {
	if len(next) == 0 {
		return
	}
	if u.isTTY {
		fmt.Fprintf(u.w, "\n"+ansiBold+"\n", "Next steps")
	} else {
		fmt.Fprintln(u.w, "Next steps:")
	}
	for i, n := range next {
		fmt.Fprintf(u.w, "  %d. %s\n", i+1, n)
	}
}
