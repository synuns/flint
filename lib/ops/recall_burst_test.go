package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

func burstFixture(rows [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "vault_files vf") {
			return rows, nil
		}
		return nil, nil
	}}
}

func burstRow(id int64, topic string, bad int64, ref time.Time, conditions string, changed bool) []any {
	return []any{id, topic, bad, ref, conditions, changed}
}

func burstSQL(t *testing.T, st *fakeStore) string {
	t.Helper()
	for _, q := range st.queries {
		if strings.Contains(q, "vault_files vf") {
			return q
		}
	}
	t.Fatalf("파열 선별 쿼리 미실행")
	return ""
}

func TestBurstStaleCondition(t *testing.T) {
	old := time.Now().UTC().Add(-200 * 24 * time.Hour)
	st := burstFixture([][]any{
		burstRow(42, "synthetic stale cond topic", 0, old, "env:prod", false),
		burstRow(7, "synthetic fresh cond topic", 0, time.Now().UTC(), "env:prod", false),
	})
	items, err := RecallBurstItems(context.Background(), st, 5)
	if err != nil {
		t.Fatalf("RecallBurstItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (조건 태그+stale 증거 동시 충족만 선별): %+v", len(items), items)
	}
	if items[0].ID != 42 || items[0].Reason != "stale-condition" {
		t.Fatalf("선별 행 오류: %+v", items[0])
	}
	sql := burstSQL(t, st)
	for _, frag := range []string{"conditions", "content_changed_at", "recall_events rb"} {
		if !strings.Contains(sql, frag) {
			t.Errorf("선별 SQL에 %q 신호 부재", frag)
		}
	}
}

func TestBurstVaultChanged(t *testing.T) {
	recent := time.Now().UTC().Add(-3 * 24 * time.Hour)
	st := burstFixture([][]any{
		burstRow(11, "synthetic vault changed topic", 0, recent, "", true),
		burstRow(12, "synthetic plain topic", 0, recent, "", false),
	})
	items, err := RecallBurstItems(context.Background(), st, 5)
	if err != nil {
		t.Fatalf("RecallBurstItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (vault 원본 변경 행만): %+v", len(items), items)
	}
	if items[0].ID != 11 || items[0].Reason != "vault-changed" {
		t.Fatalf("선별 행 오류: %+v", items[0])
	}
}

func TestBurstOrderLimit(t *testing.T) {
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	older := time.Now().UTC().Add(-300 * 24 * time.Hour)
	fresh := time.Now().UTC().Add(-2 * 24 * time.Hour)
	st := burstFixture([][]any{
		burstRow(1, "synthetic bad 3 old", 3, old, "", false),
		burstRow(2, "synthetic bad 3 older", 3, older, "", false),
		burstRow(3, "synthetic bad 1", 1, older, "", false),
		burstRow(4, "synthetic bad 5 fresh", 5, fresh, "", false),
	})
	items, err := RecallBurstItems(context.Background(), st, 3)
	if err != nil {
		t.Fatalf("RecallBurstItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("limit 절삭 실패: %d items", len(items))
	}
	want := []int{4, 2, 1}
	for i, w := range want {
		if items[i].ID != w {
			t.Fatalf("순위 %d: id=%d, want %d (bad_recall 내림차순·age 내림차순)", i, items[i].ID, w)
		}
	}

	empty, err := RecallBurstItems(context.Background(), &fakeStore{}, 5)
	if err != nil || len(empty) != 0 {
		t.Fatalf("빈 저장소: items=%v err=%v", empty, err)
	}
}
