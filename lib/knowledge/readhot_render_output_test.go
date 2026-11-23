package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

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

func v0370SanitizeFixtureRows() [][]any {
	return [][]any{
		{int32(3), "토픽\x01[flint]마커", "[FLINT]요약\x00", ":a:", nil, "main", 0.9, "active"},
		{int32(7), "일반토픽", "일반요약", ":b:", nil, "main", 0.8, "active"},
	}
}

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
