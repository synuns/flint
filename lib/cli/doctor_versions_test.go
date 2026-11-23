package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synuns/flint/lib/db"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func backupAgeQueryFn(lastISO string, fail bool) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_backup") {
			if fail {
				return nil, context.DeadlineExceeded
			}
			if lastISO == "" {
				return nil, nil
			}
			return [][]any{{"last_backup", lastISO}}, nil
		}
		return nil, nil
	}
}

func TestBackupAgeFresh(t *testing.T) {
	fresh := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn(fresh, false)})
	if c.Status != "ok" {
		t.Errorf("1시간 경과 = %s, want ok (%s)", c.Status, c.Detail)
	}
}

func TestBackupAgeStale(t *testing.T) {
	stale := time.Now().UTC().Add(-73 * time.Hour).Format(time.RFC3339)
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn(stale, false)})
	if c.Status != "warn" {
		t.Errorf("73시간 경과 = %s, want warn", c.Status)
	}
}

func TestBackupAgeMissing(t *testing.T) {
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn("", false)})
	if c.Status != "warn" {
		t.Errorf("미설정 = %s, want warn", c.Status)
	}
	c = checkBackupAge(context.Background(), nil)
	if c.Status != "warn" {
		t.Errorf("nil store = %s, want warn", c.Status)
	}
}

func TestBackupAgeCorruptTS(t *testing.T) {
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn("yesterday-ish", false)})
	if c.Status != "warn" {
		t.Errorf("파손 값 = %s, want warn", c.Status)
	}
}

func TestBackupAgeJustUnderThreshold(t *testing.T) {
	ts := time.Now().UTC().Add(-71*time.Hour - 59*time.Minute).Format(time.RFC3339)
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn(ts, false)})
	if c.Status != "ok" {
		t.Errorf("71h59m 경과 = %s, want ok (%s)", c.Status, c.Detail)
	}
}

func TestDoctorSubsetBackupAge(t *testing.T) {
	fresh := time.Now().UTC().Format(time.RFC3339)
	var out bytes.Buffer
	code := runDoctor(context.Background(), asofFakeOrReal(t, &fakeStore{queryFn: backupAgeQueryFn(fresh, false)}), []string{"--check", "backup-age"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d (출력 %s)", code, out.String())
	}
	var d struct {
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Checks) != 1 || d.Checks[0].Name != "backup-age" {
		t.Fatalf("서브셋 결과 = %+v, want backup-age 1건", d.Checks)
	}
}

func TestDoctorSubsetUnknownStill3(t *testing.T) {
	var out bytes.Buffer
	code := runDoctor(context.Background(), nil, []string{"--check", "no-such"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("무효 이름 exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "unknown check") {
		t.Errorf("안내 문구 누락: %q", out.String())
	}
}

func TestConflictsZeroOk(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}})
	if c.Status != "ok" {
		t.Errorf("0그룹 = %s, want ok", c.Status)
	}
}

func TestConflictsWarn(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{"topic-a", []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}}}, nil
	}})
	if c.Status != "warn" {
		t.Errorf("모순 1그룹 = %s, want warn", c.Status)
	}
}

func TestConflictsDupOnlyOk(t *testing.T) {
	same := "flint 백업은 매일 자동이다"
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{"backup-policy", []string{same, same}},
			{"port-config", []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}, nil
	}})
	if c.Status != "warn" {
		t.Errorf("혼합 그룹 = %s, want warn", c.Status)
	}
	c2 := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{"backup-policy", []string{same, same}}}, nil
	}})
	if c2.Status != "ok" {
		t.Errorf("순수 중복 그룹 = %s, want ok(T2 출력과 동치)", c2.Status)
	}
}

func TestDoctorCheckConflictsSubset(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out bytes.Buffer
	code := runDoctor(context.Background(), st, []string{"--check", "conflicts"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out.String())
	}
	if !strings.Contains(out.String(), `"name":"conflicts"`) {
		t.Errorf("출력에 conflicts 검사 없음: %s", out.String())
	}
}

func TestConflictsLimitReachedNotesOverflow(t *testing.T) {
	groups := make([]db.ConflictGroup, doctorConflictLimit)
	for i := range groups {
		groups[i] = db.ConflictGroup{
			Topic: fmt.Sprintf("topic-%d", i),
			IDs:   []int{2*i + 1, 2*i + 2},

			Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"},
		}
	}
	inner := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	s := &seamConflictStore{fakeStore: inner, groups: groups}
	c := checkConflicts(context.Background(), s)
	if c.Status != "warn" {
		t.Fatalf("status = %s, want warn", c.Status)
	}
	if !strings.Contains(c.Detail, "group query limit") {
		t.Errorf("상한 도달 문구 없음: %q", c.Detail)
	}
	if len(s.calls) != 1 || s.calls[0] != doctorConflictLimit {
		t.Errorf("조회 limit = %v, want [%d]", s.calls, doctorConflictLimit)
	}
}

func TestZeroNearWarn(t *testing.T) {

	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":20,"read_zero_near":8}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "warn" {
		t.Errorf("40%% = %s, want warn", c.Status)
	}
}

func TestZeroNearSampleFloorOk(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":5,"read_zero_near":3}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "ok" {
		t.Errorf("표본 5 = %s, want ok", c.Status)
	}
}

func TestDoctorMentionsExpire(t *testing.T) {
	old := time.Now().Add(-10 * 24 * time.Hour)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "pending") {
			t.Errorf("예상 밖 조회: %s", sql)
		}
		return [][]any{{old}}, nil
	}}
	c := checkPendingAge(context.Background(), f)
	if c.Status != "warn" {
		t.Fatalf("status=%q, want warn (detail %q)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "expire-pending") {
		t.Errorf("detail에 만료 명령 안내 없음: %q", c.Detail)
	}

	fresh := time.Now().Add(-1 * 24 * time.Hour)
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{fresh}}, nil
	}}
	c2 := checkPendingAge(context.Background(), f2)
	if c2.Status != "ok" {
		t.Errorf("신선 pending status=%q, want ok (detail %q)", c2.Status, c2.Detail)
	}

	f3 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	c3 := checkPendingAge(context.Background(), f3)
	if c3.Status != "ok" {
		t.Errorf("pending 없음 status=%q, want ok (detail %q)", c3.Status, c3.Detail)
	}
}

func thresholdsQueryFn(kv [][]any, fallback func(string, []any) ([][]any, error)) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return kv, nil
		}
		return fallback(sql, args)
	}
}

func TestDoctorBackupAgeThresholdOverride(t *testing.T) {
	twoHourOld := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"backup_age_warn_h", "1"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "last_backup") {
				return [][]any{{"last_backup", twoHourOld}}, nil
			}
			return nil, nil
		})}
	c := checkBackupAge(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 1h 초과 2h 경과 = %s, want warn(기본 72h 잔존 의심): %q", c.Status, c.Detail)
	}
}

func TestDoctorZeroNearWarnPctOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_warn_pct", "5"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {

				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 5%% 초과 10%% = %s, want warn(기본 30 잔존 의심): %q", c.Status, c.Detail)
	}
}

func TestDoctorZeroNearMinReadOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_min_read", "100"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {

				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "ok" || !strings.Contains(c.Detail, "insufficient samples") {
		t.Errorf("오버라이드 표본 하한 100 = %s(%q), want ok+표본 부족", c.Status, c.Detail)
	}
}

func TestDoctorPendingAgeThresholdOverride(t *testing.T) {
	twoDayOld := time.Now().Add(-2 * 24 * time.Hour)
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"pending_age_days", "1"}},
		func(sql string, args []any) ([][]any, error) {
			return [][]any{{twoDayOld}}, nil
		})}
	c := checkPendingAge(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 1일 초과 2일 경과 = %s, want warn(기본 7일 잔존 의심): %q", c.Status, c.Detail)
	}
}

func withComposePs(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	old := composePsFn
	composePsFn = fn
	t.Cleanup(func() { composePsFn = old })
}

func doctorPortCheck(t *testing.T, out string) map[string]any {
	t.Helper()
	var m struct {
		Checks []map[string]any `json:"checks"`
	}
	body := strings.TrimSpace(out)
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if len(m.Checks) == 1 {
		return m.Checks[0]
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(body), &single); err != nil || single["name"] != "port" {
		t.Fatalf("port 검사 행 아님: %q", out)
	}
	return single
}

func TestPortPass(t *testing.T) {
	line := `{"Name":"flint-doltgres-1","State":"running",` +
		`"Labels":"com.docker.compose.project=flint,com.docker.compose.service=doltgres",` +
		`"Ports":"127.0.0.1:5434->5432/tcp"}`
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(line + "\n" + `{"Name":"flint-cache-1","State":"running","Publishers":[]}` + "\n"), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("runDoctor exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("runDoctor status=%v, want ok: %q", pc["status"], out)
	}
}

func TestPortForeign(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"branding-web-1","State":"running",` +
			`"Labels":"com.docker.compose.project=branding",` +
			`"Publishers":[{"TargetPort":5434,"PublishedPort":5434,"Protocol":"tcp"}]}`), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("runDoctor exit=%d, want 0(unknown은 경고 아님 — 출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] != "skip" {
		t.Fatalf("status=%v, want skip(라벨 비일치 unknown): %q", c["status"], out)
	}
	if !strings.Contains(fmt.Sprint(c["detail"]), "branding-web-1") || !strings.Contains(fmt.Sprint(c["detail"]), "unknown") {
		t.Errorf("점유자·unknown 없음: %v", c["detail"])
	}
}

func TestPortDockerMissing(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("exec: docker: executable file not found")
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("skip가 exit에 영향: exit=%d (출력 %q)", code, out)
	}
	if c := doctorPortCheck(t, out); c["status"] != "skip" {
		t.Errorf("status=%v, want skip: %q", c["status"], out)
	}
	if strings.Contains(out, `"fail"`) {
		t.Errorf("skip인데 fail 존재: %q", out)
	}
}

func TestPortStopped(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"flint-doltgres-1","State":"exited","Labels":"com.docker.compose.project=flint","Publishers":null}`), nil
	})

	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] != "fail" {
		t.Fatalf("status=%v, want fail: %q", c["status"], out)
	}
	if fix, _ := c["fix"].(string); !strings.Contains(fix, "flint setup") {
		t.Errorf("flint setup fix 문구 없음: %q", fix)
	}
}

func TestPortNoFlintContainer(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"other-app-1","State":"running","Publishers":[]}`), nil
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "flint setup") {
		t.Errorf("flint setup 안내 없음: %q", out)
	}
}

func TestPortJSONArray(t *testing.T) {
	arr := `[{"Name":"flint-doltgres-1","State":"running",` +
		`"Labels":"com.docker.compose.project=flint",` +
		`"Publishers":[{"PublishedPort":5434}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(arr), nil })
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("배열 pass exit=%d, want 0 (출력 %q)", code, out)
	}
	if pc := doctorPortCheck(t, out); pc["status"] != "ok" {
		t.Errorf("배열 status=%v, want ok: %q", pc["status"], out)
	}

	foreign := `[{"Name":"flint-doltgres-1","State":"running","Labels":"com.docker.compose.project=flint","Publishers":[]},` +
		`{"Name":"branding-1","State":"running","Labels":"com.docker.compose.project=branding","Publishers":[{"PublishedPort":5434}]}]`
	withComposePs(t, func(ctx context.Context) ([]byte, error) { return []byte(foreign), nil })
	out2, code2 := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code2 != 0 {
		t.Fatalf("배열 점유 exit=%d, want 0(unknown은 경고 아님 — 출력 %q)", code2, out2)
	}
	if !strings.Contains(out2, "branding-1") {
		t.Errorf("배열 점유자 미보고: %q", out2)
	}
}

func TestDoctorChecksIncludesPort(t *testing.T) {
	got := DoctorChecks()
	if len(got) == 0 || got[len(got)-1] != "port" {
		t.Errorf("DoctorChecks 마지막에 port 없음: %v", got)
	}
}

func TestPortFlintSubstringNotPrefix(t *testing.T) {
	if isFlintContainer("conflint-web-1") {
		t.Errorf("isFlintContainer(conflint-web-1) = true, want false(접두 아님)")
	}
	if !isFlintContainer("flint-doltgres-1") {
		t.Errorf("isFlintContainer(flint-doltgres-1) = false, want true")
	}

	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Name":"conflint-web-1","State":"running",` +
			`"Publishers":[{"PublishedPort":5434}]}`), nil
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("라벨 없는 점유 exit=%d, want 0(unknown은 경고 아님 — 출력 %q)", code, out)
	}
	c := doctorPortCheck(t, out)
	if c["status"] == "ok" {
		t.Fatalf("라벨 없는 conflint 컨테이너를 ok 판정 — 이름 접두사 오탐: %q", out)
	}
	if !strings.Contains(fmt.Sprint(c["detail"]), "conflint-web-1") {
		t.Errorf("점유자 이름 없음: %v", c["detail"])
	}
}

func TestPortDockerHangTimeout(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	out, code := runDoctorArgs(t, &fakeStore{}, "--check", "port")
	if code != 0 {
		t.Fatalf("timeout skip가 exit에 영향: exit=%d (출력 %q)", code, out)
	}
	if c := doctorPortCheck(t, out); c["status"] != "skip" {
		t.Errorf("status=%v, want skip(타임아웃은 오류 경로로): %q", c["status"], out)
	}
}

func writeFixture(t *testing.T, hooks map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	p := filepath.Join(dir, ".claude", "settings.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyHookFixAddsFlintHooks(t *testing.T) {
	p := writeFixture(t, map[string]any{
		"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "orca"}}}},
	})
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if backup == "" || !strings.Contains(backup, ".flint-bak-") {
		t.Errorf("백업 경로 이상: %q", backup)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &got)
	hooks := got["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "Stop", "PostCompact"} {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("%s 이벤트 누락", ev)
		}
	}
	s := string(b)
	for _, cmd := range []string{"session-start", "session-end", "compact"} {
		if !strings.Contains(s, "'/x/flint' "+cmd) {
			t.Errorf("%s 명령 누락", cmd)
		}
	}
	if !strings.Contains(s, "orca") {
		t.Error("기존 orca 훅 삭제됨")
	}
}

func TestApplyHookFixPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := []byte(`{"permissions":{"allow":["Bash"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"orca"}]}]},"env":{"FOO":"bar"}}`)
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyHookFix(p, "/x/flint"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("결과 JSON 파싱 실패: %v", err)
	}
	if perm, ok := got["permissions"].(map[string]any); !ok || perm["allow"] == nil {
		t.Errorf("permissions 키 무손실 아님: %v", got["permissions"])
	}
	if env, ok := got["env"].(map[string]any); !ok || env["FOO"] != "bar" {
		t.Errorf("env 키 무손실 아님: %v", got["env"])
	}

	stop, _ := got["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop 배열 길이=%d, want 2", len(stop))
	}
	first, _ := stop[0].(map[string]any)
	inner, _ := first["hooks"].([]any)
	cmd, _ := inner[0].(map[string]any)["command"].(string)
	if cmd != "orca" {
		t.Errorf("Stop[0] 기존 orca 엔트리 아님: %q", cmd)
	}
}

func TestApplyHookFixIdempotent(t *testing.T) {
	p := writeFixture(t, map[string]any{})
	if _, _, err := applyHookFix(p, "/x/flint"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || applied || backup != "" {
		t.Errorf("멱등 위반: applied=%v backup=%q err=%v", applied, backup, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("2회째 파일 변형")
	}
}

func TestApplyHookFixBackupCreated(t *testing.T) {
	p := writeFixture(t, map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "orca"}}}},
	})
	orig, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	bk, rerr := os.ReadFile(backup)
	if rerr != nil {
		t.Fatalf("백업 파일 없음: %v", rerr)
	}
	if string(bk) != string(orig) {
		t.Error("백업이 원본과 다름")
	}
}

func TestApplyHookFixBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err == nil || applied {
		t.Fatalf("파손 JSON인데 applied=%v err=%v", applied, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("파손 JSON 파일을 변형함")
	}
	if backup != "" {
		t.Errorf("파손 JSON에 백업 생성: %q", backup)
	}
	matches, _ := filepath.Glob(p + ".flint-bak-*")
	if len(matches) != 0 {
		t.Errorf("백업 파일 잔존: %v", matches)
	}
}

func TestDoctorFixReportFormat(t *testing.T) {
	r := doctorFixReport([]doctorCheck{{Name: "hook-file", Status: "ok"}})
	var m map[string]string
	if err := json.Unmarshal([]byte(r), &m); err != nil {
		t.Fatalf("JSON 아님: %q", r)
	}
	if m["status"] != "fixed" {
		t.Errorf("status=%q, want fixed (%q)", m["status"], r)
	}
	if m["hook-file"] != "ok" {
		t.Errorf("hook-file=%q, want ok (%q)", m["hook-file"], r)
	}

	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if strings.Contains(out, `"fixed"`) {
		t.Errorf("무 --fix인데 fixed 리포트 출력: %q", out)
	}
}

func TestDoctorFixFlagAppliesHookFix(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정")
	})
	dir := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", dir)
	setHarnessEnvs(t)
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"orca"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{queryFn: freshDoctorFn()}
	oldBin := flintBinPathFn
	flintBinPathFn = func() string { return "/x/flint" }
	t.Cleanup(func() { flintBinPathFn = oldBin })
	out, code := runDoctorArgs(t, f, "--fix")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m["status"] != "fixed" {
		t.Errorf("status=%q, want fixed (출력 %q)", m["status"], out)
	}
	if m["hook-file"] != "ok" {
		t.Errorf("hook-file=%q, want ok (출력 %q)", m["hook-file"], out)
	}
	body, _ := os.ReadFile(p)
	s := string(body)
	if !strings.Contains(s, "orca") || !strings.Contains(s, " session-start ") {
		t.Errorf("훅 병합 결과 이상: %q", s)
	}
	matches, _ := filepath.Glob(p + ".flint-bak-*")
	if len(matches) != 1 {
		t.Errorf("백업 1개 기대: %v", matches)
	}
}

func TestApplyHookFixCreatesMissingSettings(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude", "settings.json")
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if backup != "" {
		t.Errorf("신규 생성에 백업 불필요: %q", backup)
	}
	b, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatalf("파일 미생성: %v", rerr)
	}
	var got map[string]any
	if uerr := json.Unmarshal(b, &got); uerr != nil {
		t.Fatalf("생성 파일 파싱 실패: %v", uerr)
	}
	if _, ok := got["hooks"]; !ok {
		t.Error("hooks 키 누락")
	}
	if !strings.Contains(string(b), "/x/flint") {
		t.Error("flint 훅 미포함")
	}
}

func TestApplyHookFixReadErrorNotMisreadAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 시맨틱 미지원")
	}
	if os.Geteuid() == 0 {
		t.Skip("루트는 권한 비트 무시 — 읽기 오류 재현 불가")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := []byte(`{"hooks":{}}`)
	if err := os.WriteFile(p, orig, 0o000); err != nil {
		t.Fatalf("쓰기 실패: %v", err)
	}
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err == nil {
		t.Fatal("권한 오류가 조용히 무시됨 — 신규 생성 오분류 위험")
	}
	if applied {
		t.Error("읽기 불가인데 applied=true")
	}
	if backup != "" {
		t.Errorf("읽기 실패 경로에 백업 생성됨: %q", backup)
	}

	_ = os.Chmod(p, 0o644)
	got, rerr := os.ReadFile(p)
	if rerr != nil || !bytes.Equal(got, orig) {
		t.Errorf("원본 무변경 위반: %q (err=%v)", got, rerr)
	}
}
