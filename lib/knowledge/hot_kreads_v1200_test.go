// hot_kreads_v1200_test.go — v1.2.0 T2 기록 배선(hot 표면). Review Focus:
// TestKnowledgeReadsHotHeadline(헤드라인 id 전수·kind=hot)·--full 주입 id도
// kind=hot로 기록(주입=기록 정합). BumpReads 캐시 경로·출력 불변은
// hot_single_view_test·hot_v0110 계약이 따로 든다.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// hotKreadsRecorder — RecordKnowledgeReads 호출을 녹음하는 hot fake.
type hotKreadsRecorder struct {
	*fakeStore
	kreads []kreadsCall
}

func (h *hotKreadsRecorder) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	h.kreads = append(h.kreads, kreadsCall{ids, session, kind})
	return nil
}

// v1200HotRows — 헤드라인 질의 행(5열 — kind,topic,summary,ts,id).
// project 팔(id NULL)·pending(3)·decision(7)의 합성 데모 픽스처.
func v1200HotRows(ts time.Time) [][]any {
	return [][]any{
		{"project", "p1", "active", ts, nil},
		{"pending", "토픽", "요약", ts.Add(-time.Minute), int32(3)},
		{"decision", "토픽2", "요약2", ts.Add(-2 * time.Minute), int32(7)},
	}
}

// TestKnowledgeReadsHotHeadline — Review Focus(v1.2.0 T2): 헤드라인(비
// --full) 경로도 노출 id 전수를 knowledge_reads에 kind=hot로 남긴다(G4
// 계상 원천 — 프로젝트 팔 제외).
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

// TestKnowledgeReadsHotFull — --full 주입 id 전수도 kind=hot로 기록된다
// (주문=증분 집합과 동일 원천 — injectedIDsForSurvivors).
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
