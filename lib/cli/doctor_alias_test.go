// doctor_alias_test.go — re-provides the test helpers of doctor_test.go, moved to
// lib/ops (runDoctorArgs, freshDoctorFn, setHarnessEnvs), to the residual cli tests
// (doctor_v07, doctor_v094, rules_sync_v097, etc.).
// The real implementation goes via the ops alias (runDoctor).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// runDoctorArgs — runs runDoctor (=ops.RunDoctor) with a fake store.
// Returns output and exit.
func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runDoctor(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// freshDoctorFn — the all-checks-pass baseline response (last-seen 1h ago).
func freshDoctorFn() func(string, []any) ([][]any, error) {
	h := time.Now().Add(-time.Hour)
	return doctorQueryFn(h, h, h)
}

// doctorQueryFn — a shared queryFn injecting per-check meta/pg_indexes responses.
// Returns schema=4, 3 index rows, and 3 last-seen keys (with adjustable elapsed).
func doctorQueryFn(lastWrite, lastHot, lastRead time.Time) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"4"}}, nil
		case strings.Contains(sql, "pg_indexes"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "last_write_at"):
			return [][]any{
				{"last_write_at", lastWrite.UTC().Format(time.RFC3339)},
				{"last_hot_at", lastHot.UTC().Format(time.RFC3339)},
				{"last_read_at", lastRead.UTC().Format(time.RFC3339)},
			}, nil
		}
		return nil, nil
	}
}

// setHarnessEnvs — isolates all 5 harness envs to empty temp directories.
// A precondition of the harnessChecks unit tests (blocking real-home settings files).
func setHarnessEnvs(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"FLINT_CODEX_HOME", "FLINT_GEMINI_HOME", "FLINT_OPENCODE_HOME",
		"FLINT_COPILOT_HOME", "FLINT_CURSOR_DIR",
	} {
		t.Setenv(e, t.TempDir())
	}
}
