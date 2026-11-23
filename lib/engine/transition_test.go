package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func resetTransitionSeams(t *testing.T) {
	t.Helper()
	savedVol, savedCopy, savedQuery, savedDL := containerDataDirPath, copyDataDir, queryDBState, downloadEngine
	containerDataDirPath, copyDataDir, queryDBState, downloadEngine = nil, nil, nil, nil
	t.Cleanup(func() {
		containerDataDirPath, copyDataDir, queryDBState, downloadEngine = savedVol, savedCopy, savedQuery, savedDL
	})
}

type transitionEnv struct {
	home        string
	events      *[]string
	copySrc     *string
	copyDst     *string
	containerOn *bool
	nativeOn    *bool
}

func setupTransition(t *testing.T, states []dbState, stateErrs []error) *transitionEnv {
	t.Helper()
	withConfigDir(t, `{"engine":{"mode":"managed-container"}}`)
	resetSeams(t)
	resetTransitionSeams(t)
	home := t.TempDir()
	t.Setenv("FLINT_HOME", home)
	env := &transitionEnv{
		home:        home,
		events:      &[]string{},
		copySrc:     new(string),
		copyDst:     new(string),
		containerOn: new(bool),
		nativeOn:    new(bool),
	}
	vol := t.TempDir()
	containerDataDirPath = func() string { return vol }
	idx := 0
	queryDBState = func(ctx context.Context, mode string) (dbState, error) {
		*env.events = append(*env.events, "query:"+mode)
		if stateErrs != nil && idx < len(stateErrs) && stateErrs[idx] != nil {
			err := stateErrs[idx]
			idx++
			return dbState{}, err
		}
		var st dbState
		if states != nil && idx < len(states) {
			st = states[idx]
		}
		idx++
		return st, nil
	}
	copyDataDir = func(src, dst string) error {
		*env.events = append(*env.events, "copy")
		*env.copySrc, *env.copyDst = src, dst
		return nil
	}
	containerService.Stop = func() error { *env.events = append(*env.events, "stop"); return nil }
	containerService.Start = func() error {
		*env.events = append(*env.events, "container-start")
		*env.containerOn = true
		return nil
	}
	nativeService.Start = func() error { *env.events = append(*env.events, "native-start"); *env.nativeOn = true; return nil }
	return env
}

func TestContainerDataDirPathDefaultAndOverride(t *testing.T) {
	t.Setenv("FLINT_ENGINE_CONTAINER_DATA", "")
	if got := containerDataDirPath(); got != "/var/lib/docker/volumes/flint_doltgres-data/_data" {
		t.Fatalf("default = %q, want flint_doltgres-data volume path", got)
	}
	t.Setenv("FLINT_ENGINE_CONTAINER_DATA", "/demo/custom/volume")
	if got := containerDataDirPath(); got != "/demo/custom/volume" {
		t.Fatalf("override = %q, want /demo/custom/volume", got)
	}
}

func TestContainerToNativeSuccessOrder(t *testing.T) {
	env := setupTransition(t, []dbState{
		{logDepth: 7, asOf: "demo-asof-1"},
		{logDepth: 7, asOf: "demo-asof-1"},
	}, nil)

	if err := ContainerToNative(context.Background()); err != nil {
		t.Fatalf("ContainerToNative: %v", err)
	}
	want := []string{
		"query:managed-container",
		"stop",
		"copy",
		"native-start",
		"query:native",
	}
	if !reflect.DeepEqual(*env.events, want) {
		t.Fatalf("events = %v, want %v", *env.events, want)
	}
	if *env.containerOn {
		t.Fatal("container restart must not run on success")
	}
	if got := CurrentMode(); got != ModeNative {
		t.Fatalf("mode = %q, want native", got)
	}
	if wantDst := filepath.Join(env.home, "engine", "data"); *env.copyDst != wantDst {
		t.Fatalf("copy dst = %q, want %q", *env.copyDst, wantDst)
	}
	if *env.copySrc == "" {
		t.Fatal("copy src not captured")
	}
}

func TestContainerToNativeEquivalenceMismatchRollsBack(t *testing.T) {
	env := setupTransition(t, []dbState{
		{logDepth: 7, asOf: "demo-asof-1"},
		{logDepth: 9, asOf: "demo-asof-2"},
	}, nil)

	err := ContainerToNative(context.Background())
	if err == nil {
		t.Fatal("ContainerToNative succeeded, want equivalence mismatch error")
	}
	if !strings.Contains(err.Error(), "not equivalent") {
		t.Fatalf("err = %v, want equivalence mismatch", err)
	}
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("mode = %q, want managed-container restored", got)
	}
	if !*env.containerOn {
		t.Fatal("container not restarted after mismatch")
	}
	last := (*env.events)[len(*env.events)-1]
	if last != "container-start" {
		t.Fatalf("last event = %q, want container-start", last)
	}
}

func TestContainerToNativeCopyFailureRestartsContainer(t *testing.T) {
	env := setupTransition(t, []dbState{{logDepth: 1, asOf: "demo-asof-1"}}, nil)
	copyDataDir = func(src, dst string) error { return errors.New("copy boom") }

	err := ContainerToNative(context.Background())
	if err == nil || !strings.Contains(err.Error(), "copy boom") {
		t.Fatalf("err = %v, want copy failure", err)
	}
	if !*env.containerOn {
		t.Fatal("container not restarted after copy failure")
	}
	if *env.nativeOn {
		t.Fatal("native must not start after copy failure")
	}
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("mode = %q, want managed-container", got)
	}
}

func TestContainerToNativeNativeStartFailureRestartsContainer(t *testing.T) {
	env := setupTransition(t, []dbState{{logDepth: 1, asOf: "demo-asof-1"}}, nil)
	nativeService.Start = func() error { *env.events = append(*env.events, "native-start"); return errors.New("start boom") }

	err := ContainerToNative(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start boom") {
		t.Fatalf("err = %v, want native start failure", err)
	}
	if !*env.containerOn {
		t.Fatal("container not restarted after native start failure")
	}
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("mode = %q, want managed-container", got)
	}
}

func TestContainerToNativeBaselineQueryFailureNoStop(t *testing.T) {
	env := setupTransition(t, nil, []error{errors.New("query boom")})

	err := ContainerToNative(context.Background())
	if err == nil || !strings.Contains(err.Error(), "query boom") {
		t.Fatalf("err = %v, want baseline query failure", err)
	}
	for _, ev := range *env.events {
		if ev == "stop" || ev == "copy" || ev == "native-start" || ev == "container-start" {
			t.Fatalf("unexpected event %q after baseline failure, events = %v", ev, *env.events)
		}
	}
	if got := CurrentMode(); got != ModeManagedContainer {
		t.Fatalf("mode = %q, want managed-container", got)
	}
}

func TestContainerToNativeNilSeams(t *testing.T) {
	setupTransition(t, nil, nil)
	nativeService.Start = nil
	if err := ContainerToNative(context.Background()); !errors.Is(err, ErrNotWired) {
		t.Fatalf("err = %v, want ErrNotWired", err)
	}
}

func TestContainerToNativeRollbackStopsNativeBeforeContainerV1311(t *testing.T) {
	env := setupTransition(t, []dbState{{logDepth: 1, asOf: "demo-asof-1"}}, nil)
	nativeService.Start = func() error { return errors.New("start boom") }
	nativeService.Stop = func() error { *env.events = append(*env.events, "native-stop"); return nil }

	err := ContainerToNative(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start boom") {
		t.Fatalf("err = %v, want native start failure", err)
	}
	events := *env.events
	stopIdx, containerIdx := -1, -1
	for i, ev := range events {
		switch ev {
		case "native-stop":
			stopIdx = i
		case "container-start":
			containerIdx = i
		}
	}
	if stopIdx == -1 {
		t.Fatalf("rollback must stop native first, events = %v", events)
	}
	if containerIdx == -1 || containerIdx < stopIdx {
		t.Fatalf("container restart must follow native stop, events = %v", events)
	}
}

func TestCopyDataDirRealChmodAndContents(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "db", "dolt"), []byte("demo-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "engine", "data")

	if err := copyDataDirReal(src, dst); err != nil {
		t.Fatalf("copyDataDirReal: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dst perm = %o, want 700", perm)
	}
	b, err := os.ReadFile(filepath.Join(dst, "db", "dolt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "demo-data" {
		t.Fatalf("copied content = %q", b)
	}
}

func TestCopyDataDirRealRejectsMissingSource(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "data")
	if err := copyDataDirReal(filepath.Join(t.TempDir(), "absent"), dst); err == nil {
		t.Fatal("copyDataDirReal succeeded, want error for missing source")
	}
}

func setupUpgrade(t *testing.T, fromVer, toVer string) string {
	t.Helper()
	tb := buildTarball(t, map[string]string{"bin/doltgres": "fake-binary"})
	sum := sha256.Sum256(tb)
	savedHash := pinHashes["darwin/arm64"]
	pinHashes["darwin/arm64"] = hex.EncodeToString(sum[:])
	savedPlatform := runtimePlatform
	runtimePlatform = func() (string, string) { return "darwin", "arm64" }
	savedExec := execVersion
	execVersion = func(string) (string, error) { return "Doltgres version " + toVer + "\n", nil }
	t.Cleanup(func() {
		pinHashes["darwin/arm64"] = savedHash
		runtimePlatform = savedPlatform
		execVersion = savedExec
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tb)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("FLINT_ENGINE_URL", ts.URL)
	home := t.TempDir()
	t.Setenv("FLINT_HOME", home)
	if os.Getenv("SUPERLITE_REAL") != "1" {
		saved := downloadEngine
		downloadEngine = func(ctx context.Context, ver string) (string, error) {
			engDir := filepath.Join(home, "engine")
			if err := os.MkdirAll(filepath.Join(engDir, ver), 0o700); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(engDir, ver, "doltgres"), []byte("fake-binary"), 0o755); err != nil {
				return "", err
			}
			if err := setCurrentVersion(engDir, ver); err != nil {
				return "", err
			}
			return filepath.Join(engDir, ver), nil
		}
		t.Cleanup(func() { downloadEngine = saved })
	}
	return home
}

func seedFromVersion(t *testing.T, home, fromVer string) string {
	t.Helper()
	engDir := filepath.Join(home, "engine")
	if err := os.MkdirAll(filepath.Join(engDir, fromVer), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engDir, fromVer, "doltgres"), []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := setCurrentVersion(engDir, fromVer); err != nil {
		t.Fatal(err)
	}
	return engDir
}

func TestUpgradeRoundTrip(t *testing.T) {
	fromVer, toVer := "0.9.0", NativePinVersion
	home := setupUpgrade(t, fromVer, toVer)
	engDir := seedFromVersion(t, home, fromVer)
	resetSeams(t)
	started := false
	nativeService.Start = func() error { started = true; return nil }

	if err := UpgradeRoundTrip(context.Background(), fromVer, toVer); err != nil {
		t.Fatalf("UpgradeRoundTrip: %v", err)
	}
	if !started {
		t.Fatal("native service not started during round trip")
	}
	link, err := os.Readlink(filepath.Join(engDir, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if link != fromVer {
		t.Fatalf("current -> %q, want rollback to %q", link, fromVer)
	}
	if _, err := os.Stat(filepath.Join(engDir, fromVer, "doltgres")); err != nil {
		t.Fatalf("from-version directory not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(engDir, toVer, "doltgres")); err != nil {
		t.Fatalf("to-version not deployed: %v", err)
	}
}

func TestUpgradeRoundTripStartFailureRollsBack(t *testing.T) {
	fromVer, toVer := "0.9.0", NativePinVersion
	home := setupUpgrade(t, fromVer, toVer)
	engDir := seedFromVersion(t, home, fromVer)
	resetSeams(t)
	nativeService.Start = func() error { return errors.New("start boom") }

	err := UpgradeRoundTrip(context.Background(), fromVer, toVer)
	if err == nil || !strings.Contains(err.Error(), "start boom") {
		t.Fatalf("err = %v, want native start failure", err)
	}
	link, lerr := os.Readlink(filepath.Join(engDir, "current"))
	if lerr != nil {
		t.Fatal(lerr)
	}
	if link != fromVer {
		t.Fatalf("current -> %q, want rollback to %q on failure", link, fromVer)
	}
}

func TestUpgradeRoundTripDownloadFailureRollsBack(t *testing.T) {
	fromVer, toVer := "0.9.0", NativePinVersion
	home := setupUpgrade(t, fromVer, toVer)
	engDir := seedFromVersion(t, home, fromVer)
	saved := downloadEngine
	downloadEngine = func(ctx context.Context, ver string) (string, error) {
		return "", errors.New("download boom")
	}
	t.Cleanup(func() { downloadEngine = saved })
	resetSeams(t)
	nativeService.Start = func() error { return nil }

	err := UpgradeRoundTrip(context.Background(), fromVer, toVer)
	if err == nil || !strings.Contains(err.Error(), "download boom") {
		t.Fatalf("err = %v, want download failure", err)
	}
	link, lerr := os.Readlink(filepath.Join(engDir, "current"))
	if lerr != nil {
		t.Fatal(lerr)
	}
	if link != fromVer {
		t.Fatalf("current -> %q, want rollback to %q on download failure", link, fromVer)
	}
}

func TestUpgradeRoundTripRejectsBadArgs(t *testing.T) {
	home := setupUpgrade(t, "0.9.0", NativePinVersion)
	seedFromVersion(t, home, "0.9.0")
	if err := UpgradeRoundTrip(context.Background(), "", NativePinVersion); err == nil {
		t.Fatal("empty fromVer want error")
	}
	if err := UpgradeRoundTrip(context.Background(), "0.9.0", ""); err == nil {
		t.Fatal("empty toVer want error")
	}
	if err := UpgradeRoundTrip(context.Background(), "8.8.8", NativePinVersion); err == nil {
		t.Fatal("missing from-version directory want error")
	}
}

func TestUpgradeRoundTripRollbackRestartsServiceV1311(t *testing.T) {
	fromVer, toVer := "0.9.0", NativePinVersion
	home := setupUpgrade(t, fromVer, toVer)
	engDir := seedFromVersion(t, home, fromVer)
	saved := downloadEngine
	downloadEngine = func(ctx context.Context, ver string) (string, error) {
		return "", errors.New("download boom")
	}
	t.Cleanup(func() { downloadEngine = saved })
	resetSeams(t)
	var seq []string
	nativeService.Stop = func() error { seq = append(seq, "stop"); return nil }
	nativeService.Start = func() error { seq = append(seq, "start"); return nil }

	err := UpgradeRoundTrip(context.Background(), fromVer, toVer)
	if err == nil || !strings.Contains(err.Error(), "download boom") {
		t.Fatalf("err = %v, want download failure", err)
	}
	link, lerr := os.Readlink(filepath.Join(engDir, "current"))
	if lerr != nil {
		t.Fatal(lerr)
	}
	if link != fromVer {
		t.Fatalf("current -> %q, want rollback to %q", link, fromVer)
	}
	if !reflect.DeepEqual(seq, []string{"stop", "start"}) {
		t.Fatalf("rollback service sequence = %v, want stop then start after symlink restore", seq)
	}
}

func TestUpgradeRoundTripNilSeamKeepsErrorV1312(t *testing.T) {
	prevS, prevD := nativeService, downloadEngine
	nativeService = NativeService{}
	downloadEngine = func(ctx context.Context, ver string) (string, error) {
		return "", errors.New("engine: download boom")
	}
	t.Cleanup(func() { nativeService, downloadEngine = prevS, prevD })
	dir := t.TempDir()
	t.Setenv("FLINT_HOME", dir)
	engDir := filepath.Join(dir, "engine", "1.4.0")
	if err := os.MkdirAll(engDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := setCurrentVersion(filepath.Join(dir, "engine"), "1.4.0"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	err := UpgradeRoundTrip(context.Background(), "1.4.0", "1.5.0")
	if err == nil {
		t.Fatal("에러 기대")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("nil-seam 시 ErrNotWired 병합 기대: %v", err)
	}
}
