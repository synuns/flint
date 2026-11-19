// maintain_test.go — maintain 소형 계약. ① TUI 로더가
// 보고 3종에 명시하는 --limit 500 호출 패턴이 모드 귀속 검증을 통과하고
// 저장소 조회에 상한이 그대로 반영된다(잘림 무표시 폐지의 ops측 절반 —
// TUI측 인자 조립은 ops.MaintainTUILoadLimit 단일 소스). ② usage 라인이
// --tui 진입을 안내한다. fakeMaintStore·runMaintainArgs는 care_test.go
// 관례 재사용.
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
)

// limitCapturingStore — DupGroups·ConflictGroups 상한 캡처 fake
// (fakeMaintStore은 limit을 기록하지 않는다 — 여기서만 필요한 얇은 랩).
type limitCapturingStore struct {
	*fakeMaintStore
	dupLimit      int
	conflictLimit int
}

func (f *limitCapturingStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	f.dupLimit = limit
	return nil, nil
}
func (f *limitCapturingStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	f.conflictLimit = limit
	return nil, nil
}

// runLimitArgs — 랩 store을 그대로 RunMaintain에 넣는 실행 헬퍼
// (runMaintainArgs는 *fakeMaintStore 고정이라 여기선 로컬 판).
func runLimitArgs(t *testing.T, f *limitCapturingStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestMaintainTUILoadLimitContract — B3: TUI suspects 로더의 호출 패턴
// (--limit 500 + 모드)이 세 보고 모드 전부에서 무효 인자(exit 1/3)로
// 거부되지 않고 상한 500이 스캔에 그대로 흐른다. 기본 상한
// (dup/conflict 10·stale 20)은 사람 대화형용 — TUI는 잘림이 화면에
// 표시되지 않는 만큼 명시적 상한으로 대체한다.
func TestMaintainTUILoadLimitContract(t *testing.T) {
	if MaintainTUILoadLimit != 500 {
		t.Fatalf("MaintainTUILoadLimit = %d, want 500(스펙 v1.6.1 B3)", MaintainTUILoadLimit)
	}
	f := &limitCapturingStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}

	// dup-suspects — TUI가 조립하는 인자 패턴 그대로.
	out, code := runLimitArgs(t, f, "--limit", "500", "--dup-suspects")
	if code != 0 || !strings.Contains(out, `"status":"dup-suspects"`) {
		t.Fatalf("dup-suspects exit=%d out=%s", code, out)
	}
	if f.dupLimit != 500 {
		t.Errorf("DupGroups limit = %d, want 500", f.dupLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--conflict-suspects")
	if code != 0 || !strings.Contains(out, `"status":"conflict-suspects"`) {
		t.Fatalf("conflict-suspects exit=%d out=%s", code, out)
	}
	if f.conflictLimit != 500 {
		t.Errorf("ConflictGroups limit = %d, want 500", f.conflictLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--stale")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("stale exit=%d out=%s", code, out)
	}
}

// TestMaintainUsageLineMentionsTUI — B4: bare usage(모드 미지정 exit 3)
// 안내에 --tui 옵트인 진입이 포함된다(에이전트 자가수신 — 에러 메시지
// 공통 접미가 유일한 발견 경로인 경우).
func TestMaintainUsageLineMentionsTUI(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f)
	if code != 3 {
		t.Fatalf("bare exit = %d, want 3", code)
	}
	if !strings.Contains(out, "usage: flint maintain") {
		t.Fatalf("usage 부재: %s", out)
	}
	if !strings.Contains(out, "--tui") {
		t.Fatalf("usage에 --tui 부재: %s", out)
	}
}

// ── decay 감쇠 산식·--decay-audit(회상 강화 회귀는 knowledge 패키지) ──

// TestEventEffectiveScoreBoundaries — 감쇠 산식 경계(v1.9.0 — 단일 정의
// knowledge.EventEffectiveScore로 소비. 구 ops.EffectiveRecallScore 래퍼는
// 파생 전환과 함께 폐지). 1일(선형 -1)·100일(하한 0)·미회상(count 0=0)·
// rate 오버라이드.
func TestEventEffectiveScoreBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		count int
		last  time.Time
		rate  float64
		want  int
	}{
		{"1일 경과 — count-1", 5, now.Add(-25 * time.Hour), 1, 4},
		{"100일 경과 — 하한 0", 5, now.Add(-100 * 24 * time.Hour), 1, 0},
		{"23시간 — 0일(절사) 감쇠 없음", 5, now.Add(-23 * time.Hour), 1, 5},
		{"미회상(count 0) — 0", 0, now.Add(-10 * 24 * time.Hour), 1, 0},
		{"rate 0.5 — 2일 -1", 5, now.Add(-2 * 24 * time.Hour), 0.5, 4},
		{"rate 2 — 가속 하한 0", 3, now.Add(-2 * 24 * time.Hour), 2, 0},
	}
	for _, c := range cases {
		if got := knowledge.EventEffectiveScore(c.count, c.last, now, c.rate); got != c.want {
			t.Errorf("%s: EventEffectiveScore(%d, %v, rate %v) = %d, want %d",
				c.name, c.count, c.last, c.rate, got, c.want)
		}
	}
}

// TestMaintainDecayAuditDerivedSource — v1.9.0 파생 전환 회귀: 조회
// 원천은 recall_events 집계 조인(cnt·last_at)이어야 하고 폐기된 v11
// 실열(knowledge.recall_score·last_recalled_at)을 참조해서는 안 된다.
func TestMaintainDecayAuditDerivedSource(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	var gotSQL string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			gotSQL = sql
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if !strings.Contains(gotSQL, "recall_events") {
		t.Fatalf("조회 원천에 recall_events 파생 조인 부재: %s", gotSQL)
	}
	for _, gone := range []string{"k.recall_score", "k.last_recalled_at", ", recall_score", ", last_recalled_at"} {
		if strings.Contains(gotSQL, gone) {
			t.Fatalf("폐기된 v11 실열 %q 참조 잔존: %s", gone, gotSQL)
		}
	}
}

// decayAuditRowsFixture — --decay-audit 조회 원천 행(합성 데모 — v1.9.0
// 파생 전환 후 id, cnt(이벤트 수), last_at(최종 회상 시각) 3열). 미회상
// (NULL — 서버측 WHERE 제외분의 Go 방어 재현)·0일 경과 행이 후보
// 필터에서 제외되는지까지 함께 검증한다.
func decayAuditRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(3), int32(5), now.Add(-100 * 24 * time.Hour)}, // 완전 감쇠(하한 0)
		{int32(1), int32(5), now.Add(-25 * time.Hour)},       // 1일 감쇠
		{int32(2), int32(7), nil},                            // 미회상 — 제외
		{int32(4), int32(9), now.Add(-1 * time.Hour)},        // 0일 — 제외
	}
}

// decayAuditJSON — --decay-audit 출력 파싱용.
type decayAuditJSON struct {
	Status       string  `json:"status"`
	DecayRate    float64 `json:"decay_rate"`
	ArchiveBelow float64 `json:"archive_below"`
	Count        int     `json:"count"`
	Items        []struct {
		ID           int    `json:"id"`
		Score        int    `json:"score"`
		LastRecalled string `json:"last_recalled"`
		Effective    int    `json:"effective"`
		Recommend    string `json:"recommend"`
	} `json:"items"`
}

// TestMaintainDecayAuditList — 감쇠 후보 목록 —
// 감쇠 진행 행만 나열(미회상·0일 제외)·기본 decay_rate=1·archive_below=0
// (비활성 — recommend 전원 공란).
func TestMaintainDecayAuditList(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil // meta(thresholds) 비어 있음 — 기본값
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "decay-audit" || got.Count != 2 || len(got.Items) != 2 {
		t.Fatalf("status/count/items = %q/%d/%d, want decay-audit/2/2 — %s", got.Status, got.Count, len(got.Items), out)
	}
	if got.DecayRate != 1 || got.ArchiveBelow != 0 {
		t.Errorf("기본 임계값 decay_rate=%v archive_below=%v, want 1/0", got.DecayRate, got.ArchiveBelow)
	}
	first, second := got.Items[0], got.Items[1]
	if first.ID != 3 || first.Score != 5 || first.Effective != 0 || first.Recommend != "" {
		t.Errorf("items[0] = %+v, want id3 score5 eff0(100일 하한) recommend 없음", first)
	}
	if second.ID != 1 || second.Score != 5 || second.Effective != 4 {
		t.Errorf("items[1] = %+v, want id1 score5 eff4(1일)", second)
	}
	if want := now.Add(-25 * time.Hour).Format(time.RFC3339); second.LastRecalled != want {
		t.Errorf("items[1].last_recalled = %q, want %q", second.LastRecalled, want)
	}
}

// TestMaintainDecayAuditLimit — --limit 상한이 목록에 반영된다(stale 관례).
func TestMaintainDecayAuditLimit(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("limit 1: count/items = %d/%d, want 1/1 — %s", got.Count, len(got.Items), out)
	}
}

// TestMaintainDecayAuditThresholdOverride — meta 오버라이드
// decay_rate·archive_below가 산식·권장에 반영된다(effective < archive_below
// → recommend "archive").
func TestMaintainDecayAuditThresholdOverride(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return [][]any{
				{int32(1), int32(5), now.Add(-6 * 24 * time.Hour)},  // 5-0.5*6=2 → archive
				{int32(2), int32(10), now.Add(-4 * 24 * time.Hour)}, // 10-0.5*4=8 → 유지
			}, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"decay_rate", "0.5"}, {"archive_below", "3"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.DecayRate != 0.5 || got.ArchiveBelow != 3 {
		t.Fatalf("오버라이드 decay_rate=%v archive_below=%v, want 0.5/3 — %s", got.DecayRate, got.ArchiveBelow, out)
	}
	if got.Items[0].ID != 1 || got.Items[0].Effective != 2 || got.Items[0].Recommend != "archive" {
		t.Errorf("items[0] = %+v, want id1 eff2 recommend archive", got.Items[0])
	}
	if got.Items[1].ID != 2 || got.Items[1].Effective != 8 || got.Items[1].Recommend != "" {
		t.Errorf("items[1] = %+v, want id2 eff8 recommend 없음", got.Items[1])
	}
}

// TestMaintainDecayAuditRejectsForeignFlags — 모드 귀속 검증: --decay-audit는
// 읽기 전용 보고 — --days 등 무관 값 플래그는 에러 거부(exit 1).
func TestMaintainDecayAuditRejectsForeignFlags(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--decay-audit", "--days", "5")
	if code != 1 {
		t.Fatalf("exit = %d, want 1(무관 플래그 거부): %s", code, out)
	}
}
