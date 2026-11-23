package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

const reservedTestEnginePort = 5499

var portBindFn = func(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

var portExecCommand = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func validPort(p int) bool {
	return p >= 1 && p <= 65535
}

func PickPort(defaultPort int) (int, error) {
	if !validPort(defaultPort) {
		return 0, fmt.Errorf("port: invalid default port %d", defaultPort)
	}
	for p := defaultPort; p <= 65535; p++ {
		if p == reservedTestEnginePort {
			continue
		}
		if portBindFn(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("port: no available port in %d..65535", defaultPort)
}

func ReadPortConfig(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var c struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, false
	}
	if !validPort(c.Port) {
		return 0, false
	}
	return c.Port, true
}

func SetPortConfig(path string, port int) error {
	if !validPort(port) {
		return fmt.Errorf("port: invalid port %d", port)
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("port: parse %s: %w", path, err)
		}
	}
	cfg["port"] = port
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func DescribeOccupant(port int) string {
	var parts []string
	if out, err := portExecCommand("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port)); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] == "COMMAND" {
				continue
			}
			parts = append(parts, fmt.Sprintf("process %s (pid %s)", fields[0], fields[1]))
			break
		}
	}
	filter := fmt.Sprintf("publish=%d", port)
	if out, err := portExecCommand("docker", "ps", "--filter", filter, "--format", "{{.Names}}"); err == nil {
		for _, name := range strings.Fields(out) {
			parts = append(parts, "docker container "+name)
		}
	}
	return strings.Join(parts, "; ")
}
