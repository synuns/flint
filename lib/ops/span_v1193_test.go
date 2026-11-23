package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func TestStallDetailGraceOverflow(t *testing.T) {
	seen := map[string]time.Time{"last_hot_at": time.Now().Add(-time.Hour)}
	warns := 0
	if got := stallDetail(seen, "last_hot_at", 3000000, &warns); got != "" || warns != 0 {
		t.Fatalf("stallDetail(grace 3000000, 1h) = %q warns=%d, want 무경고(클램프 — 랩 시 항상 경고)", got, warns)
	}
	seen["last_hot_at"] = time.Now().Add(-100 * time.Hour)
	if got := stallDetail(seen, "last_hot_at", 48, &warns); got == "" || warns != 1 {
		t.Fatalf("stallDetail(grace 48, 100h) = %q warns=%d, want 경고(통상 동작 불변)", got, warns)
	}
}

func TestStallWarningsGraceOverflow(t *testing.T) {
	th := store.DefaultThresholds()
	th.StallHotH = 3000000
	th.StallWriteH = 3000000
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_hot_at") {
			return [][]any{
				{"last_hot_at", time.Now().Add(-time.Hour)},
				{"last_write_at", time.Now().Add(-time.Hour)},
			}, nil
		}
		return nil, nil
	}}
	if warns := stallWarnings(context.Background(), f, th); len(warns) != 0 {
		t.Fatalf("stallWarnings(grace 3000000) = %v, want 없음(클램프)", warns)
	}
}

func TestBackupAgeWarnHOverflow(t *testing.T) {
	th := store.DefaultThresholds()
	th.BackupAgeWarnH = 3000000
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_backup") {
			return [][]any{{"last_backup", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}}, nil
		}
		return nil, nil
	}}
	c := checkBackupAgeTh(context.Background(), f, th)
	if c.Status != "ok" {
		t.Fatalf("backup-age status=%q detail=%q, want ok(클램프 — 랩 시 즉시 경고)", c.Status, c.Detail)
	}
}

func TestChainWindowOverflow(t *testing.T) {
	now := time.Now().UTC()

	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "fallback, ids") {
			return [][]any{
				{now.Add(-2 * time.Hour), false, false, "1,2", 1, 400, "", 2},
				{now.Add(-1 * time.Hour), false, false, "1,2", 1, 400, "", 2},
			}, nil
		}
		return dispatchReportQuery(sql)
	}}
	th := store.DefaultThresholds()
	th.ChainWindowH = 3000000
	out := &ReportOutput{}
	if err := collectEffect(context.Background(), f, 3, th, out); err != nil {
		t.Fatalf("collectEffect: %v", err)
	}
	if out.Effect.ChainPct != 100 {
		t.Fatalf("ChainPct(chain_window_h=3000000) = %d, want 100(클램프 — 랩 시 0)", out.Effect.ChainPct)
	}
}
