// span_v1193_test.go — the v1.19.3 hour/minute-unit span clamp regressions
// (R77-R78) at the ops seams: a stall_hot_h/stall_write_h/backup_age_warn_h/
// chain_window_h override past the Duration ceiling (e.g. 3000000) clamps to
// the 87600h ceiling via db.HoursSpan — the pre-fix
// time.Duration(h)*time.Hour wrapped negative, so a 1h-old heartbeat read as
// stalled, a 1h-old backup as over-age, and no chain could ever count.
package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

// TestStallDetailGraceOverflow — doctor's stall grace: a 3000000h override
// with a 1h-old stamp stays silent (clamped to 10 years); the normal 48h
// grace still warns at 100h elapsed.
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

// TestStallWarningsGraceOverflow — maintain's stall warnings under the same
// override: a 1h-old last_hot_at/last_write_at yields no warnings.
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

// TestBackupAgeWarnHOverflow — doctor backup-age: a 3000000h
// backup_age_warn_h override keeps a 1h-old backup ok.
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

// TestChainWindowOverflow — the report chain verdict under a chain_window_h
// override past the ceiling, at the collectEffect site itself: the
// db.HoursSpan-clamped window still counts chains (the wrapped-negative
// window broke out of both the re-return and the write scan immediately —
// ChainPct 0, no usage chain could ever register).
func TestChainWindowOverflow(t *testing.T) {
	now := time.Now().UTC()
	// the dispatch fixture with id-bearing reads 2h and 1h old (a 1h-later
	// re-return of the same ids — a chain under any sane window).
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
