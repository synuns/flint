package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/engine"
	"github.com/synuns/flint/lib/wire"
)

const doctorPort = 5434

const enginePinnedImage = "dolthub/doltgresql@sha256:3a119bb6726a0be283cfda2a73fa9fbdff58df06c0b850f245cd1828f57281a6"

const flintProjectLabel = "com.docker.compose.project"

var ComposePsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "ps", "--format", "json").Output()
}

type composePsPublisher struct {
	PublishedPort int `json:"PublishedPort"`
}

type composePsEntry struct {
	Name       string               `json:"Name"`
	Names      string               `json:"Names"`
	State      string               `json:"State"`
	Image      string               `json:"Image"`
	Labels     string               `json:"Labels"`
	Ports      string               `json:"Ports"`
	Publishers []composePsPublisher `json:"Publishers"`
}

func (e composePsEntry) displayName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.Names
}

var hostPortRe = regexp.MustCompile(`:(\d+)->`)

func entryProject(e composePsEntry) string {
	for _, kv := range strings.Split(e.Labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.TrimSpace(k) == flintProjectLabel {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func checkPort(ctx context.Context) DoctorCheck {
	c := DoctorCheck{Name: "port"}

	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		c.Status = "skip"
		c.Detail = "docker ps failed: " + err.Error()
		return c
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		c.Status = "skip"
		c.Detail = "cannot parse docker ps output: " + perr.Error()
		return c
	}

	port := enginePort()
	for _, e := range entries {
		if entryProject(e) == "flint" && e.State == "running" && publishesPort(e, port) {
			c.Status = "ok"
			c.Detail = fmt.Sprintf("%s (project flint, managed) publishes %d", e.displayName(), port)
			return c
		}
	}

	for _, e := range entries {
		if publishesPort(e, port) {
			c.Status = "skip"
			c.Detail = fmt.Sprintf("port %d held by %s (project %q) — unknown, not flint-managed", port, e.displayName(), entryProject(e))
			return c
		}
	}

	if EngineModeFn != nil {
		if mode, merr := EngineModeFn(); merr == nil && mode == "native" {
			c.Status = "skip"
			c.Detail = "native engine mode — container port check not applicable"
			return c
		}
	}
	c.Status = "fail"
	c.Detail = fmt.Sprintf("no flint-managed container (label %s=flint) publishes %d — unknown", flintProjectLabel, port)
	c.Fix = "fix: flint setup (starts the pinned embedded-compose engine)"
	return c
}

func parseComposePs(b []byte) ([]composePsEntry, error) {
	trimmed := bytes.TrimSpace(b)
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var out []composePsEntry
	if len(trimmed) > 0 && trimmed[0] == '[' {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return nil, errors.New("expected array start")
		}
		for dec.More() {
			var e composePsEntry
			if err := dec.Decode(&e); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	for {
		var e composePsEntry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
}

func IsFlintContainer(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "flint")
}

func publishesPort(e composePsEntry, port int) bool {
	for _, p := range e.Publishers {
		if p.PublishedPort == port {
			return true
		}
	}
	for _, m := range hostPortRe.FindAllStringSubmatch(e.Ports, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n == port {
			return true
		}
	}
	return false
}

func enginePort() int {
	if u, err := url.Parse(wire.ResolveDSN()); err == nil {
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				return n
			}
		}
	}
	return doctorPort
}

func engineContainerName(ctx context.Context) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		return "", err
	}
	entries, err := parseComposePs(b)
	if err != nil {
		return "", err
	}
	port := enginePort()
	for _, e := range entries {
		if e.State == "running" && publishesPort(e, port) {
			return e.displayName(), nil
		}
	}
	return "", fmt.Errorf("no running container publishes %d", port)
}

var EngineStatsFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "json").Output()
}

type dockerStatsEntry struct {
	Name     string `json:"Name"`
	MemUsage string `json:"MemUsage"`
}

var memUsageUnits = map[string]float64{
	"B": 1.0 / (1 << 20), "KiB": 1.0 / (1 << 10), "MiB": 1, "GiB": 1 << 10, "TiB": 1 << 20,
	"kB": 1e3 / (1 << 20), "KB": 1e3 / (1 << 20),
	"MB": 1e6 / (1 << 20), "GB": 1e9 / (1 << 20), "TB": 1e12 / (1 << 20),
}

func parseMemUsageMB(s string) (int, bool) {
	used, _, _ := strings.Cut(s, "/")
	var f float64
	var unit string
	if n, _ := fmt.Sscanf(strings.TrimSpace(used), "%f%s", &f, &unit); n != 2 {
		return 0, false
	}
	factor, ok := memUsageUnits[unit]
	if !ok || f < 0 {
		return 0, false
	}
	return int(f * factor), true
}

func EngineMemMB(ctx context.Context) (int, bool, error) {
	name, err := engineContainerName(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("engine container selection: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := EngineStatsFn(tctx)
	if err != nil {
		return 0, false, err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b)))
	for {
		var e dockerStatsEntry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, false, fmt.Errorf("docker stats has no row for container %q", name)
			}
			return 0, false, fmt.Errorf("docker stats decode: %w", err)
		}
		if e.Name == name {
			mb, ok := parseMemUsageMB(e.MemUsage)
			if !ok {
				return 0, false, fmt.Errorf("unparsable MemUsage %q for container %q", e.MemUsage, name)
			}
			return mb, true, nil
		}
	}
}

var RestartCountFn = func(ctx context.Context) (int, error) {
	name, err := engineContainerName(ctx)
	if err != nil {
		return 0, err
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(tctx, "docker", "inspect", "--format", "{{.RestartCount}}", name).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

var HandshakeFn = func(ctx context.Context, st wire.Store) (db.EngineInfo, error) {
	return db.Handshake(ctx, st)
}

var ManagedEngineImageFn = func(ctx context.Context) (string, bool) {
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := ComposePsFn(tctx)
	if err != nil {
		return "", false
	}
	entries, perr := parseComposePs(b)
	if perr != nil {
		return "", false
	}
	for _, e := range entries {
		if entryProject(e) == "flint" && e.State == "running" {
			return e.Image, true
		}
	}
	return "", false
}

func engineVersionPart(ctx context.Context, st wire.Store) (part string, warn string, fix string) {
	if st == nil {
		return "engine unknown (no store)", "", ""
	}
	info, err := HandshakeFn(ctx, st)
	if err != nil {
		return "engine unknown (handshake failed: " + err.Error() + ")", "", ""
	}
	part = "engine " + info.Kind + " " + info.Version
	if info.Kind == "" {
		part = "engine unknown kind " + info.Version
	}
	img, managed := ManagedEngineImageFn(ctx)
	if !managed {
		return part + " (external — version recorded only)", "", ""
	}
	if info.Kind != "doltgres" {
		return part + " (managed)", "managed engine is " + info.Kind + " " + info.Version + " — flint requires Doltgres",
			"fix: flint setup (starts the pinned Doltgres engine)"
	}
	if img != enginePinnedImage {
		return part + fmt.Sprintf(" (managed, image %s — not pinned)", img),
			fmt.Sprintf("managed engine image drift — running %s but flint pins %s", img, enginePinnedImage),
			"fix: flint teardown --yes --purge then flint setup (re-pins the engine image)"
	}
	return part + " (managed, pinned)", "", ""
}

func checkEngineFootprint(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "engine-footprint"}
	if engine.CurrentMode() == engine.ModeNative {
		mb, ok, err := NativeEngineRSSFn(ctx)
		if !ok {
			c.Status = "skip"
			c.Detail = "native rss unavailable — check skipped"
			if err != nil {
				c.Detail = "engine-footprint skipped — " + err.Error()
			}
			return c
		}
		c.Status = "ok"
		c.Detail = fmt.Sprintf("engine working set %dMB (native)", mb)
		return c
	}
	mb, ok, err := EngineMemMB(ctx)
	if !ok {
		c.Status = "skip"
		c.Detail = "docker stats unavailable — check skipped"
		if err != nil {
			c.Detail = "engine-footprint skipped — " + err.Error()
		}
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("engine working set %dMB", mb)
	if restarts, rerr := RestartCountFn(ctx); rerr == nil {
		c.Detail += fmt.Sprintf(" · restarts %d", restarts)
	}
	return c
}

var EngineStatusFn = defaultEngineStatus

func defaultEngineStatus() (engine.ServiceStatus, error) {
	s, err := engine.Status()
	if errors.Is(err, engine.ErrNotWired) && engine.CurrentMode() == engine.ModeNative {
		engine.SetNativeService(engine.NewNativeService())
		s, err = engine.Status()
	}
	return s, err
}

var NativeEngineRSSFn = defaultNativeEngineRSS

func defaultNativeEngineRSS(ctx context.Context) (int, bool, error) {
	if engine.CurrentMode() != engine.ModeNative {
		return 0, false, nil
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(tctx, "ps", "-eo", "rss=,comm=").Output()
	if err != nil {
		return 0, false, err
	}
	best := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := filepath.Base(fields[len(fields)-1])
		if name != "doltgres" && !strings.Contains(name, "doltgres") {
			continue
		}
		if kb, aerr := strconv.Atoi(fields[0]); aerr == nil && kb > best {
			best = kb
		}
	}
	if best == 0 {
		return 0, false, nil
	}
	return best / 1024, true, nil
}

func checkEngine(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "engine"}
	mode := engine.CurrentMode()
	if mode != engine.ModeNative {
		c.Status = "skip"
		c.Detail = "native mode only — current mode " + mode
		return c
	}
	if EngineStatusFn == nil {
		c.Status = "skip"
		c.Detail = "engine status unwired — check skipped"
		return c
	}
	s, err := EngineStatusFn()
	if err != nil {
		c.Status = "fail"
		c.Detail = "engine status unavailable: " + err.Error()
		c.Fix = "fix: flint setup --engine native"
		return c
	}
	if !s.Registered {
		c.Status = "fail"
		c.Detail = "native engine service not registered"
		c.Fix = "fix: flint setup --engine native (registers the service manager entry)"
		return c
	}
	if !s.Running {
		c.Status = "fail"
		c.Detail = "native engine registered but not running"
		c.Fix = "fix: flint engine start"
		return c
	}
	if s.Version != engine.NativePinVersion {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("native engine %s differs from pinned %s", s.Version, engine.NativePinVersion)
		c.Fix = "fix: flint setup --engine native (updates the engine to the pinned version)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("native engine %s (pinned %s) registered and running", s.Version, engine.NativePinVersion)
	return c
}
