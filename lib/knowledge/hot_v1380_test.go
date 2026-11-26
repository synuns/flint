package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

type hotStateFake struct {
	*fakeStore
	openStates  []map[string]any
	listProject int
	listLimit   int
}

func (f *hotStateFake) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	return 1, "open", nil
}

func (f *hotStateFake) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	return nil
}

func (f *hotStateFake) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	f.listProject = projectID
	f.listLimit = limit
	return f.openStates
}

func hotStateStore(t *testing.T, rows [][]any, open []map[string]any) *hotStateFake {
	t.Helper()
	return &hotStateFake{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil
	}}, openStates: open}
}

func synthState(id int, projectID int, task, next string, updated time.Time) map[string]any {
	return map[string]any{
		"id": id, "project_id": projectID, "task": task,
		"next_action": next, "updated_at": updated,
	}
}

func TestHotIncludesOpenSnapshot(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "합성토픽", "합성요약", ts, int32(3), ts, ts},
	}
	open := []map[string]any{
		synthState(1, 2, "합성 과업A", "다음A", now.Add(-time.Hour)),
		synthState(2, 3, "합성 과업B", "", now.Add(-2*time.Hour)),
	}
	t.Run("open 행이 state 3열로 하단 주입", func(t *testing.T) {
		f := hotStateStore(t, rows, open)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		got := out.String()
		if !strings.Contains(got, "state|합성 과업A|다음A\n") {
			t.Errorf("state 행A 누락: %q", got)
		}
		if !strings.Contains(got, "state|합성 과업B|\n") {
			t.Errorf("state 행B(빈 next 3열 유지) 누락: %q", got)
		}
		idxA := strings.Index(got, "state|합성 과업A")
		idxB := strings.Index(got, "state|합성 과업B")
		if idxA < 0 || idxB < 0 || idxA > idxB {
			t.Errorf("state 행 순서 = A@%d B@%d, want A 선행(updated_at 최신순)", idxA, idxB)
		}
		if !strings.HasSuffix(got, "state|합성 과업B|\n") {
			t.Errorf("스냅샷이 출력 하단이 아님: %q", got)
		}
		if idx := strings.Index(got, "합성요약"); idx < 0 || idx > idxA {
			t.Errorf("지식 행(%d)이 스냅샷(A@%d)보다 앞이어야 함", idx, idxA)
		}
		if f.listLimit <= 0 {
			t.Errorf("ListOpenWorkState limit = %d, want 양수", f.listLimit)
		}
	})
	t.Run("헤드라인 경로에도 하단 주입", func(t *testing.T) {
		f := hotStateStore(t, rows, open)
		var out bytes.Buffer
		if code := RunHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot exit = %d: %s", code, out.String())
		}
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("헤드라인 행 수 = %d, want 3(헤드라인+state 2): %q", len(lines), out.String())
		}
		if !strings.HasPrefix(lines[0], "[flint]") {
			t.Errorf("첫 행 = %q, want [flint] 헤드라인", lines[0])
		}
		if lines[1] != "state|합성 과업A|다음A" || lines[2] != "state|합성 과업B|" {
			t.Errorf("state 행 = %q %q, want 최신순 A·B", lines[1], lines[2])
		}
	})
	t.Run("스냅샷 부재 시 기존 hot 출력 바이트 동일", func(t *testing.T) {
		f := hotStateStore(t, rows, nil)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		want := hotFullOutput(hotSuffixedLines(rows, now))
		if out.String() != want {
			t.Errorf("출력 = %q, want %q(스냅샷 부재 시 바이트 동일)", out.String(), want)
		}
	})
	t.Run("work_state 미구현 스토어에서도 기존 출력 유지", func(t *testing.T) {
		f := hotRankedStore(t, rows)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		want := hotFullOutput(hotSuffixedLines(rows, now))
		if out.String() != want {
			t.Errorf("출력 = %q, want %q(미구현 스토어 무시)", out.String(), want)
		}
	})
}

func TestHotSnapshotCappedAtMax(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	open := make([]map[string]any, 0, 5)
	for i := 0; i < 5; i++ {
		open = append(open, synthState(i+1, 1, "합성 과업"+string(rune('가'+i)), "", now.Add(-time.Duration(i)*time.Hour)))
	}
	t.Run("기본 상한 3", func(t *testing.T) {
		rows := [][]any{{"decision", "합성토픽", "합성요약", now, int32(3), now, now}}
		f := hotStateStore(t, rows, open)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		if got := strings.Count(out.String(), "state|"); got != 3 {
			t.Errorf("state 행 = %d, want 3(open 5건 → state_snapshot_max 3)", got)
		}
		if !strings.Contains(out.String(), "state|합성 과업가|") {
			t.Errorf("최신 행 누락: %q", out.String())
		}
	})
	t.Run("meta 오버라이드 state_snapshot_max=2", func(t *testing.T) {
		rows := [][]any{{"decision", "합성토픽", "합성요약", now, int32(3), now, now}}
		f := hotStateStore(t, rows, open)
		f.queryFn = func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM hot_view"):
				return rows, nil
			case strings.Contains(sql, "WHERE key IN"):
				return [][]any{{"state_snapshot_max", "2"}}, nil
			}
			return nil, nil
		}
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		if got := strings.Count(out.String(), "state|"); got != 2 {
			t.Errorf("state 행 = %d, want 2(thresholds 경유 오버라이드)", got)
		}
	})
}

func TestHotProjectFirstThenRecent(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	open := []map[string]any{
		synthState(1, 9, "타프로젝트최신", "", now.Add(-time.Minute)),
		synthState(2, 7, "현재프로젝트오래됨", "", now.Add(-5*time.Hour)),
		synthState(3, 7, "현재프로젝트최신", "", now.Add(-time.Hour)),
		synthState(4, 9, "타프로젝트중간", "", now.Add(-2*time.Hour)),
	}
	f := hotStateStore(t, nil, open)
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "WHERE key IN"):
			return [][]any{{"state_snapshot_max", "4"}}, nil
		}
		return nil, nil
	}
	lines := hotSnapshotLines(ctx, f, 7, now)
	want := []string{
		"state|현재프로젝트최신|",
		"state|현재프로젝트오래됨|",
		"state|타프로젝트최신|",
		"state|타프로젝트중간|",
	}
	if len(lines) != len(want) {
		t.Fatalf("행 수 = %d, want %d: %v", len(lines), len(want), lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("행 %d = %q, want %q(프로젝트 7 우선→updated_at 최신순)", i, lines[i], w)
		}
	}
}

func TestHotSnapshotExcludesStale(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	open := []map[string]any{
		synthState(1, 7, "신선한 과업", "", now.Add(-13*24*time.Hour)),
		synthState(2, 7, "부패한 과업", "", now.Add(-14*24*time.Hour-time.Minute)),
	}
	f := hotStateStore(t, nil, open)
	lines := hotSnapshotLines(ctx, f, 7, now)
	if len(lines) != 1 || !strings.Contains(lines[0], "신선한 과업") {
		t.Errorf("stale 제외 결과 = %v, want 신선한 과업 1행(state_stale_days 14)", lines)
	}
}

func TestHotSnapshotRuneCap(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	long := strings.Repeat("가", 200)
	open := []map[string]any{
		synthState(1, 7, long, "", now.Add(-time.Hour)),
		synthState(2, 7, "후행 과업", "", now.Add(-2*time.Hour)),
	}
	f := hotStateStore(t, nil, open)
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"200"}}, nil
		}
		return nil, nil
	}
	lines := hotSnapshotLines(ctx, f, 7, now)
	const cap = 200 / 5
	total := 0
	for _, ln := range lines {
		total += len([]rune(ln))
	}
	if total > cap {
		t.Errorf("스냅샷 rune 총량 = %d, want ≤ %d(hot_budget 200의 20%%)", total, cap)
	}
	if len(lines) == 0 {
		t.Fatalf("행 0개 — 첫 행은 상한 내 절단 포함되어야 함: %v", lines)
	}
	if !strings.Contains(lines[0], long[:20]) {
		t.Errorf("첫 행 절단 내용 이상: %q", lines[0])
	}
}
