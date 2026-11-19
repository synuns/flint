// hot_kreads_v1200_test.go — the recording wiring (hot surface).
// TestKnowledgeReadsHotHeadline (headline full id set·kind=hot)·the --full
// injected ids are also recorded with kind=hot (injection=recording
// alignment). The BumpReads cache path·output invariants are covered
// separately by the hot_single_view_test·hot_v0110 contracts.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// hotKreadsRecorder — a hot fake that records RecordKnowledgeReads calls.
type hotKreadsRecorder struct {
	*fakeStore
	kreads []kreadsCall
}

func (h *hotKreadsRecorder) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	h.kreads = append(h.kreads, kreadsCall{ids, session, kind})
	return nil
}

// v1200HotRows — headline query rows (5 columns — kind,topic,summary,ts,
// id). A synthetic demo fixture of a project entry (id NULL)·pending (3)·
// decision (7).
func v1200HotRows(ts time.Time) [][]any {
	return [][]any{
		{"project", "p1", "active", ts, nil},
		{"pending", "토픽", "요약", ts.Add(-time.Minute), int32(3)},
		{"decision", "토픽2", "요약2", ts.Add(-2 * time.Minute), int32(7)},
	}
}

// TestKnowledgeReadsHotHeadline — the headline (non --full) path also
// leaves the full exposed id set in knowledge_reads with kind=hot (the G4
// accounting source — project entries excluded).
func TestKnowledgeReadsHotHeadline(t *testing.T) {
	injectHotSeams(t)
	saved := SessionLabelFn
	SessionLabelFn = func([]string) string { return "hot-kr" }
	t.Cleanup(func() { SessionLabelFn = saved })
	ctx := context.Background()
	st := &hotKreadsRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return v1200HotRows(time.Now().UTC()), nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot exit = %d: %s", code, out.String())
	}
	if len(st.kreads) != 1 {
		t.Fatalf("knowledge_reads 기록 %d건, want 1(헤드라인 포함)", len(st.kreads))
	}
	call := st.kreads[0]
	if len(call.ids) != 2 || call.ids[0] != 3 || call.ids[1] != 7 {
		t.Errorf("kreads ids = %v, want [3 7](헤드라인 id 전수 — 프로젝트 팔 제외)", call.ids)
	}
	if call.kind != "hot" || call.session != "hot-kr" {
		t.Errorf("kreads kind·session = %q·%q, want hot·hot-kr", call.kind, call.session)
	}
}

// TestKnowledgeReadsHotFull — the --full injected full id set is also
// recorded with kind=hot (the same source as the order=increment set —
// injectedIDsForSurvivors).
func TestKnowledgeReadsHotFull(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	ts := time.Now().UTC()
	rows := [][]any{
		{"project", "p1", "active", ts, nil, nil, nil},
		{"pending", "토픽", "요약", ts.Add(-time.Minute), int32(3), ts, ts},
		{"decision", "토픽2", "요약2", ts.Add(-2 * time.Minute), int32(7), ts, ts},
	}
	st := &hotKreadsRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	if len(st.kreads) != 1 {
		t.Fatalf("knowledge_reads 기록 %d건, want 1", len(st.kreads))
	}
	call := st.kreads[0]
	if len(call.ids) != 2 || call.ids[0] != 3 || call.ids[1] != 7 {
		t.Errorf("kreads ids = %v, want [3 7](주입 생존 id 전수)", call.ids)
	}
	if call.kind != "hot" {
		t.Errorf("kreads kind = %q, want hot", call.kind)
	}
}
