package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

func ageAuditClaimRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(51), "t4v-배포 버전 경로 데모", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "decision", "agent"},
		{int32(52), "t4v-회고 절차 데모", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "procedure", "human"},
		{int32(53), "t4v-출처 휴먼 데모", now.Add(-90 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "", "human"},
		{int32(54), "t4v-가져오기 데모", now.Add(-90 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "", "import"},
		{int32(55), "t4v-평범 데모", now.Add(-5 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "", "agent"},
	}
}

func runAgeAuditFixture(t *testing.T, rows [][]any) ageAuditJSON {
	t.Helper()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return rows, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--age-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got ageAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	return got
}

func TestSuggestedVolClaimDefault(t *testing.T) {
	got := runAgeAuditFixture(t, ageAuditClaimRowsFixture(timedAgeNow(t)))
	if len(got.Items) != 5 {
		t.Fatalf("항목 수 = %d, want 5: %+v", len(got.Items), got.Items)
	}
	byID := map[int]struct{ sug, tag string }{}
	for _, it := range got.Items {
		byID[it.ID] = struct{ sug, tag string }{it.SuggestedVol, it.VolTag}
	}
	if r := byID[51]; r.sug != "vol:immutable" || r.tag != "" {
		t.Errorf("id51 = %+v, want suggested vol:immutable(claim=decision이 키워드·출처보다 우선)", r)
	}
	if r := byID[52]; r.sug != "vol:conditional" || r.tag != "" {
		t.Errorf("id52 = %+v, want suggested vol:conditional(claim=procedure가 출처 human보다 우선)", r)
	}
	if r := byID[53]; r.sug != "vol:immutable" || r.tag != "" {
		t.Errorf("id53 = %+v, want suggested vol:immutable(출처 human 기본값)", r)
	}
	if r := byID[54]; r.sug != "vol:conditional" || r.tag != "" {
		t.Errorf("id54 = %+v, want suggested vol:conditional(출처 import 기본값)", r)
	}
	if r := byID[55]; r.sug != "vol:conditional" || r.tag != "" {
		t.Errorf("id55 = %+v, want suggested vol:conditional(미매칭 폴백)", r)
	}
}

func timedAgeNow(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
}

func TestSuggestedVolExistingTagWins(t *testing.T) {
	now := timedAgeNow(t)
	rows := [][]any{
		{int32(61), "t4v-기존태그 조건부 데모", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env::vol:conditional:", "decision", "agent"},
		{int32(62), "t4v-기존태그 휘발 데모", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env::vol:volatile:", "", "human"},
	}
	got := runAgeAuditFixture(t, rows)
	byID := map[int]struct{ sug, tag string }{}
	for _, it := range got.Items {
		byID[it.ID] = struct{ sug, tag string }{it.SuggestedVol, it.VolTag}
	}
	if r := byID[61]; r.tag != "vol:conditional" || r.sug != "" {
		t.Errorf("id61 = %+v, want vol_tag vol:conditional·제안 없음(기존 태그가 claim보다 우선)", r)
	}
	if r := byID[62]; r.tag != "vol:volatile" || r.sug != "" {
		t.Errorf("id62 = %+v, want vol_tag vol:volatile·제안 없음(기존 태그가 출처보다 우선)", r)
	}
}

type applyVolCall struct {
	id  int
	tag string
}

type applyVolFake struct {
	*fakeMaintStore
	applyCalls []applyVolCall
}

func (f *applyVolFake) MergeVolTag(ctx context.Context, id int, volTag string) (int64, error) {
	f.applyCalls = append(f.applyCalls, applyVolCall{id, volTag})
	return 1, nil
}

func newApplyVolFake(rows [][]any) *applyVolFake {
	return &applyVolFake{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return rows, nil
		}
		return nil, nil
	}}}}
}

func runApplyVolArgs(t *testing.T, st wire.Store, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), st, args, nil, &out)
	return out.String(), code
}

type applyVolJSON struct {
	Status  string `json:"status"`
	Applied bool   `json:"applied"`
	Count   int    `json:"count"`
	Items   []struct {
		ID           int    `json:"id"`
		SuggestedVol string `json:"suggested_vol"`
		MergedTag    string `json:"merged_tag"`
	} `json:"items"`
	Summary string `json:"summary"`
}

func parseApplyVolJSON(t *testing.T, out string) applyVolJSON {
	t.Helper()
	var got applyVolJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	return got
}

func applyVolRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(51), "t4v-배포 버전 경로 데모", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "decision", "agent"},
		{int32(52), "t4v-회고 절차 데모", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:", "procedure", "human"},
		{int32(61), "t4v-기존태그 휘발 데모", now.Add(-100 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env::vol:volatile:", "", "agent"},
	}
}

func TestApplyVolRequiresYes(t *testing.T) {
	f := newApplyVolFake(applyVolRowsFixture(timedAgeNow(t)))
	out, code := runApplyVolArgs(t, f, "--age-audit", "--apply-vol")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseApplyVolJSON(t, out)
	if got.Status != "apply-vol" || got.Applied {
		t.Errorf("드라이런 표시 오류: status=%q applied=%v, want apply-vol/false", got.Status, got.Applied)
	}
	if got.Count != 2 {
		t.Errorf("제안 수 = %d, want 2(미태깅 2행): %+v", got.Count, got.Items)
	}
	if len(f.applyCalls) != 0 {
		t.Errorf("--yes 없이 쓰기 발생: %+v", f.applyCalls)
	}
	if len(f.fakeStore.commits) != 0 {
		t.Errorf("--yes 없이 커밋 발생: %v", f.fakeStore.commits)
	}
}

func TestApplyVolMergesTags(t *testing.T) {
	shortRetry(t)
	f := newApplyVolFake(applyVolRowsFixture(timedAgeNow(t)))
	out, code := runApplyVolArgs(t, f, "--age-audit", "--apply-vol", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	got := parseApplyVolJSON(t, out)
	if !got.Applied || got.Count != 2 {
		t.Fatalf("적용 결과 오류: applied=%v count=%d: %s", got.Applied, got.Count, out)
	}
	want := map[int]string{51: ":vol:immutable:", 52: ":vol:conditional:"}
	for _, c := range f.applyCalls {
		if want[c.id] != c.tag {
			t.Errorf("id=%d 병합 태그 %q, want %q", c.id, c.tag, want[c.id])
		}
		delete(want, c.id)
	}
	if len(want) != 0 {
		t.Errorf("미병합 제안 잔존: %v", want)
	}
	for _, c := range f.applyCalls {
		if c.id == 61 {
			t.Errorf("기존 vol 태그 행(id=61) 병합 시도 — 기존 태그 보존 위반")
		}
	}
	if len(f.fakeStore.commits) != 1 {
		t.Errorf("커밋 수 = %d, want 1: %v", len(f.fakeStore.commits), f.fakeStore.commits)
	}
	if len(f.fakeStore.commits) == 1 && !strings.Contains(f.fakeStore.commits[0], "apply-vol") {
		t.Errorf("커밋 메시지에 apply-vol 없음: %q", f.fakeStore.commits[0])
	}
}

func TestApplyVolRepoRoute(t *testing.T) {
	shortRetry(t)
	f := newApplyVolFake(applyVolRowsFixture(timedAgeNow(t)))
	out, code := runApplyVolArgs(t, f, "--age-audit", "--apply-vol", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.applyCalls) != 2 {
		t.Errorf("Repo 메서드 경유 호출 수 = %d, want 2", len(f.applyCalls))
	}
	for _, q := range f.fakeStore.queries {
		if strings.Contains(q, "UPDATE knowledge") {
			t.Errorf("원시 UPDATE SQL 탐지(Repo 경유 위반): %s", q)
		}
	}
	bare := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return applyVolRowsFixture(timedAgeNow(t)), nil
		}
		return nil, nil
	}}}
	out2, code2 := runApplyVolArgs(t, bare, "--age-audit", "--apply-vol", "--yes")
	if code2 == 0 || !strings.Contains(out2, "unsupported store") {
		t.Errorf("미지원 저장소 경로: exit=%d out=%s, want 1/unsupported store", code2, out2)
	}
}

func TestDecayCircuitUnchanged(t *testing.T) {
	now := timedAgeNow(t)
	last := now.Add(-10 * 24 * time.Hour)
	if got := knowledge.EffectiveScoreFor(3, last, now, 1.0, ":vol:immutable:", 0, 0); got != 3 {
		t.Errorf("immutable 감쇠 면제 변동: %d, want 3", got)
	}
	if got := knowledge.EffectiveScoreFor(3, last, now, 1.0, "", 0, 0); got != 0 {
		t.Errorf("미태깅 감쇠 변동: %d, want 0(10일 경과·일 1.0 감쇠)", got)
	}
	if got := knowledge.EffectiveScoreFor(8, last, now, 1.0, "", 2, 3); got != 4 {
		t.Errorf("생존 완화 감쇠 변동: %d, want 4", got)
	}
	if got := StaleScore(200, 0, 180); got != 70 {
		t.Errorf("StaleScore(200,0,180) = %d, want 70", got)
	}
	if got := StaleScore(10, 3, 180); got != 34 {
		t.Errorf("StaleScore(10,3,180) = %d, want 34", got)
	}

	var gotSQL string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			gotSQL = sql
			return ageAuditClaimRowsFixture(now), nil
		}
		return nil, nil
	}}}
	items, err := listStaleScored(context.Background(), f, 0)
	if err != nil {
		t.Fatalf("listStaleScored: %v", err)
	}
	if !strings.Contains(gotSQL, "strpos(k.tags, ':vol:immutable:') = 0") {
		t.Errorf("서버측 immutable 제외 조건 변동: %s", gotSQL)
	}
	byID := map[int]staleScoredItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if it, ok := byID[51]; !ok || it.Score != 70 {
		t.Errorf("id51(claim=decision) 감쇠 점수 변동: %+v, want score70 — 제안은 면제 아님", it)
	}
	got := runAgeAuditFixture(t, ageAuditClaimRowsFixture(now))
	for _, it := range got.Items {
		if it.ID == 51 && (!it.OverThreshold || it.Score != 70) {
			t.Errorf("claim=decision 행 마킹 변동: %+v, want score70 마킹 유지", it)
		}
	}
}
