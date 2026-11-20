// write_v06_test.go — write --source: source parsing and inbound-metering wiring (fake fast).
// Contracts: ParseSourceFlag (invalid ErrInvalidSource -> exit 3; unspecified = manual),
// recordUsage (UsageStore assertion; unimplemented is a no-op, failure silent), and
// on duplicate, write 1 + dup 1 (no double counting).
package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// usageCall — RecordUsage call record (kind, source).
type usageCall struct{ kind, source string }

// writeUsageRecorder — a fake recording RecordUsage on top of a Store (for seam injection).
type writeUsageRecorder struct {
	Store
	calls []usageCall
	err   error
}

func (u *writeUsageRecorder) RecordUsage(ctx context.Context, kind, source string) error {
	u.calls = append(u.calls, usageCall{kind, source})
	return u.err
}

// withRecorder — swaps the newStore seam to writeUsageRecorder (default fake, metering only).
func withRecorder(t *testing.T, f *fakeStore) *writeUsageRecorder {
	t.Helper()
	rec := &writeUsageRecorder{Store: f}
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return rec, nil }
	t.Cleanup(func() { newStore = prev })
	return rec
}

// TestParseSourceFlag — unspecified = manual; invalid value or missing value = ErrInvalidSource.
func TestParseSourceFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		err  error
	}{
		{"hook", []string{"--source", "hook"}, "hook", nil},
		{"manual", []string{"--source", "manual"}, "manual", nil},
		{"mcp", []string{"--source", "mcp"}, "mcp", nil},
		{"default", nil, "manual", nil},
		{"other-flags", []string{"write", "knowledge", "--session", "s"}, "manual", nil},
		{"invalid", []string{"--source", "bad"}, "", ErrInvalidSource},
		{"empty", []string{"--source", ""}, "", ErrInvalidSource},
		{"dangling", []string{"--source"}, "", ErrInvalidSource},
	}
	for _, tc := range cases {
		got, err := ParseSourceFlag(tc.args)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Fatalf("%s: ParseSourceFlag(%v) = (%q,%v), want (%q,%v)",
				tc.name, tc.args, got, err, tc.want, tc.err)
		}
	}
}

// TestWriteSourceInvalidExit3 — an invalid --source exits 3 with no insert (Review protocol).
func TestWriteSourceInvalidExit3(t *testing.T) {
	f := &fakeStore{}
	withRecorder(t, f)
	out, code := mustRun(t, []string{"write", "knowledge", "--source", "bad"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "error") {
		t.Fatalf("출력=%q, want error JSON", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("무효 소스 시 삽입 금지; 삽입됨 %d건", len(f.inserted))
	}
}

// TestWriteRecordsUsage — after a new commit, recordUsage("write",source) exactly once.
// Unspecified = manual; --source hook; metering failure silent (exit 0 kept).
func TestWriteRecordsUsage(t *testing.T) {
	t.Run("default-manual", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		_, code := mustRun(t, []string{"write", "knowledge"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if len(rec.calls) != 1 || rec.calls[0] != (usageCall{"write", "manual"}) {
			t.Fatalf("calls=%v, want [write manual]", rec.calls)
		}
	})
	t.Run("source-hook", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		_, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if len(rec.calls) != 1 || rec.calls[0] != (usageCall{"write", "hook"}) {
			t.Fatalf("calls=%v, want [write hook]", rec.calls)
		}
	})
	t.Run("usage-error-silent", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		rec.err = errors.New("usage down")
		out, code := mustRun(t, []string{"write", "knowledge"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0(계측 실패 조용히); out=%s", code, out)
		}
	})
}

// TestWriteDupCountsOnceEach — on duplicate, write 1 + dup once per key (no double counting,
// call order [write,source] -> [dup,""] -> [dup_<source>,""] —
// additive). Both dedup paths (ExistsHash and InsertBatch ErrDuplicate)
// behave the same.
func TestWriteDupCountsOnceEach(t *testing.T) {
	// a payload without tags: with no vocabulary warnings, the single duplicate exact-match line is
	// kept (after #6, warnings accompany the dup output anyway).
	payload := `{"topic":"t","summary":"s","confidence":0.9}`
	t.Run("exists-hash", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{exists: true})
		out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"}, payload)
		if code != 0 || strings.TrimSpace(out) != `{"status":"duplicate"}` {
			t.Fatalf("exit=%d out=%s, want duplicate/0", code, out)
		}
		want := []usageCall{{"write", "hook"}, {"dup", ""}, {"dup_hook", ""}}
		if len(rec.calls) != 3 || rec.calls[0] != want[0] || rec.calls[1] != want[1] || rec.calls[2] != want[2] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
	t.Run("insert-batch-dup", func(t *testing.T) {
		// race (passes the pre-check, then ErrDuplicate in the batch): if the recheck finds it exists,
		// it is a lossless duplicate — the write 1 + dup 1 contract is unchanged.
		// (Real collisions that stay ErrDuplicate through retries are covered by write_test.go.)
		rs := &raceDupStore{fakeStore: &fakeStore{}, racedDups: 1}
		rec := &writeUsageRecorder{Store: rs}
		prev := newStore
		newStore = func(ctx context.Context, dsn string) (Store, error) { return rec, nil }
		t.Cleanup(func() { newStore = prev })
		out, code := mustRun(t, []string{"write", "knowledge", "--source", "mcp"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d out=%s, want 0", code, out)
		}
		if !strings.Contains(out, "duplicate") {
			t.Fatalf("out=%s, want duplicate", out)
		}
		want := []usageCall{{"write", "mcp"}, {"dup", ""}, {"dup_mcp", ""}}
		if len(rec.calls) != 3 || rec.calls[0] != want[0] || rec.calls[1] != want[1] || rec.calls[2] != want[2] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
}

// TestWriteNoUsageStoreNoop — a Store without RecordUsage is a no-op (normal behavior).
func TestWriteNoUsageStoreNoop(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f) // fakeStore is not a UsageStore
	out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(no-op 계측); out=%s", code, out)
	}
}
