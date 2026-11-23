// sessionhook_test.go — the v1.26.0 T3 session-start self-report contracts:
// RunSessionStart stamps the running version into
// self_version_last:session-start (value "version|RFC3339", silent failure),
// and the meta-hygiene classification accepts the new key prefix.
package ops

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// withSelfVersion — pins the version seams to a final build (the standalone
// ops test init keeps them dev/empty, which skips the self-report) and wires
// the hot-path seams (injectSessionLabelFn — the sessionlabel_v102 convention).
func withSelfVersion(t *testing.T, v string) {
	t.Helper()
	prevCur, prevVal := CurrentVersionFn, ValidVersionFn
	CurrentVersionFn = func() string { return v }
	ValidVersionFn = func(s string) bool { return s == v }
	t.Cleanup(func() { CurrentVersionFn, ValidVersionFn = prevCur, prevVal })
	injectSessionLabelFn(t)
}

// selfReportStore — a fakeStore recording the meta upsert args.
type selfReportStore struct {
	fakeStore
	mu   sync.Mutex
	kv   [][2]string
	fail bool
}

func (s *selfReportStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if strings.Contains(sql, "INSERT INTO meta") && strings.Contains(sql, "ON CONFLICT") && len(args) >= 2 {
		if strings.Contains(sql, "self_version_last") || strings.HasPrefix(fmtString(args[0]), "self_version_last:") {
			s.mu.Lock()
			s.kv = append(s.kv, [2]string{fmtString(args[0]), fmtString(args[1])})
			s.mu.Unlock()
			if s.fail {
				return nil, errSelfReportFail
			}
			return nil, nil
		}
	}
	return s.fakeStore.Query(ctx, sql, args...)
}

// fmtString — an any arg to a string without fmt.Sprint nil quirks.
func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

// errSelfReportFail — the injected upsert failure (the silent contract).
var errSelfReportFail = &selfReportError{}

type selfReportError struct{}

func (*selfReportError) Error() string { return "self-report upsert failed" }

// TestSessionStartSelfVersionReport — one meta upsert per session start:
// key self_version_last:session-start, value "version|RFC3339" (parseable
// timestamp), and a dev build (unfinalized version) skips the write.
func TestSessionStartSelfVersionReport(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	withSelfVersion(t, "1.26.0")
	s := &selfReportStore{fakeStore: fakeStore{queryFn: freshDoctorFn()}}
	var out bytes.Buffer
	code := RunSessionStart(context.Background(), s, []string{"--session", "t3-self-report"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0: %s", code, out.String())
	}
	if len(s.kv) != 1 {
		t.Fatalf("upsert %d회, want 1회: %+v", len(s.kv), s.kv)
	}
	key, val := s.kv[0][0], s.kv[0][1]
	if key != "self_version_last:session-start" {
		t.Errorf("key=%q, want self_version_last:session-start", key)
	}
	ver, ts, ok := strings.Cut(val, "|")
	if !ok || ver != "1.26.0" {
		t.Errorf("value=%q, want 1.26.0|RFC3339", val)
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("타임스탬프 파싱 실패: %q %v", ts, err)
	}
}

// TestSessionStartSelfVersionReportSilentFailure — an upsert failure is
// silent: the hook still exits 0 and injects as usual (§13).
func TestSessionStartSelfVersionReportSilentFailure(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	withSelfVersion(t, "1.26.0")
	s := &selfReportStore{fakeStore: fakeStore{queryFn: freshDoctorFn()}, fail: true}
	var out bytes.Buffer
	code := RunSessionStart(context.Background(), s, []string{"--session", "t3-silent"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("upsert 실패 exit=%d, want 0(조용히): %s", code, out.String())
	}
	if strings.Contains(out.String(), "self_version_last") {
		t.Errorf("실패 노출 위반: %q", out.String())
	}
}

// TestSelfVersionKnownMetaKey — the new prefix is classified known, so
// meta-hygiene does not flag doctor's own self-report key.
func TestSelfVersionKnownMetaKey(t *testing.T) {
	if !KnownMetaKey("self_version_last:session-start") {
		t.Error("KnownMetaKey(self_version_last:session-start) = false, want true")
	}
}
