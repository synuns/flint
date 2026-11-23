package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/engine"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/wire"
)

var (
	engineNativeDownloadFn  func(ctx context.Context) error
	engineNativeServiceFn   func(ctx context.Context) error
	engineNativeBootstrapFn func(ctx context.Context) error
)

type engineActionSummary struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
	Action string `json:"action"`
}

type setupEngineSummary struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

func engineErrLine(stdout io.Writer, err error) int {
	if errors.Is(err, engine.ErrNotWired) {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("%v — not wired yet; run flint setup --engine <managed-container|native|external>", err)))
		return 1
	}
	fmt.Fprintln(stdout, errLine(err))
	return 1
}

func runEngine(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf("%s", i18n.T("usage.engine"))))
		return 3
	}
	sub, rest := args[0], args[1:]
	if len(rest) > 0 {
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf("%s — unexpected argument %q", i18n.T("usage.engine"), wire.MaskToken(rest[0]))))
		return 3
	}
	switch sub {
	case "status":
		s, err := engine.Status()
		if err != nil {
			return engineErrLine(stdout, err)
		}
		b, err := json.Marshal(s)
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	case "start", "stop":
		var err error
		if sub == "start" {
			err = engine.Start()
		} else {
			err = engine.Stop()
		}
		if err != nil {
			return engineErrLine(stdout, err)
		}
		b, err := json.Marshal(engineActionSummary{Status: "ok", Mode: engine.CurrentMode(), Action: sub})
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintln(stdout, errLine(wire.UsageErrorf("%s — unknown subcommand %q", i18n.T("usage.engine"), wire.MaskToken(sub))))
	return 3
}

func runSetupEngineMode(ctx context.Context, stdout io.Writer, mode string) int {
	switch mode {
	case engine.ModeManagedContainer, engine.ModeNative, engine.ModeExternal:
	default:
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf("setup: --engine requires managed-container|native|external, got %q", mode)))
		return 3
	}
	if err := engine.SetMode(mode); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	if mode == engine.ModeNative {
		for _, fn := range []func(context.Context) error{engineNativeDownloadFn, engineNativeServiceFn, engineNativeBootstrapFn} {
			if fn == nil {
				continue
			}
			if err := fn(ctx); err != nil {
				fmt.Fprintln(stdout, errLine(err))
				return 1
			}
		}
	}
	b, err := json.Marshal(setupEngineSummary{Status: "ok", Mode: mode})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func init() {
	Register("engine", runEngine)
	engineNativeDownloadFn = func(ctx context.Context) error {
		_, err := engine.Download(ctx, "")
		return err
	}
	engineNativeServiceFn = func(ctx context.Context) error {
		return engine.Register()
	}
	engineNativeBootstrapFn = engine.BootstrapCredentials
}
