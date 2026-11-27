package ops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

type recallAuditJSONV1390 struct {
	Status string `json:"status"`
	MinBad int    `json:"min_bad"`
	Count  int    `json:"count"`
	Items  []struct {
		ID        int    `json:"id"`
		Topic     string `json:"topic"`
		BadRecall int    `json:"bad_recall"`
		Recommend string `json:"recommend"`
	} `json:"items"`
	Summary string `json:"summary"`
}

func parseRecallAuditV1390(t *testing.T, out string) recallAuditJSONV1390 {
	t.Helper()
	var got recallAuditJSONV1390
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	return got
}

func TestRecallAuditRecommendsSupersede(t *testing.T) {
	rows := [][]any{
		{int32(21), "v1390-recall-audit 배포 절차 데모", int64(3), int64(2)},
	}
	f, _ := recallAuditFake(t, rows)
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseRecallAuditV1390(t, out)
	if got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("count/items = %d/%d — %s", got.Count, len(got.Items), out)
	}
	it := got.Items[0]
	if it.ID != 21 || it.BadRecall != 3 {
		t.Fatalf("item = %+v, want (21, bad 3)", it)
	}
	if it.Recommend != "supersede" {
		t.Errorf("recommend = %q, want supersede(bad 3 >= 하한 2·본행 recall 있음)", it.Recommend)
	}
	if len(f.superseded) > 0 || len(f.invalidatedIDs) > 0 || len(f.commits) > 0 {
		t.Errorf("recommend 리포트가 처분 실행: supersede=%v invalidate=%v commits=%v",
			f.superseded, f.invalidatedIDs, f.commits)
	}
}

func TestRecallAuditRecommendsHoldBelowFloor(t *testing.T) {
	rows := [][]any{
		{int32(22), "v1390-recall-audit 회고 리듬 데모", int64(1), int64(5)},
	}
	f, _ := recallAuditFake(t, rows)
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseRecallAuditV1390(t, out)
	if got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("count/items = %d/%d — %s", got.Count, len(got.Items), out)
	}
	it := got.Items[0]
	if it.Recommend != "hold" {
		t.Errorf("recommend = %q, want hold(bad 1 < 하한 2)", it.Recommend)
	}
}

func TestRecallAuditRecommendsExpireWhenRowNotRecalled(t *testing.T) {
	rows := [][]any{
		{int32(23), "v1390-recall-audit 무회수 잔존 행 데모", int64(3), int64(0)},
	}
	f, _ := recallAuditFake(t, rows)
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseRecallAuditV1390(t, out)
	if got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("count/items = %d/%d — %s", got.Count, len(got.Items), out)
	}
	if it := got.Items[0]; it.Recommend != "expire" {
		t.Errorf("recommend = %q, want expire(bad 3 >= 하한·본행 recall 0)", it.Recommend)
	}
}

func TestRecallAuditRecommendFloorOverride(t *testing.T) {
	rows := [][]any{
		{int32(24), "v1390-recall-audit 오버라이드 경계 데모", int64(3), int64(2)},
	}
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "AS bad_recall") && strings.Contains(sql, "ORDER BY") {
			return rows, nil
		}
		if strings.Contains(sql, "FROM meta") && strings.Contains(sql, "SELECT") {
			return [][]any{{"recall_audit_recommend_min", "4"}}, nil
		}
		if strings.Contains(sql, "INSERT INTO meta") {
			return nil, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseRecallAuditV1390(t, out)
	if it := got.Items[0]; it.Recommend != "hold" {
		t.Errorf("recommend = %q, want hold(bad 3 < 오버라이드 하한 4)", it.Recommend)
	}
}

func TestRecallAuditRecommendThresholdRegistered(t *testing.T) {
	m, ok := store.ThresholdMetaByKey("recall_audit_recommend_min")
	if !ok {
		t.Fatal("thresholds 등록표에 recall_audit_recommend_min 부재")
	}
	if m.Field != "RecallAuditRecommendMin" {
		t.Errorf("meta field = %q, want RecallAuditRecommendMin", m.Field)
	}
	if def := store.DefaultThresholds().RecallAuditRecommendMin; def != 2 {
		t.Errorf("기본값 = %d, want 2", def)
	}
	th, err := store.ParseThresholds([][2]any{{"recall_audit_recommend_min", "5"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.RecallAuditRecommendMin != 5 {
		t.Errorf("오버라이드 = %d, want 5", th.RecallAuditRecommendMin)
	}
}
