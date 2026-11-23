package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func withClassifyGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := classifyGOOS
	classifyGOOS = goos
	t.Cleanup(func() { classifyGOOS = prev })
}

func withClassifyLookPath(t *testing.T, exists bool) {
	t.Helper()
	prev := classifyLookPathFn
	classifyLookPathFn = func(name string) (string, error) {
		if exists {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { classifyLookPathFn = prev })
}

func TestClassifyEngineErrorTable(t *testing.T) {
	withClassifyGOOS(t, "darwin")
	withClassifyLookPath(t, true)
	cases := []struct {
		name string
		err  error
		kind string
	}{
		{"daemon_down", errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"), "daemon_down"},
		{"permission_denied", errors.New("permission denied while trying to connect to the Docker daemon socket"), "permission_denied"},
		{"binary_missing", errors.New("exec: docker: executable file not found in $PATH"), "binary_missing"},
		{"binary_missing_wrapped", fmt.Errorf("up: %w: ", exec.ErrNotFound), "binary_missing"},
		{"compose_v1", errors.New("docker: 'compose' is not a docker command."), "compose_v1"},
		{"rate_limited_toomanyrequests", errors.New("toomanyrequests: You have reached your unauthenticated pull rate limit."), "rate_limited"},
		{"rate_limited_phrase", errors.New("error pulling image: you have hit your rate limit"), "rate_limited"},
		{"tls_error_x509", errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"), "tls_error"},
		{"tls_error_phrase", errors.New("Get \"https://registry.example/v2/: remote error: tls: bad certificate"), "tls_error"},
		{"health_check_timeout_unhealthy", errors.New("container flint-engine-1 is unhealthy"), "health_check_timeout"},
		{"health_check_timeout_phrase", errors.New("dependency failed to start: container flint-engine-1 is unhealthy (health check failed)"), "health_check_timeout"},
		{"lightweight_runtime_colima", errors.New("colima daemon is not running"), "lightweight_runtime"},
		{"lightweight_runtime_orbstack", errors.New("orbstack docker socket unavailable"), "lightweight_runtime"},
		{"lightweight_runtime_podman", errors.New("podman machine not started"), "lightweight_runtime"},
		{"unclassified", errors.New("some future docker failure"), "unclassified"},
	}
	for _, tc := range cases {
		kind, remedy := ClassifyEngineError(tc.err)
		if kind != tc.kind {
			t.Fatalf("%s: kind=%q, want %q (remedy=%q)", tc.name, kind, tc.kind, remedy)
		}
		if strings.TrimSpace(remedy) == "" {
			t.Fatalf("%s: 처방이 비어 있음", tc.name)
		}
	}
}

func TestClassifyEngineErrorUnclassifiedKeepsCause(t *testing.T) {
	_, remedy := ClassifyEngineError(errors.New("dial tcp: weird blast radius"))
	if !strings.Contains(remedy, "weird blast radius") {
		t.Fatalf("미분류는 원인 문구를 유지해야 한다: %q", remedy)
	}
}

func TestClassifyEngineErrorComposeV1RequiresFallbackBinary(t *testing.T) {
	withClassifyLookPath(t, false)
	kind, _ := ClassifyEngineError(errors.New("docker: 'compose' is not a docker command."))
	if kind != "unclassified" {
		t.Fatalf("docker-compose 부재 시 compose_v1이 아니어야 한다: %q", kind)
	}
}

func TestClassifyEngineErrorComposeV1RemedyNoFallbackClaim(t *testing.T) {
	withClassifyLookPath(t, true)
	_, remedy := ClassifyEngineError(errors.New("docker: 'compose' is not a docker command."))
	if strings.Contains(strings.ToLower(remedy), "falls back") {
		t.Fatalf("compose_v1 처방이 미구현 폴백을 주장함: %q", remedy)
	}
	if !strings.Contains(remedy, "compose plugin") {
		t.Fatalf("compose_v1 처방에 플러그인 설치 안내 부재: %q", remedy)
	}
}

func TestClassifyEngineErrorRemedyPerOS(t *testing.T) {
	withClassifyGOOS(t, "darwin")
	_, darwin := ClassifyEngineError(errors.New("Cannot connect to the Docker daemon"))
	if !strings.Contains(darwin, "open -a Docker") {
		t.Fatalf("darwin 데몬 처방에 open -a Docker 없음: %q", darwin)
	}
	if !strings.Contains(darwin, "colima start") || !strings.Contains(darwin, "OrbStack") {
		t.Fatalf("darwin 데몬 처방에 경량 런타임 문구 없음: %q", darwin)
	}
	withClassifyGOOS(t, "linux")
	_, linux := ClassifyEngineError(errors.New("Cannot connect to the Docker daemon"))
	if !strings.Contains(linux, "systemctl start docker") {
		t.Fatalf("linux 데몬 처방 문구 없음: %q", linux)
	}
	if strings.Contains(linux, "open -a Docker") {
		t.Fatalf("linux 처방에 darwin 문구 혼입: %q", linux)
	}
	withClassifyGOOS(t, "linux")
	_, permLinux := ClassifyEngineError(errors.New("permission denied"))
	if !strings.Contains(permLinux, "docker") {
		t.Fatalf("linux 권한 처방 문구 없음: %q", permLinux)
	}
	withClassifyGOOS(t, "darwin")
	_, binDarwin := ClassifyEngineError(errors.New("exec: docker: executable file not found in $PATH"))
	if !strings.Contains(binDarwin, "install") {
		t.Fatalf("darwin 바이너리 부재 처방에 install 없음: %q", binDarwin)
	}
}

type firstRunStore struct {
	wire.HistoryStubs

	mu          sync.Mutex
	pingErr     error
	pings       int
	migrateErr  error
	migrates    int
	pingSucceed int
}

func (s *firstRunStore) Ping(ctx context.Context) error {
	s.mu.Lock()
	s.pings++
	n := s.pings
	s.mu.Unlock()
	if s.pingSucceed > 0 && n >= s.pingSucceed {
		return nil
	}
	return s.pingErr
}

func (s *firstRunStore) Migrate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrates++
	return s.migrateErr
}

func (s *firstRunStore) Close() {}

func (s *firstRunStore) Commit(ctx context.Context, msg string) (string, error) { return "", nil }

func (s *firstRunStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}

func (s *firstRunStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}

func (s *firstRunStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}

func (s *firstRunStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return nil, nil
}

func withFirstRunSeams(t *testing.T, upErr error, store func() (Store, error)) (*composeRecorder, *bytes.Buffer) {
	t.Helper()
	rec := withFakeCompose(t)
	rec.upErr = upErr
	prevNew, prevErr := newStore, setupStderr
	newStore = func(ctx context.Context, dsn string) (Store, error) { return store() }
	var buf bytes.Buffer
	setupStderr = &buf
	t.Cleanup(func() { newStore, setupStderr = prevNew, prevErr })
	return rec, &buf
}

func TestEnsureFirstRunSuccessOneShot(t *testing.T) {
	st := &firstRunStore{}
	rec, errs := withFirstRunSeams(t, nil, func() (Store, error) { return st, nil })
	code := EnsureFirstRun(context.Background(), nil, FirstRunConfig{PingRetryCount: 5, PingRetryInterval: time.Millisecond})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; errs=%s", code, errs.String())
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose up=%d회, want 1", rec.upCalls)
	}
	if st.migrates != 1 {
		t.Fatalf("migrate=%d회, want 1(같은 실행 완결)", st.migrates)
	}
}

func TestEnsureFirstRunPingRetriesThenRecreate(t *testing.T) {
	st := &firstRunStore{pingErr: errors.New("connection refused"), pingSucceed: 3}
	_, errs := withFirstRunSeams(t, nil, func() (Store, error) { return st, nil })
	code := EnsureFirstRun(context.Background(), nil, FirstRunConfig{PingRetryCount: 5, PingRetryInterval: time.Millisecond})
	if code != 0 {
		t.Fatalf("exit=%d, want 0(재시도 내 성공); errs=%s", code, errs.String())
	}
	if st.migrates != 1 {
		t.Fatalf("migrate=%d회, want 1", st.migrates)
	}
}

func TestEnsureFirstRunPingBudgetExhausted(t *testing.T) {
	st := &firstRunStore{pingErr: errors.New("connection refused")}
	_, errs := withFirstRunSeams(t, nil, func() (Store, error) { return nil, errors.New("server down") })
	code := EnsureFirstRun(context.Background(), st, FirstRunConfig{PingRetryCount: 3, PingRetryInterval: time.Millisecond})
	if code != 1 {
		t.Fatalf("exit=%d, want 1(예산 소진); errs=%s", code, errs.String())
	}
	if st.pings != 3 {
		t.Fatalf("ping 시도=%d회, want 3(예산과 일치)", st.pings)
	}
	if st.migrates != 0 {
		t.Fatalf("ping 실패로 migrate=%d회 발생", st.migrates)
	}
	if !strings.Contains(errs.String(), "ping") {
		t.Fatalf("예산 소진 안내 없음: %q", errs.String())
	}
}

func TestEnsureFirstRunDefaultBudget(t *testing.T) {
	if defaultPingRetryCount != 30 {
		t.Fatalf("ping_retry_count 기본값=%d, want 30", defaultPingRetryCount)
	}
	if defaultPingRetryInterval != 200*time.Millisecond {
		t.Fatalf("ping_retry_interval_ms 기본값=%v, want 200ms", defaultPingRetryInterval)
	}
	n, d := FirstRunConfig{}.normalized()
	if n != defaultPingRetryCount || d != defaultPingRetryInterval {
		t.Fatalf("zero config 정규화=%d/%v, want %d/%v", n, d, defaultPingRetryCount, defaultPingRetryInterval)
	}
}

func TestEnsureFirstRunComposeFailClassified(t *testing.T) {
	withClassifyGOOS(t, "darwin")
	_, errs := withFirstRunSeams(t, errors.New("Cannot connect to the Docker daemon"), nil)
	st := &firstRunStore{}
	code := EnsureFirstRun(context.Background(), st, FirstRunConfig{PingRetryCount: 2, PingRetryInterval: time.Millisecond})
	if code != 1 {
		t.Fatalf("exit=%d, want 1(기동 실패)", code)
	}
	if st.pings != 0 {
		t.Fatalf("기동 실패 후 ping=%d회 발생", st.pings)
	}
	out := errs.String()
	if !strings.Contains(out, "daemon_down") {
		t.Fatalf("분류 kind 미출력: %q", out)
	}
	if !strings.Contains(out, "open -a Docker") {
		t.Fatalf("OS별 처방 미출력: %q", out)
	}
}

func TestEnsureFirstRunMigrateFail(t *testing.T) {
	st := &firstRunStore{migrateErr: errors.New("boom")}
	_, errs := withFirstRunSeams(t, nil, func() (Store, error) { return st, nil })
	code := EnsureFirstRun(context.Background(), nil, FirstRunConfig{PingRetryCount: 2, PingRetryInterval: time.Millisecond})
	if code != 1 {
		t.Fatalf("exit=%d, want 1(migrate 실패); errs=%s", code, errs.String())
	}
	if st.migrates != 1 {
		t.Fatalf("migrate=%d회, want 1", st.migrates)
	}
}

type psCall struct {
	args []string
}

func withDockerPS(t *testing.T, out string, err error) *[]psCall {
	t.Helper()
	var calls []psCall
	prev := dockerPSFn
	dockerPSFn = func(ctx context.Context, args ...string) (string, error) {
		calls = append(calls, psCall{args: args})
		return out, err
	}
	t.Cleanup(func() { dockerPSFn = prev })
	return &calls
}

func withPortDial(t *testing.T, inUse bool) {
	t.Helper()
	prev := setupPortDialFn
	setupPortDialFn = func(addr string) bool { return inUse }
	t.Cleanup(func() { setupPortDialFn = prev })
}

func TestExistingInstallAdoptsFlintLabel(t *testing.T) {
	withPortDial(t, true)
	calls := withDockerPS(t, "0.0.0.0:5437->5432/tcp\n", nil)
	adopt, port, stop := ExistingInstall(context.Background())
	if !adopt || stop {
		t.Fatalf("adopt=%v stop=%v, want true/false", adopt, stop)
	}
	if port != 5437 {
		t.Fatalf("port=%d, want 5437(라벨 컨테이너 게시 포트)", port)
	}
	if len(*calls) == 0 {
		t.Fatal("docker ps 미호출")
	}
	joined := strings.Join((*calls)[0].args, " ")
	if !strings.Contains(joined, "label=com.docker.compose.project=flint") {
		t.Fatalf("라벨 정확 일치 필터 아님: %q", joined)
	}
}

func TestExistingInstallStopsOnNonFlintPreemptor(t *testing.T) {
	withPortDial(t, true)
	withDockerPS(t, "", nil)
	adopt, port, stop := ExistingInstall(context.Background())
	if adopt || !stop {
		t.Fatalf("adopt=%v stop=%v, want false/true(비flint 선점)", adopt, stop)
	}
	if port != defaultEnginePort {
		t.Fatalf("port=%d, want %d", port, defaultEnginePort)
	}
	remedy := ExistingInstallRemedy(port)
	if !strings.Contains(remedy, "RUNBOOK") || !strings.Contains(remedy, "§11") {
		t.Fatalf("정지 처방이 RUNBOOK §11을 지목하지 않음: %q", remedy)
	}
}

func TestExistingInstallNewWhenIdle(t *testing.T) {
	withPortDial(t, false)
	withDockerPS(t, "", nil)
	adopt, port, stop := ExistingInstall(context.Background())
	if adopt || stop || port != 0 {
		t.Fatalf("adopt=%v port=%d stop=%v, want false/0/false(신규)", adopt, port, stop)
	}
}

func TestExistingInstallDockerUnavailableIsNew(t *testing.T) {
	withPortDial(t, false)
	withDockerPS(t, "", errors.New("docker: executable file not found"))
	adopt, _, stop := ExistingInstall(context.Background())
	if adopt || stop {
		t.Fatalf("docker 부재 시 adopt=%v stop=%v, want false/false(신규 절차 진행)", adopt, stop)
	}
}
