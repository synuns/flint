package wire

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

const DefaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

var ConfigDir func() string

func configDirOrEmpty() string {
	if ConfigDir == nil {
		return ""
	}
	return ConfigDir()
}

func ConfigPath() string {
	d := configDirOrEmpty()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

type fileConfig struct {
	dsn       string
	engineURL string
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
	return fc
}

// LoadConfigDSN returns the DSN recorded in config.json — engine_url first
// (external mode), then the dsn key.
func LoadConfigDSN() string {
	c := loadFileConfig()
	if c.engineURL != "" {
		return c.engineURL
	}
	return c.dsn
}

// WithDSNPort substitutes the port of a postgresql:// DSN, leaving the rest
// (credentials, path, query) untouched.
func WithDSNPort(dsn string, port int) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || u.Hostname() == "" || port < 1 || port > 65535 {
		return dsn
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	return u.String()
}

// ResolveDSN resolves the production DSN: FLINT_DSN env, then config.json
// (engine_url for external mode, else dsn — with the config port key
// substituted into the port), then the default. FLINT_PORT is a compose
// variable and never participates here.
func ResolveDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	c := loadFileConfig()
	if c.engineURL != "" {
		return c.engineURL
	}
	d := c.dsn
	if d == "" {
		d = DefaultDSN
	}
	if c.port != 0 {
		d = WithDSNPort(d, c.port)
	}
	return d
}
