package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeDump 은 dumpFn seam을 교체해 실 DB 없이 덤프 기록을 흉내 낸다.
// SUPERLITE_REAL=1이면 실 Dump가 사용된다(slow 대상).
func fakeDump(t *testing.T, calls *int) {
	t.Helper()
	orig := dumpFn
	dumpFn = func(ctx context.Context, dsn string, w io.Writer) error {
		*calls++
		if _, err := io.WriteString(w, "-- fake dump\n"); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() { dumpFn = orig })
}

func realDumpEnv(t *testing.T) {
	t.Helper()
	if !realDumpEnabled() {
		t.Skip("SUPERLITE_REAL 미설정 — fake dump로 충분")
	}
}

// Review Focus(numeric): 23h59m no / 24h01m yes 경계.
func TestShouldBackupBoundary(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last string
		want bool
	}{
		{"23h59m 경과", now.Add(-23*time.Hour - 59*time.Minute).Format(time.RFC3339), false},
		{"24h 정각", now.Add(-24 * time.Hour).Format(time.RFC3339), true},
		{"24h01m 경과", now.Add(-24*time.Hour - time.Minute).Format(time.RFC3339), true},
		{"빈 값(첫 백업)", "", true},
		{"잘못된 형식", "not-a-time", true},
	}
	for _, c := range cases {
		if got := ShouldBackup(c.last, now); got != c.want {
			t.Errorf("%s: ShouldBackup(%q) = %v, want %v", c.name, c.last, got, c.want)
		}
	}
}

// Review Focus(state): 경과 시 덤프 기록+last_backup 메타 갱신.
func TestRunIfNeededBacksUp(t *testing.T) {
	calls := 0
	fakeDump(t, &calls)

	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	var gotKey, gotVal string
	a := &Auto{DSN: "fake-dsn"}
	var buf bytes.Buffer

	if err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return old, nil },
		func(k, v string) error { gotKey, gotVal = k, v; return nil },
		&buf); err != nil {
		t.Fatalf("RunIfNeeded error: %v", err)
	}
	if calls != 1 {
		t.Errorf("dump 호출 수 = %d, want 1", calls)
	}
	if !strings.Contains(buf.String(), "-- fake dump") {
		t.Errorf("dump w에 기록 안 됨: %q", buf.String())
	}
	if gotKey != "last_backup" {
		t.Errorf("setKV 키 = %q, want last_backup", gotKey)
	}
	if _, err := time.Parse(time.RFC3339, gotVal); err != nil {
		t.Errorf("setKV 값이 RFC3339 아님: %q", gotVal)
	}
}

// 비경과 시 no-op — 덤프도 메타 갱신도 일어나지 않는다.
func TestRunIfNeededSkipsRecent(t *testing.T) {
	calls := 0
	fakeDump(t, &calls)

	recent := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	setCalled := false
	a := &Auto{DSN: "fake-dsn"}

	if err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return recent, nil },
		func(k, v string) error { setCalled = true; return nil },
		io.Discard); err != nil {
		t.Fatalf("RunIfNeeded error: %v", err)
	}
	if calls != 0 || setCalled {
		t.Errorf("no-op 위반: dump %d회, setKV %v", calls, setCalled)
	}
}

// Review Focus(state): setKV 실패 시 에러 반환 + 메타 미기록 → 다음 재시도.
func TestRunIfNeededSetKVFailureRetries(t *testing.T) {
	calls := 0
	fakeDump(t, &calls)

	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	recorded := false
	a := &Auto{DSN: "fake-dsn"}

	err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return old, nil },
		func(k, v string) error { recorded = false; return errors.New("meta down") },
		io.Discard)
	if err == nil {
		t.Fatal("setKV 실패 시 에러 반환 필요")
	}
	if recorded {
		t.Error("기록 실패했는데 recorded=true")
	}
	// 메타가 여전히 과거 값이므로 다음 호출은 다시 백업을 시도한다.
	calls = 0
	if err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return old, nil },
		func(k, v string) error { recorded = true; return nil },
		io.Discard); err != nil {
		t.Fatalf("재시도 실패: %v", err)
	}
	if calls != 1 || !recorded {
		t.Errorf("재시도 위반: dump %d회, recorded %v", calls, recorded)
	}
}

// meta 읽기 실패는 에러로 전달한다(비차단은 호출부 책임).
func TestRunIfNeededMetaReadError(t *testing.T) {
	calls := 0
	fakeDump(t, &calls)

	a := &Auto{DSN: "fake-dsn"}
	err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return "", errors.New("meta read fail") },
		func(k, v string) error { return nil },
		io.Discard)
	if err == nil || !strings.Contains(err.Error(), "meta read fail") {
		t.Fatalf("metaKV 에러 전달 필요: %v", err)
	}
	if calls != 0 {
		t.Errorf("메타 읽기 실패 시 덤프하면 안 됨: %d회", calls)
	}
}

// SUPERLITE_REAL=1 — 실 Dump로 동일 시나리오(DSN 필요).
func TestRunIfNeededRealDump(t *testing.T) {
	realDumpEnv(t)
	orig := dumpFn
	dumpFn = Dump
	t.Cleanup(func() { dumpFn = orig })

	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	a := &Auto{DSN: testDSN()}
	var buf bytes.Buffer
	if err := a.RunIfNeeded(context.Background(),
		func(k string) (string, error) { return old, nil },
		func(k, v string) error { return nil },
		&buf); err != nil {
		t.Fatalf("RunIfNeeded(실 Dump) error: %v", err)
	}
	if !strings.Contains(buf.String(), "flint backup") {
		t.Error("실 덤프 헤더 없음")
	}
}
