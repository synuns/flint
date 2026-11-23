package cli

import (
	"fmt"
	"io"
	"os"
)

const (
	ansiGreen = "\033[32m✓\033[0m"
	ansiRed   = "\033[31m✗\033[0m"
	ansiDim   = "\033[2m%s\033[0m"
	ansiBold  = "\033[1m%s\033[0m"
)

type setupUX struct {
	w        io.Writer
	isTTY    bool
	lastStep string
}

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

func (u *setupUX) Step(name string) {
	u.lastStep = name
	if u.isTTY {
		fmt.Fprintf(u.w, "  ◯ %s…", name)
	}
}

func (u *setupUX) Success(detail string) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiGreen, u.lastStep, fmt.Sprintf(ansiDim, detail))
		return
	}
	fmt.Fprintf(u.w, "%s: ok (%s)\n", u.lastStep, detail)
}

func (u *setupUX) Fail(err error) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiRed, u.lastStep, err)
		return
	}
	fmt.Fprintf(u.w, "%s: failed (%v)\n", u.lastStep, err)
}

func (u *setupUX) Remedy(kind, remedy string) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s: %s\n", ansiRed, u.lastStep, kind, remedy)
		return
	}
	fmt.Fprintf(u.w, "%s: %s — %s\n", u.lastStep, kind, remedy)
}

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
