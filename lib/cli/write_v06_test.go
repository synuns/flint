// write_v06_test.go — T61 write --source: 소스 파싱·유입 계측 배선(fake fast).
// 계약: ParseSourceFlag(무효 ErrInvalidSource→exit 3·미지정 manual)·
// recordUsage(UsageStore 단언, 미구현 no-op·실패 조용히)·
// duplicate 시 write 1 + dup 1(이중 계수 금지).
package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"flint/lib/store"
)

// usageCall — RecordUsage 호출 기록(kind, source).
type usageCall struct{ kind, source string }

// writeUsageRecorder — Store 위에 RecordUsage를 녹음하는 fake(seam 주입용).
type writeUsageRecorder struct {
	Store
	calls []usageCall
	err   error
}

func (u *writeUsageRecorder) RecordUsage(ctx context.Context, kind, source string) error {
	u.calls = append(u.calls, usageCall{kind, source})
	return u.err
}

// withRecorder — newStore seam을 writeUsageRecorder로 교체(기본 fake, 계측 전용).
func withRecorder(t *testing.T, f *fakeStore) *writeUsageRecorder {
	t.Helper()
	rec := &writeUsageRecorder{Store: f}
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return rec, nil }
	t.Cleanup(func() { newStore = prev })
	return rec
}

// TestParseSourceFlag — 미지정 manual·무효값/값 부재 ErrInvalidSource(플랜 T61).
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

// TestWriteSourceInvalidExit3 — 무효 --source는 exit 3·삽입 없음(Review protocol).
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

// TestWriteRecordsUsage — 신규 커밋 후 recordUsage("write",source) 1회.
// 미지정 manual·--source hook·계측 실패 조용히(exit 0 유지).
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

// TestWriteDupCountsOnceEach — duplicate 시 write 1 + dup 1(이중 계수 금지,
// 호출 순서 [write,source]→[dup,""]). dedup 이중 경로(ExistsHash·InsertBatch
// ErrDuplicate) 모두 동일.
func TestWriteDupCountsOnceEach(t *testing.T) {
	// tags 없는 페이로드: 어휘 경고가 없어 duplicate 한 줄 exact-match가
	// 유지된다(#6 이후 dup 출력에도 경고가 병행되므로).
	payload := `{"topic":"t","summary":"s","confidence":0.9}`
	t.Run("exists-hash", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{exists: true})
		out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"}, payload)
		if code != 0 || strings.TrimSpace(out) != `{"status":"duplicate"}` {
			t.Fatalf("exit=%d out=%s, want duplicate/0", code, out)
		}
		want := []usageCall{{"write", "hook"}, {"dup", ""}}
		if len(rec.calls) != 2 || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
	t.Run("insert-batch-dup", func(t *testing.T) {
		// 레이스(사전 확인 통과 후 배치에서 ErrDuplicate): 재확인에서 존재로
		// 판명되면 유실 없이 duplicate — write 1 + dup 1 계약은 불변.
		// (재시도까지 ErrDuplicate인 실제 충돌은 write_test.go가 다룬다.)
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
		want := []usageCall{{"write", "mcp"}, {"dup", ""}}
		if len(rec.calls) != 2 || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
}

// errDuplicateSentinel — InsertBatch 경로 dedup용 store.ErrDuplicate.
func errDuplicateSentinel() error { return store.ErrDuplicate }

// TestWriteNoUsageStoreNoop — RecordUsage 미구현 Store는 no-op(정상 동작).
func TestWriteNoUsageStoreNoop(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f) // fakeStore는 UsageStore 아님
	out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(no-op 계측); out=%s", code, out)
	}
}
