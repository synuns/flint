//go:build e2e

package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/engine"
)

func v1310RequireReal(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("SUPERLITE_REAL 미설정 — 실 엔진 v1.31.0 e2e skip")
	}
}

func v1310Dialable(dsn string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return false
	}
	pool.Close()
	return true
}

func v1310SimpleDSN(dsn string) string {
	suffix := "?default_query_exec_mode=simple_protocol"
	if strings.Contains(dsn, "?") {
		suffix = "&default_query_exec_mode=simple_protocol"
	}
	return dsn + suffix
}

func v1310Docker(t *testing.T, args ...string) error {
	t.Helper()
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(err.Error() + ": " + strings.TrimSpace(string(out)))
	}
	return nil
}

func v1310Scalar(t *testing.T, dsn, sql string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, v1310SimpleDSN(dsn))
	if err != nil {
		t.Fatalf("접속 실패(%s): %v", dsn, err)
	}
	defer pool.Close()
	var v string
	if err := pool.QueryRow(ctx, sql).Scan(&v); err != nil {
		t.Fatalf("쿼리 실패(%s): %v", sql, err)
	}
	return v
}

func TestEngineSwitchEquivalenceV1310(t *testing.T) {
	v1310RequireReal(t)
	contDSN := os.Getenv("FLINT_ENGINE_CONTAINER_DSN")
	if contDSN == "" {
		contDSN = "postgresql://flint:flint-dev@127.0.0.1:5434/flint"
	}
	if !v1310Dialable(contDSN) {
		t.Skipf("컨테이너 엔진 미기동(%s) — 전환 전제 미충족", contDSN)
	}
	nativeDSN := testengine.ResolveDSN()
	if v1310Dialable(nativeDSN) {
		t.Skip("네이티브 엔진이 이미 기동 — 전환 전제(네이티브 정지) 위반")
	}
	dataDir := os.Getenv("FLINT_ENGINE_CONTAINER_DATA")
	if dataDir == "" {
		t.Skip("FLINT_ENGINE_CONTAINER_DATA 미설정 — 컨테이너 데이터 디렉터리 경로 필요")
	}
	if fi, err := os.Stat(dataDir); err != nil || !fi.IsDir() {
		t.Skipf("컨테이너 데이터 디렉터리 접근 불가(%s): %v", dataDir, err)
	}
	ctr := os.Getenv("FLINT_ENGINE_CONTAINER_NAME")
	if ctr == "" {
		t.Skip("FLINT_ENGINE_CONTAINER_NAME 미설정 — 정지·재기동 대상 컨테이너 필요")
	}
	if home := os.Getenv("FLINT_HOME"); home == "" {
		t.Skip("FLINT_HOME 미설정 — 전환 데이터 수신 경로 필요")
	}

	marker := "v1310-switch-" + time.Now().UTC().Format("20060102T150405")
	ctx := context.Background()
	pool, err := db.Connect(ctx, v1310SimpleDSN(contDSN))
	if err != nil {
		t.Fatalf("컨테이너 접속 실패: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO meta(key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		"v1310_switch_marker", marker); err != nil {
		pool.Close()
		t.Fatalf("마커 기록 실패: %v", err)
	}
	var beforeDepth string
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM `+db.DoltLogTable).Scan(&beforeDepth); err != nil {
		pool.Close()
		t.Fatalf("전환 전 dolt_log 깊이 조회 실패: %v", err)
	}
	pool.Close()

	engine.SetContainerService(engine.ContainerService{
		Stop:  func() error { return v1310Docker(t, "stop", ctr) },
		Start: func() error { return v1310Docker(t, "start", ctr) },
	})
	t.Cleanup(func() {
		engine.SetContainerService(engine.ContainerService{})
		_ = v1310Docker(t, "start", ctr)
		_ = engine.SetMode(engine.ModeManagedContainer)
	})
	engine.SetNativeService(engine.NewNativeService())
	t.Cleanup(func() { engine.SetNativeService(engine.NativeService{}) })

	if err := engine.ContainerToNative(ctx); err != nil {
		if errors.Is(err, engine.ErrNotWired) {
			t.Skipf("전환 내부 seam 미와이어 — 프로덕션 wiring 대기: %v", err)
		}
		t.Fatalf("ContainerToNative 실패: %v", err)
	}

	gotMarker := v1310Scalar(t, nativeDSN,
		`SELECT value FROM meta WHERE key = 'v1310_switch_marker'`)
	if gotMarker != marker {
		t.Fatalf("마커 불일치 — 컨테이너 %q, 네이티브 %q", marker, gotMarker)
	}
	gotDepth := v1310Scalar(t, nativeDSN,
		`SELECT count(*)::text FROM `+db.DoltLogTable)
	if gotDepth != beforeDepth {
		t.Fatalf("dolt_log 깊이 불일치 — 컨테이너 %s, 네이티브 %s", beforeDepth, gotDepth)
	}
}

func TestEngineUpgradeRoundTripV1310(t *testing.T) {
	v1310RequireReal(t)
	to := os.Getenv("FLINT_ENGINE_UPGRADE_TO")
	if to == "" {
		t.Skip("FLINT_ENGINE_UPGRADE_TO 미설정 — 업그레이드 대상 버전 필요")
	}
	from := os.Getenv("FLINT_ENGINE_UPGRADE_FROM")
	if from == "" {
		from = engine.NativePinVersion
	}
	if from == to {
		t.Skipf("from==to(%s) — 왕복 검증 불가", from)
	}
	home := os.Getenv("FLINT_HOME")
	if home == "" {
		t.Skip("FLINT_HOME 미설정 — 엔진 설치 루트 필요")
	}
	if _, err := os.Stat(filepath.Join(home, "engine", from, "doltgres")); err != nil {
		t.Skipf("구버전 %s 미설치: %v", from, err)
	}
	nativeDSN := testengine.ResolveDSN()
	if !v1310Dialable(nativeDSN) {
		t.Skip("네이티브 엔진 미기동 — 업그레이드 왕복 전제 미충족")
	}
	engine.SetNativeService(engine.NewNativeService())
	t.Cleanup(func() { engine.SetNativeService(engine.NativeService{}) })

	ctx := context.Background()
	if err := engine.UpgradeRoundTrip(ctx, from, to); err != nil {
		if _, perr := engine.PinnedHash(runtime.GOOS, runtime.GOARCH); perr != nil {
			t.Skipf("플랫폼 핀 부재로 다운로드 불가(%s/%s): %v", runtime.GOOS, runtime.GOARCH, err)
		}
		t.Fatalf("UpgradeRoundTrip(%s→%s) 실패: %v", from, to, err)
	}
	link, err := os.Readlink(filepath.Join(home, "engine", "current"))
	if err != nil {
		t.Fatalf("current 심링크 확인 실패: %v", err)
	}
	if link != from {
		t.Fatalf("current=%q, want 구버전 원복 %q", link, from)
	}
	v, err := engine.Version()
	if err != nil {
		t.Fatalf("왕복 후 버전 확인 실패: %v", err)
	}
	if v != from {
		t.Fatalf("engine.Version=%q, want 원복 %q", v, from)
	}
	if !v1310Dialable(nativeDSN) {
		t.Fatal("왕복 후 네이티브 접속 불가")
	}
}

func TestHookExit2PreservedV1310(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	bin := filepath.Join(root, "bin", "flint-v1310")
	build := exec.Command("go", "build", "-o", bin, "./cmd/flint")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build 실패: %v; out=%s", err, b)
	}
	cmd := exec.Command(bin, "session-start", "--session", "e2e-exit2-v1310")
	cmd.Env = append(os.Environ(),
		"FLINT_CONFIG_DIR="+t.TempDir(),
		"FLINT_DSN=postgresql://flint:pw@127.0.0.1:1/flint")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("실행 실패: %v; out=%s", err, out)
		}
		code = ee.ExitCode()
	}
	if code != 2 {
		t.Fatalf("session-start exit=%d, want 2(server_down): %s", code, out)
	}
	if !strings.Contains(string(out), "server_down") {
		t.Fatalf("출력에 server_down 부재: %s", out)
	}
}
