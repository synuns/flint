package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

type staleStateFakeStore struct {
	*fakeMaintStore
	staleRows []map[string]any
	gotDays   int
}

func (f *staleStateFakeStore) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	f.gotDays = days
	return f.staleRows
}

func TestStaleStateProposesResolve(t *testing.T) {
	f := &staleStateFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}
	f.staleRows = []map[string]any{{
		"id": int64(7), "project_id": nil, "task": "demo stale task",
		"updated_at": time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	}}

	var out strings.Builder
	code := RunMaintain(context.Background(), f, []string{"--stale-state"}, nil, &out)
	if code != 0 {
		t.Fatalf("mode exit=%d out=%s", code, out.String())
	}
	out.Reset()
	code = maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: false}, &out)
	if code != 0 {
		t.Fatalf("dry-run exit=%d out=%s", code, out.String())
	}
	if f.gotDays != store.DefaultThresholds().StateStaleDays {
		t.Errorf("StaleOpenWorkStates days = %d, want %d", f.gotDays, store.DefaultThresholds().StateStaleDays)
	}
	if f.batchResolveN != 0 {
		t.Errorf("dry-run resolved %d, want none", f.batchResolveN)
	}
	var resp struct {
		Status  string           `json:"status"`
		Days    int              `json:"days"`
		Count   int              `json:"count"`
		Items   []staleStateItem `json:"items"`
		Summary string           `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("unmarshal: %v out=%s", err, out.String())
	}
	if resp.Status != "stale-state" || resp.Count != 1 || len(resp.Items) != 1 {
		t.Fatalf("제안 = %+v, want 1건", resp)
	}
	if resp.Items[0].ID != 7 || resp.Items[0].Task != "demo stale task" {
		t.Errorf("item = %+v, want id 7 demo stale task", resp.Items[0])
	}
	if !strings.Contains(resp.Summary, "approval required") {
		t.Errorf("summary = %q, want approval required", resp.Summary)
	}

	var commitOut strings.Builder
	code = maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: true}, &commitOut)
	if code != 0 {
		t.Fatalf("commit exit=%d out=%s", code, commitOut.String())
	}
	if len(f.batchResolve) != 1 {
		t.Errorf("batch resolve calls = %d, want 1", len(f.batchResolve))
	}
	if len(f.batchResolve[0]) != 1 || f.batchResolve[0][0] != 7 {
		t.Errorf("batch ids = %v, want [7]", f.batchResolve[0])
	}
	found := false
	for _, c := range f.commits {
		if strings.Contains(c, "stale-state") {
			found = true
		}
	}
	if !found {
		t.Errorf("commits = %v, want stale-state commit", f.commits)
	}
}

func TestStaleStateBatchResolveSingleRoundtrip(t *testing.T) {
	f := &staleStateFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}
	for i := 1; i <= 5; i++ {
		f.staleRows = append(f.staleRows, map[string]any{
			"id": int64(i), "project_id": nil, "task": "demo batch task",
			"updated_at": time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		})
	}
	var out strings.Builder
	code := maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: true}, &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out.String())
	}
	if len(f.batchResolve) != 1 {
		t.Errorf("batch resolve calls = %d, want 1 (single roundtrip)", len(f.batchResolve))
	}
	if f.batchResolveN != 5 {
		t.Errorf("batch resolve ids = %v, want 5", f.batchResolve)
	}
	var resp struct {
		Resolved int `json:"resolved"`
	}
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Resolved != 5 {
		t.Errorf("resolved = %d, want 5", resp.Resolved)
	}
}

func TestStaleStateResolvedRetentionCleanup(t *testing.T) {
	f := &staleStateFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}
	f.purgeResolvedN = 1
	var out strings.Builder
	code := maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: true}, &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out.String())
	}
	if len(f.purgeCutoffs) != 1 {
		t.Fatalf("purge calls = %d, want 1", len(f.purgeCutoffs))
	}
	want := time.Now().AddDate(0, 0, -store.DefaultThresholds().WorkStateResolvedRetentionD)
	got := f.purgeCutoffs[0]
	if got.Sub(want) > time.Hour || want.Sub(got) > time.Hour {
		t.Errorf("cutoff = %v, want ~%v (retention %d days)", got, want,
			store.DefaultThresholds().WorkStateResolvedRetentionD)
	}
	if len(f.commits) == 0 {
		t.Errorf("commits = %v, want stale-state commit for purge", f.commits)
	}
}

func TestStaleStateEmptyCommitNoop(t *testing.T) {
	f := &staleStateFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}
	var out strings.Builder
	code := maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: true}, &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out.String())
	}
	if f.batchResolveN != 0 || len(f.purgeCutoffs) == 0 || len(f.commits) != 0 {
		t.Errorf("empty commit batchN=%d purge=%v commits=%v, want noop commit-free",
			f.batchResolveN, f.purgeCutoffs, f.commits)
	}
}

func TestSecretsScansWorkState(t *testing.T) {
	hit := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM work_state") {
			return [][]any{{int64(11), "demo task with ghp_abcdefghijklmnopqrst", "clean detail", "clean next"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), hit)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#work_state:11") {
		t.Errorf("work_state 발견 케이스 = %+v", c)
	}

	clean := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM work_state") {
			return [][]any{{int64(2), "demo task", "demo detail", "demo next"}}, nil
		}
		return nil, nil
	}}
	if c := checkSecrets(context.Background(), clean); c.Status != "ok" {
		t.Errorf("무해 케이스 = %+v", c)
	}

	gap := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM work_state") {
			return nil, errors.New("relation missing")
		}
		return nil, nil
	}}
	if c := checkSecrets(context.Background(), gap); c.Status != "warn" || !strings.Contains(c.Detail, "work_state query failed") {
		t.Errorf("work_state 조회 실패 케이스 = %+v", c)
	}
}

func TestStaleStateTaskRendered(t *testing.T) {
	f := &staleStateFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}
	f.staleRows = []map[string]any{{
		"id": int64(3), "project_id": nil,
		"task":       "demo [flint] marker task",
		"updated_at": time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	}}
	var out strings.Builder
	code := maintainRunStaleState(context.Background(), &maintainRequest{ms: f, commit: false}, &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out.String())
	}
	var resp struct {
		Items []staleStateItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if strings.Contains(resp.Items[0].Task, "[flint]") {
		t.Errorf("task bypassed render chokepoint: %q", resp.Items[0].Task)
	}
	if !strings.Contains(resp.Items[0].Task, "marker task") {
		t.Errorf("task = %q, want folded marker task", resp.Items[0].Task)
	}
}

func TestSecretsScansResolvedWorkState(t *testing.T) {
	var workStateSQL string
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "origin") {
			return [][]any{{int64(1), "토픽", "무해한 요약", "", "", "", ""}}, nil
		}
		if strings.Contains(sql, "FROM work_state") {
			workStateSQL = sql
			return [][]any{{int64(21), "resolved task with ghp_abcdefghijklmnopqrst", "clean", "clean"}}, nil
		}
		return nil, nil
	}}
	c := checkSecrets(context.Background(), f)
	if c.Status != "warn" || !strings.Contains(c.Detail, "github-token#work_state:21") {
		t.Errorf("resolved 행 발견 = %+v", c)
	}
	if strings.Contains(workStateSQL, "status = 'open'") {
		t.Errorf("work_state 스캔이 open 한정 — resolved 누락: %s", workStateSQL)
	}
}

func TestThresholdsRegistered(t *testing.T) {
	m, ok := store.ThresholdMetaByKey("state_stale_days")
	if !ok {
		t.Fatal("state_stale_days 미등록")
	}
	if m.Field != "StateStaleDays" || m.Desc != "maintain --stale-state" {
		t.Errorf("meta = %+v", m)
	}
	if got := store.DefaultThresholds().StateStaleDays; got != 14 {
		t.Errorf("StateStaleDays default = %d, want 14", got)
	}

	m, ok = store.ThresholdMetaByKey("state_snapshot_max")
	if !ok {
		t.Fatal("state_snapshot_max 미등록")
	}
	if m.Field != "StateSnapshotMax" || m.Desc != "hot" {
		t.Errorf("meta = %+v", m)
	}
	if got := store.DefaultThresholds().StateSnapshotMax; got != 3 {
		t.Errorf("StateSnapshotMax default = %d, want 3", got)
	}

	m, ok = store.ThresholdMetaByKey("work_state_resolved_retention_days")
	if !ok {
		t.Fatal("work_state_resolved_retention_days 미등록")
	}
	if m.Field != "WorkStateResolvedRetentionD" || m.Desc != "maintain --stale-state resolved cleanup" {
		t.Errorf("meta = %+v", m)
	}
	if got := store.DefaultThresholds().WorkStateResolvedRetentionD; got != 90 {
		t.Errorf("WorkStateResolvedRetentionD default = %d, want 90", got)
	}

	th, err := store.ParseThresholds([][2]any{
		{"state_stale_days", "7"}, {"state_snapshot_max", "5"},
		{"work_state_resolved_retention_days", "30"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds: %v", err)
	}
	if th.StateStaleDays != 7 || th.StateSnapshotMax != 5 || th.WorkStateResolvedRetentionD != 30 {
		t.Errorf("override = %d/%d/%d, want 7/5/30",
			th.StateStaleDays, th.StateSnapshotMax, th.WorkStateResolvedRetentionD)
	}
}
