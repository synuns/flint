package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	classifyGOOS       = runtime.GOOS
	classifyLookPathFn = exec.LookPath
)

const (
	defaultPingRetryCount    = 30
	defaultPingRetryInterval = 200 * time.Millisecond
	defaultEnginePort        = 5434
)

func ClassifyEngineError(err error) (kind string, remedy string) {
	if err == nil {
		return "unclassified", ""
	}
	msg := err.Error()
	l := strings.ToLower(msg)
	switch {
	case errors.Is(err, exec.ErrNotFound) || strings.Contains(msg, "exec: docker: executable file not found"):
		return "binary_missing", remedyBinaryMissing()
	case strings.Contains(msg, "Cannot connect to the Docker daemon"):
		return "daemon_down", remedyDaemonDown()
	case strings.Contains(msg, "permission denied"):
		return "permission_denied", remedyPermission()
	case strings.Contains(msg, "is not a docker command") && dockerComposeV1Present():
		return "compose_v1", "docker compose plugin missing — docker-compose v1 detected; install the compose plugin (https://docs.docker.com/compose/install/), then re-run flint setup"
	case strings.Contains(l, "toomanyrequests") || strings.Contains(l, "rate limit"):
		return "rate_limited", "Docker Hub pull rate limit reached — wait for the quota window to reset or authenticate to Docker Hub (docker login), then re-run flint setup"
	case strings.Contains(l, "tls") || strings.Contains(l, "x509") || strings.Contains(l, "certificate"):
		return "tls_error", "TLS certificate failure while pulling or connecting — check registry proxy and CA trust settings, then re-run flint setup"
	case strings.Contains(l, "unhealthy") || strings.Contains(l, "health check"):
		return "health_check_timeout", "engine container failed its health check — inspect it with docker logs, then re-run flint setup"
	}
	if remedy, ok := remedyLightweightRuntime(msg); ok {
		return "lightweight_runtime", remedy
	}
	return "unclassified", msg
}

func dockerComposeV1Present() bool {
	_, err := classifyLookPathFn("docker-compose")
	return err == nil
}

func remedyDaemonDown() string {
	switch classifyGOOS {
	case "darwin":
		return "start Docker Desktop (open -a Docker) — Colima: colima start · OrbStack: open -a OrbStack — then re-run flint setup"
	case "linux":
		return "start the docker service (sudo systemctl start docker) — Podman: podman machine start — then re-run flint setup"
	default:
		return "start the Docker daemon, then re-run flint setup"
	}
}

func remedyPermission() string {
	if classifyGOOS == "linux" {
		return "docker socket permission denied — add your user to the docker group (sudo usermod -aG docker $USER) and re-login, then re-run flint setup"
	}
	return "docker permission denied — check Docker Desktop settings and socket access, then re-run flint setup"
}

func remedyBinaryMissing() string {
	if classifyGOOS == "darwin" {
		return "docker is not installed — install it (brew install --cask docker or https://docs.docker.com/get-docker/), then re-run flint setup"
	}
	return "docker is not installed — install it from https://docs.docker.com/get-docker/, then re-run flint setup"
}

func remedyLightweightRuntime(msg string) (string, bool) {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "colima"):
		return "start Colima (colima start), then re-run flint setup", true
	case strings.Contains(l, "orbstack"):
		return "start OrbStack (open -a OrbStack), then re-run flint setup", true
	case strings.Contains(l, "podman"):
		return "start the podman machine (podman machine start), then re-run flint setup", true
	}
	return "", false
}

type FirstRunConfig struct {
	PingRetryCount    int
	PingRetryInterval time.Duration
}

func (c FirstRunConfig) normalized() (int, time.Duration) {
	n, d := c.PingRetryCount, c.PingRetryInterval
	if n <= 0 {
		n = defaultPingRetryCount
	}
	if d <= 0 {
		d = defaultPingRetryInterval
	}
	return n, d
}

func EnsureFirstRun(ctx context.Context, st Store, cfg FirstRunConfig) int {
	ux := newSetupUX(setupStderr, false)

	ux.Step("Start engine (docker compose up)")
	if err := composeUpFn(ctx); err != nil {
		kind, remedy := ClassifyEngineError(err)
		ux.Remedy(kind, remedy)
		return 1
	}

	retries, interval := cfg.normalized()
	ux.Step("Check DB connection (ping)")
	connected := false
	for i := 0; i < retries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				ux.Remedy("ping_timeout", pingTimeoutRemedy(retries))
				return 1
			case <-time.After(interval):
			}
		}
		if st != nil {
			if err := st.Ping(ctx); err == nil {
				connected = true
				break
			}
		}
		if s, err := newStore(ctx, dsnFromEnv()); err == nil {
			st = s
			if err := st.Ping(ctx); err == nil {
				connected = true
				break
			}
		}
	}
	if !connected {
		ux.Remedy("ping_timeout", pingTimeoutRemedy(retries))
		return 1
	}
	ux.Success("connected")

	ux.Step("Migration")
	if err := st.Migrate(ctx); err != nil {
		ux.Fail(err)
		return 1
	}
	ux.Success("schema synced")
	return 0
}

func pingTimeoutRemedy(retries int) string {
	return fmt.Sprintf("engine did not accept connections within %d ping retries — run flint doctor to diagnose", retries)
}

var dockerPSFn = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", append([]string{"ps"}, args...)...).CombinedOutput()
	return string(out), err
}

var publishedPortRe = regexp.MustCompile(`:([0-9]+)->`)

func firstPublishedPort(out string) (int, bool) {
	m := publishedPortRe.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	port, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return port, true
}

func ExistingInstall(ctx context.Context) (adopt bool, port int, stop bool) {
	out, err := dockerPSFn(ctx, "--filter", "label=com.docker.compose.project=flint", "--format", "{{.Ports}}")
	if err == nil {
		if p, ok := firstPublishedPort(out); ok {
			return true, p, false
		}
	}
	if setupPortDialFn(fmt.Sprintf("localhost:%d", defaultEnginePort)) {
		return false, defaultEnginePort, true
	}
	return false, 0, false
}

func ExistingInstallRemedy(port int) string {
	return fmt.Sprintf("port %d is already held by a non-flint process — stop it or follow the migration procedure in docs/RUNBOOK.md §11, then re-run flint setup", port)
}
