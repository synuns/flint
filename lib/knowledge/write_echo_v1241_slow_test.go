package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/render"
)

func echoCandRows(t *testing.T, st *writeRealStore, echoID int) [][]any {
	t.Helper()
	rows, err := st.Query(context.Background(),
		`SELECT candidate_id, echo_id, relation, score FROM echo_candidates WHERE echo_id = $1`, echoID)
	if err != nil {
		t.Fatalf("echo_candidates 조회: %v", err)
	}
	return rows
}

func TestSlowWriteEchoCandsAccruedV1241(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()

	var out strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1241 에코 기준","summary":"합성 데모 회의록","confidence":0.9}`+"\n"), &out); code != 0 {
		t.Fatalf("기준 적립 exit=%d: %s", code, out.String())
	}
	aID := idOfTopic(t, st, "v1241 에코 기준")

	out.Reset()
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1241 에코 기준","summary":"합성 데모 회의록","confidence":0.85}`+"\n"), &out); code != 0 {
		t.Fatalf("에코 write exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"committed"`) {
		t.Fatalf("B 커밋 응답 아님: %s", out.String())
	}
	bID := idOfTopicConf(t, st, "v1241 에코 기준", 0.85)
	if bID == 0 || bID == aID {
		t.Fatalf("B 행 id=%d(a=%d) — 신규 행이어야 한다", bID, aID)
	}

	rows := echoCandRows(t, st, bID)
	if len(rows) != 1 {
		t.Fatalf("B echo_candidates=%d행, want 1(후보 수와 동일): %+v", len(rows), rows)
	}
	cand, _ := render.CellToInt(rows[0][0])
	rel, _ := rows[0][2].(string)
	score, ok := rows[0][3].(float64)
	if cand != aID {
		t.Errorf("candidate_id=%d, want %d(기준 행 A)", cand, aID)
	}
	if rel != "duplicates" {
		t.Errorf("relation=%q, want duplicates", rel)
	}
	if !ok || score < 0.99 {
		t.Errorf("score=%v, want 1.0 근사(동일 문면)", rows[0][3])
	}

	out.Reset()
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1241 에코 기준","summary":"합성 데모 회의록","confidence":0.85}`+"\n"), &out); code != 0 {
		t.Fatalf("재전송 exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"duplicate"`) {
		t.Fatalf("재전송 응답=%q, want duplicate", strings.TrimSpace(out.String()))
	}
	rows = echoCandRows(t, st, bID)
	if len(rows) != 3 {
		t.Fatalf("재전송 후 echo_candidates=%d행, want 3(기존 id 기준 재적립): %+v", len(rows), rows)
	}
	for _, r := range rows {
		if eid, _ := render.CellToInt(r[1]); eid != bID {
			t.Errorf("echo_id=%d, want %d(DupMeta.ID — 기존 행 기준)", eid, bID)
		}
	}

	out.Reset()
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"서늘한 아침 산책로 기록","summary":"동 트기 전 개울을 따라 걷는 코스와 만난 새들의 목록을 적어 둔다","confidence":0.9}`+"\n"), &out); code != 0 {
		t.Fatalf("무관 write exit=%d: %s", code, out.String())
	}
	cID := idOfTopic(t, st, "서늘한 아침 산책로 기록")
	if n := len(echoCandRows(t, st, cID)); n != 0 {
		t.Fatalf("무관 write echo_candidates=%d행, want 0: %+v", n, echoCandRows(t, st, cID))
	}
}

func idOfTopicConf(t *testing.T, st *writeRealStore, topic string, conf float64) int {
	t.Helper()
	rows, err := st.Query(context.Background(),
		`SELECT id FROM knowledge WHERE topic = $1 AND confidence = $2`, topic, conf)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 조회(topic=%q conf=%v): %v", topic, conf, err)
	}
	id, _ := render.CellToInt(rows[0][0])
	return id
}

func TestSlowMergeAuditPendingExcludedV1241(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()

	var out strings.Builder
	pend := `{"topic":"v1241 대기 쌍","summary":"합성 데모 동일 문면","confidence":%s}` + "\n"
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		fmt.Sprintf(pend, "0.1")+fmt.Sprintf(pend, "0.11")), &out); code != 0 {
		t.Fatalf("대기쌍 적립 exit=%d: %s", code, out.String())
	}
	p1 := idOfTopicConf(t, st, "v1241 대기 쌍", 0.1)
	p2 := idOfTopicConf(t, st, "v1241 대기 쌍", 0.11)

	out.Reset()
	act := `{"topic":"v1241 활성 쌍","summary":"합성 데모 동일 문면 둘","confidence":%s}` + "\n"
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		fmt.Sprintf(act, "0.9")+fmt.Sprintf(act, "0.91")), &out); code != 0 {
		t.Fatalf("활성쌍 적립 exit=%d: %s", code, out.String())
	}
	a1 := idOfTopicConf(t, st, "v1241 활성 쌍", 0.9)
	a2 := idOfTopicConf(t, st, "v1241 활성 쌍", 0.91)

	fetched, _, err := FetchMergeAuditRows(ctx, st, 50)
	if err != nil {
		t.Fatalf("FetchMergeAuditRows: %v", err)
	}
	got := map[int]bool{}
	for _, r := range fetched {
		got[r.ID] = true
	}
	if got[p1] || got[p2] {
		t.Fatalf("pending 행(%d,%d)이 쌍 스캔에 포함 — status='active' 배제 위반: %+v", p1, p2, fetched)
	}
	if !got[a1] || !got[a2] {
		t.Fatalf("활성 대조쌍(%d,%d) 미스캔: %+v", a1, a2, fetched)
	}

	pairs, _ := ClassifyMergeAuditPairs(fetched, 0.6)
	found := false
	for _, p := range pairs {
		if (p.A == a1 && p.B == a2) || (p.A == a2 && p.B == a1) {
			found = true
		}
		if (p.A == p1 || p.B == p1) || (p.A == p2 || p.B == p2) {
			t.Errorf("pending 쌍 리스팅(%d,%d): %+v", p1, p2, p)
		}
	}
	if !found {
		t.Fatalf("활성 대조쌍 리스팅 부재: %+v", pairs)
	}
}
