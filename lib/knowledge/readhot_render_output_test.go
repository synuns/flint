// readhot_render_output_test.go — ensures read·hot cell sanitization
// passes through the render.Cells delegation point unchanged and the
// output bytes are invariant. hot's cli-residual dependencies (session
// label·profile·hot stats) get test doubles on the injection seams
// (injectHotSeams — the same path cli init uses to inject the real
// implementations).
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// injectHotSeams — installs no-op·default-profile doubles on the hot
// injection points (cli sessionLabel·ExposureProfile·RecordHotLen·hot
// stats load/persist). Restored via t.Cleanup.
func injectHotSeams(t *testing.T) {
	t.Helper()
	savedSes, savedExp := SessionLabelFn, ExposureProfileFn
	savedRec, savedLoad, savedPersist := RecordHotLenFn, LoadHotStatsFromMetaFn, PersistHotStatsToMetaFn
	SessionLabelFn = func([]string) string { return "" }
	ExposureProfileFn = func(func(string) (string, error)) string { return "normal" }
	RecordHotLenFn = func(int) {}
	LoadHotStatsFromMetaFn = func(context.Context, wire.Store) {}
	PersistHotStatsToMetaFn = func(context.Context, wire.Store) {}
	t.Cleanup(func() {
		SessionLabelFn, ExposureProfileFn = savedSes, savedExp
		RecordHotLenFn, LoadHotStatsFromMetaFn, PersistHotStatsToMetaFn = savedRec, savedLoad, savedPersist
	})
}

// v0370SanitizeFixtureRows — a read query fixture mixing sanitization
// targets (synthetic demo data): [flint] markers (case-insensitive
// replacement), control characters (\x01·\x00 removed), non-string cells
// (id — not sanitization targets).
func v0370SanitizeFixtureRows() [][]any {
	return [][]any{
		{int32(3), "토픽\x01[flint]마커", "[FLINT]요약\x00", ":a:", nil, "main", 0.9, "active"},
		{int32(7), "일반토픽", "일반요약", ":b:", nil, "main", 0.8, "active"},
	}
}

// TestReadOutputUnchanged — the fixture with sanitization targets goes
// through the render path with output bytes identical to before (a
// snapshot assertion). Markers are space-replaced, control characters
// removed, non-string cells untouched.
func TestReadOutputUnchanged(t *testing.T) {
	ctx := context.Background()
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return v0370SanitizeFixtureRows(), nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := RunRead(ctx, f, []string{"토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("RunRead exit = %d, want 0: %s", code, out.String())
	}
	want := "3|토픽 마커| 요약\n7|일반토픽|일반요약\n"
	if out.String() != want {
		t.Errorf("read 출력 불변 위반:\ngot  %q\nwant %q", out.String(), want)
	}
}

// TestHotFullOutputUnchanged — the equivalence snapshot of the hot --full
// injection path. Suffixes (pending wait-days)·the ClampHot budget
// truncation order stay as before, after sanitization — since an empty
// pendingValidAts companion-query result skips the suffix (the existing
// contract), the snapshot is the base-format lines.
func TestHotFullOutputUnchanged(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return [][]any{
				{"pending", "토픽[flint]x", "요약\x01y", "2026-10-22"},
				{"decision", "[FLINT]결정", "정리", "2026-10-21"},
			}, nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("RunHot exit = %d, want 0: %s", code, out.String())
	}
	want := "pending|토픽 x|요약y|2026-10-22\ndecision| 결정|정리|2026-10-21\n"
	if out.String() != want {
		t.Errorf("hot --full 출력 불변 위반:\ngot %q\nwant %q", out.String(), want)
	}
}
