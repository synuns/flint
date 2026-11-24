package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const pluginVersionGapKey = "plugin_version_gap_minors"

var pluginInstalledVersionFn = readInstalledPluginVersion

func CheckPluginGap(ctx context.Context, st wire.Store) DoctorCheck {
	return CheckPluginGapTh(ctx, st, knowledge.WriteThresholds(ctx, st))
}

func CheckPluginGapTh(_ context.Context, _ wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "plugin-version-gap"}
	cli := CurrentVersionFn()
	if ValidVersionFn == nil || !ValidVersionFn(cli) {
		c.Status = "ok"
		c.Detail = "CLI version not finalized (dev) — verdict deferred"
		return c
	}
	plugin := pluginInstalledVersionFn()
	if plugin == "" {
		c.Status = "ok"
		c.Detail = "flint plugin not detected"
		return c
	}
	gap := versionMinorGap(cli, plugin)
	limit := store.ClampThresholdInt(pluginVersionGapKey, th.PluginVersionGapMinors)
	if gap > limit {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("plugin v%s vs CLI v%s — minor gap %d exceeds %s %d — reinstall via /plugin install flint@flint", plugin, cli, gap, pluginVersionGapKey, limit)
		c.Fix = "fix: flint update"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("plugin v%s within %d minors of CLI v%s", plugin, limit, cli)
	return c
}

func versionMinorGap(a, b string) int {
	am, an, okA := versionMajorMinor(a)
	bm, bn, okB := versionMajorMinor(b)
	if !okA || !okB {
		return 0
	}
	if am != bm {
		return 1000
	}
	d := an - bn
	if d < 0 {
		d = -d
	}
	return d
}

func versionMajorMinor(v string) (int, int, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, minor, true
}

func readInstalledPluginVersion() string {
	home := ClaudeHome()
	if home == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "installed_plugins.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return ""
	}
	for name, entries := range doc.Plugins {
		if !strings.HasPrefix(name, "flint@") {
			continue
		}
		for _, e := range entries {
			if e.Version != "" {
				return e.Version
			}
		}
	}
	return ""
}
