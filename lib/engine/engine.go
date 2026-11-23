package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ModeManagedContainer = "managed-container"
	ModeNative           = "native"
	ModeExternal         = "external"
	ModeUnknown          = "unknown"

	managedContainerPort = 5434
	testEnginePort       = 5499
	defaultDSN           = "postgresql://flint:flint-dev@localhost:5434/flint"
)

var (
	ErrNotWired    = errors.New("engine: service backend not wired")
	ErrUnknownMode = errors.New("engine: unknown mode — run flint setup --engine <managed-container|native|external>")
)

var configDir = defaultConfigDir

func defaultConfigDir() string {
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "flint")
}

func ConfigPath() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

type fileConfig struct {
	dsn       string
	engineURL string
	mode      string
	port      int
}

func loadFileConfig() fileConfig {
	p := ConfigPath()
	if p == "" {
		return fileConfig{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fileConfig{}
	}
	var c struct {
		DSN       any `json:"dsn"`
		EngineURL any `json:"engine_url"`
		Port      any `json:"port"`
		Engine    *struct {
			Mode any `json:"mode"`
		} `json:"engine"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return fileConfig{}
	}
	var fc fileConfig
	if s, ok := c.DSN.(string); ok {
		fc.dsn = s
	}
	if s, ok := c.EngineURL.(string); ok {
		fc.engineURL = s
	}
	switch v := c.Port.(type) {
	case float64:
		fc.port = int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			n = 0
		}
		fc.port = n
	}
	if fc.port < 1 || fc.port > 65535 {
		fc.port = 0
	}
	if c.Engine != nil {
		if s, ok := c.Engine.Mode.(string); ok {
			fc.mode = s
		}
	}
	return fc
}

func isLoopbackHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func dsnHostPort(dsn string) (string, int) {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "", 0
	}
	port := 0
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return u.Hostname(), 0
		}
		port = n
	}
	return u.Hostname(), port
}

func withDSNPort(dsn string, port int) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || u.Hostname() == "" || port < 1 || port > 65535 {
		return dsn
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	return u.String()
}

func CurrentMode() string {
	c := loadFileConfig()
	switch c.mode {
	case ModeManagedContainer, ModeNative, ModeExternal:
		return c.mode
	}
	return inferMode(c)
}

func inferMode(c fileConfig) string {
	if c.engineURL != "" {
		return ModeExternal
	}
	if c.dsn == "" {
		return ModeUnknown
	}
	host, port := dsnHostPort(c.dsn)
	if c.port != 0 {
		port = c.port
	}
	if !isLoopbackHost(host) {
		return ModeUnknown
	}
	switch port {
	case managedContainerPort, testEnginePort:
		return ModeManagedContainer
	}
	return ModeUnknown
}

func SetMode(mode string) error {
	switch mode {
	case ModeManagedContainer, ModeNative, ModeExternal:
	default:
		return fmt.Errorf("engine: invalid mode %q — want managed-container|native|external", mode)
	}
	path := ConfigPath()
	if path == "" {
		return errors.New("engine: config directory unavailable — set FLINT_CONFIG_DIR")
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("engine: parse %s: %w", path, err)
		}
	}
	engineMap, _ := cfg["engine"].(map[string]any)
	if engineMap == nil {
		engineMap = map[string]any{}
	}
	engineMap["mode"] = mode
	cfg["engine"] = engineMap
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

func resolveDSN(c fileConfig) string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	if c.engineURL != "" {
		return c.engineURL
	}
	d := c.dsn
	if d == "" {
		return defaultDSN
	}
	if c.port != 0 {
		d = withDSNPort(d, c.port)
	}
	return d
}

func ModeDSNMismatch() bool {
	c := loadFileConfig()
	switch CurrentMode() {
	case ModeManagedContainer, ModeNative:
		host, _ := dsnHostPort(resolveDSN(c))
		return !isLoopbackHost(host)
	case ModeExternal:
		if c.engineURL != "" || os.Getenv("FLINT_DSN") != "" {
			return false
		}
		host, port := dsnHostPort(resolveDSN(c))
		return isLoopbackHost(host) && port == managedContainerPort
	}
	return false
}

type ServiceStatus struct {
	Mode       string `json:"mode"`
	Registered bool   `json:"registered"`
	Running    bool   `json:"running"`
	Version    string `json:"version"`
	Detail     string `json:"detail"`
}

type ContainerService struct {
	Status  func() (ServiceStatus, error)
	Start   func() error
	Stop    func() error
	Version func() (string, error)
}

type NativeService struct {
	Status  func() (ServiceStatus, error)
	Start   func() error
	Stop    func() error
	Version func() (string, error)
}

var (
	containerService ContainerService
	nativeService    NativeService
)

func SetContainerService(s ContainerService) { containerService = s }
func SetNativeService(s NativeService)       { nativeService = s }

func Status() (ServiceStatus, error) {
	mode := CurrentMode()
	switch mode {
	case ModeManagedContainer:
		if containerService.Status == nil {
			return ServiceStatus{Mode: mode}, ErrNotWired
		}
		return containerService.Status()
	case ModeNative:
		if nativeService.Status == nil {
			return ServiceStatus{Mode: mode}, ErrNotWired
		}
		return nativeService.Status()
	case ModeExternal:
		return ServiceStatus{Mode: mode, Detail: externalDetail()}, nil
	}
	return ServiceStatus{Mode: mode}, ErrUnknownMode
}

func Start() error {
	mode := CurrentMode()
	switch mode {
	case ModeManagedContainer:
		if containerService.Start == nil {
			return ErrNotWired
		}
		return containerService.Start()
	case ModeNative:
		if nativeService.Start == nil {
			return ErrNotWired
		}
		return nativeService.Start()
	case ModeExternal:
		return errExternalUnavailable("start")
	}
	return ErrUnknownMode
}

func Stop() error {
	mode := CurrentMode()
	switch mode {
	case ModeManagedContainer:
		if containerService.Stop == nil {
			return ErrNotWired
		}
		return containerService.Stop()
	case ModeNative:
		if nativeService.Stop == nil {
			return ErrNotWired
		}
		return nativeService.Stop()
	case ModeExternal:
		return errExternalUnavailable("stop")
	}
	return ErrUnknownMode
}

func Version() (string, error) {
	mode := CurrentMode()
	switch mode {
	case ModeManagedContainer:
		if containerService.Version == nil {
			return "", ErrNotWired
		}
		return containerService.Version()
	case ModeNative:
		if nativeService.Version == nil {
			return "", ErrNotWired
		}
		return nativeService.Version()
	case ModeExternal:
		return "", errExternalUnavailable("version")
	}
	return "", ErrUnknownMode
}

func externalDetail() string {
	return "external engine — flint does not manage it; connect via the configured DSN"
}

func errExternalUnavailable(cmd string) error {
	return fmt.Errorf("engine: %s is unavailable in external mode — flint does not manage an external engine; connect via the configured DSN", cmd)
}
