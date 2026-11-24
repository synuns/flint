package ops

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/synuns/flint/lib/wire"
)

var DSNClassDSNFn = func() string {
	return wire.ResolveDSN()
}

var DSNClassContextFn = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 4*time.Second)
}

var DSNClassLookupFn = func(ctx context.Context, host string) error {
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errors.New("no addresses resolved")
	}
	return nil
}

var DSNClassDialFn = func(ctx context.Context, host, port string) error {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	return conn.Close()
}

const dsnClassDefaultPort = 5434

func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

func CheckDSNClass() DoctorCheck {
	c := DoctorCheck{Name: "dsn-class"}
	raw := strings.TrimSpace(DSNClassDSNFn())
	u, perr := url.Parse(raw)
	if perr != nil || u.Scheme == "" || u.Host == "" {
		c.Status = "fail"
		c.Detail = "config dsn defect: malformed — engine endpoint URL cannot be parsed"
		c.Fix = "fix: correct config engine_url/dsn (flint config) — expected postgresql://host:port/db"
		return c
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = strconv.Itoa(dsnClassDefaultPort)
	}
	dctx, cancel := DSNClassContextFn()
	defer cancel()
	if lerr := DSNClassLookupFn(dctx, host); lerr != nil {
		c.Status = "fail"
		c.Detail = "config dsn defect: host not resolvable (" + host + ") — endpoint can never be reached"
		c.Fix = "fix: correct the host in config engine_url/dsn (flint config)"
		return c
	}
	if derr := DSNClassDialFn(dctx, host, port); derr != nil {
		c.Status = "fail"
		if isConnRefused(derr) {
			c.Detail = fmt.Sprintf("engine down: connection refused at %s:%s", host, port)
			c.Fix = "fix: flint setup (starts the embedded engine) or flint engine start"
			return c
		}
		c.Detail = fmt.Sprintf("engine unreachable at %s:%s (%s)", host, port, dialErrKind(derr))
		c.Fix = "fix: flint status — check engine connectivity and config engine_url"
		return c
	}
	c.Status = "pass"
	c.Detail = fmt.Sprintf("engine endpoint reachable at %s:%s", host, port)
	return c
}

func dialErrKind(err error) string {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no route"):
		return "no route to host"
	case strings.Contains(msg, "permission"):
		return "permission denied"
	default:
		return "network error"
	}
}
