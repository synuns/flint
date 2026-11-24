package ops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckFreshnessWarnsAfter72hGap(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	c := CheckFreshness(context.Background(), &fakeStore{})
	if c.Name != "collection-freshness" || c.Status != "warn" {
		t.Fatalf("got %+v, want name=collection-freshness status=warn", c)
	}
}

func freshnessStore(last time.Time) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "session_starts") {
			return [][]any{{last}}, nil
		}
		return nil, nil
	}}
}

func TestFreshnessStale(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	stale := CheckFreshness(context.Background(), freshnessStore(time.Now().Add(-73*time.Hour)))
	if stale.Name != "collection-freshness" || stale.Status != "warn" {
		t.Fatalf("73h gap: got %+v, want status=warn", stale)
	}
	if !strings.Contains(stale.Detail, "73h") {
		t.Errorf("detail %q lacks the stale age", stale.Detail)
	}
	fresh := CheckFreshness(context.Background(), freshnessStore(time.Now().Add(-1*time.Hour)))
	if fresh.Status != "ok" {
		t.Fatalf("1h gap: got %s, want ok (%s)", fresh.Status, fresh.Detail)
	}
}

func TestThresholdOverride(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "collection_freshness_hours") {
			return [][]any{{"collection_freshness_hours", "24"}}, nil
		}
		if strings.Contains(sql, "session_starts") {
			return [][]any{{time.Now().Add(-25 * time.Hour)}}, nil
		}
		return nil, nil
	}}
	c := CheckFreshness(context.Background(), st)
	if c.Status != "warn" {
		t.Fatalf("25h with 24h override: got %s, want warn (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "24h") {
		t.Errorf("detail %q lacks the overridden 24h threshold", c.Detail)
	}
}

func TestFreshnessRangeClamp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "collection_freshness_hours") {
			return [][]any{{"collection_freshness_hours", "3"}}, nil
		}
		if strings.Contains(sql, "session_starts") {
			return [][]any{{time.Now().Add(-4 * time.Hour)}}, nil
		}
		return nil, nil
	}}
	c := CheckFreshness(context.Background(), st)
	if c.Status != "ok" {
		t.Fatalf("out-of-range override 3 must fall back to 72: got %s, want ok (%s)", c.Status, c.Detail)
	}
}

func stubPluginEnv(t *testing.T, cli, plugin string) {
	t.Helper()
	oldCur, oldValid, oldPlugin := CurrentVersionFn, ValidVersionFn, pluginInstalledVersionFn
	CurrentVersionFn = func() string { return cli }
	ValidVersionFn = func(string) bool { return cli != "" }
	pluginInstalledVersionFn = func() string { return plugin }
	t.Cleanup(func() { CurrentVersionFn, ValidVersionFn, pluginInstalledVersionFn = oldCur, oldValid, oldPlugin })
}

func TestPluginGapWarn(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	st := &fakeStore{}
	stubPluginEnv(t, "1.33.0", "1.30.0")
	c := CheckPluginGap(context.Background(), st)
	if c.Name != "plugin-version-gap" || c.Status != "warn" {
		t.Fatalf("gap 3 minors: got %+v, want warn", c)
	}
	if !strings.Contains(c.Detail, "3") {
		t.Errorf("detail %q lacks the gap count", c.Detail)
	}

	stubPluginEnv(t, "1.33.0", "1.33.0")
	c = CheckPluginGap(context.Background(), st)
	if c.Status != "ok" {
		t.Fatalf("same version: got %s, want ok (%s)", c.Status, c.Detail)
	}

	stubPluginEnv(t, "1.33.0", "")
	c = CheckPluginGap(context.Background(), st)
	if c.Status != "ok" {
		t.Fatalf("plugin absent: got %s, want ok (%s)", c.Status, c.Detail)
	}
}

func TestPluginGapThresholdOverride(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "plugin_version_gap_minors") {
			return [][]any{{"plugin_version_gap_minors", "10"}}, nil
		}
		return nil, nil
	}}
	stubPluginEnv(t, "1.33.0", "1.30.0")
	c := CheckPluginGap(context.Background(), st)
	if c.Status != "ok" {
		t.Fatalf("gap 3 with meta override 10: got %s, want ok — meta 오버라이드 미반영 (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "10") {
		t.Errorf("detail %q lacks the overridden limit 10", c.Detail)
	}
}

func stubServiceEnv(t *testing.T, goos, path string, data []byte, pathErr error) {
	t.Helper()
	oldOS, oldPath, oldRead := serviceRemnantOSFn, serviceRemnantPathFn, serviceRemnantReadFileFn
	serviceRemnantOSFn = func() string { return goos }
	serviceRemnantPathFn = func() (string, error) { return path, pathErr }
	serviceRemnantReadFileFn = func(string) ([]byte, error) {
		if pathErr != nil {
			return nil, pathErr
		}
		return data, nil
	}
	t.Cleanup(func() { serviceRemnantOSFn, serviceRemnantPathFn, serviceRemnantReadFileFn = oldOS, oldPath, oldRead })
}

func TestServiceRemnant(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v", r)
		}
	}()
	home := t.TempDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceRemnantLabel+".plist")
	stubServiceEnv(t, "darwin", plist,
		[]byte("<string>/tmp/flint-demo-zombie/engine</string>"), nil)
	c := CheckServiceRemnant()
	if c.Name != "service-remnant" || c.Status != "warn" {
		t.Fatalf("tmp plist: got %+v, want warn", c)
	}

	stubServiceEnv(t, "darwin", plist,
		[]byte("/opt/flint-demo/engine/current/doltgres -data-dir /opt/flint-demo/engine/data"), nil)
	c = CheckServiceRemnant()
	if c.Status != "ok" {
		t.Fatalf("clean plist: got %s, want ok (%s)", c.Status, c.Detail)
	}

	stubServiceEnv(t, "darwin", plist, nil, errors.New("not found"))
	c = CheckServiceRemnant()
	if c.Status != "ok" {
		t.Fatalf("no definition: got %s, want ok (%s)", c.Status, c.Detail)
	}

	stubServiceEnv(t, "windows", "", nil, nil)
	c = CheckServiceRemnant()
	if c.Status != "ok" {
		t.Fatalf("platform without service manager: got %s, want ok (%s)", c.Status, c.Detail)
	}
}
