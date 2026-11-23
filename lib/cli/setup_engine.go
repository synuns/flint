package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var engineHandshakeFn func(ctx context.Context, dsn string) error

func MaskEngineURL(dsn string) string {
	i := strings.Index(dsn, "://")
	if i < 0 {
		return dsn
	}
	rest := dsn[i+3:]
	at := strings.Index(rest, "@")
	if at < 0 {
		return dsn
	}
	userinfo := rest[:at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		return dsn
	}
	return dsn[:i+3] + userinfo[:colon+1] + "***" + rest[at:]
}

func isLoopbackHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func engineURLArg(args []string) (string, bool) {
	for i, a := range args {
		if a == "--engine-url" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(a, "--engine-url=") {
			return strings.TrimPrefix(a, "--engine-url="), true
		}
	}
	return "", false
}

type engineURLSummary struct {
	Status    string `json:"status"`
	EngineURL string `json:"engine_url"`
	Mode      string `json:"mode"`
}

func runSetupEngineURL(ctx context.Context, stdout io.Writer, dsn string) int {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		fmt.Fprintln(stdout, errLine(errors.New("setup: --engine-url requires a postgresql:// DSN on the loopback (localhost, 127.0.0.1, [::1]) — unix sockets, remote hosts and other schemes are rejected; see docs/QUICKSTART.md")))
		return 3
	}
	if !isLoopbackHost(u.Hostname()) {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("setup: --engine-url is loopback-only (localhost, 127.0.0.1, [::1]) — got host %q; see docs/QUICKSTART.md", u.Hostname())))
		return 3
	}
	if err := writeEngineConfig(dsn); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	if engineHandshakeFn != nil {
		if err := engineHandshakeFn(ctx, dsn); err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
	}
	b, err := json.Marshal(engineURLSummary{Status: "ok", EngineURL: MaskEngineURL(dsn), Mode: "external"})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func writeEngineConfig(dsn string) error {
	path := ConfigPath()
	if path == "" {
		return errors.New("setup: config directory unavailable — set FLINT_CONFIG_DIR")
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("setup: parse %s: %w", path, err)
		}
	}
	cfg["engine_url"] = dsn
	engine, _ := cfg["engine"].(map[string]any)
	if engine == nil {
		engine = map[string]any{}
	}
	engine["mode"] = "external"
	cfg["engine"] = engine
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
