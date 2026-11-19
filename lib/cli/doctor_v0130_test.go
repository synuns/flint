package cli

import (
	"context"
	"testing"
)

func TestConflictsZeroOk(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 0 groups
	}})
	if c.Status != "ok" {
		t.Errorf("0그룹 = %s, want ok", c.Status)
	}
}

func TestConflictsWarn(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{"topic-a", []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}}}, nil
	}})
	if c.Status != "warn" {
		t.Errorf("모순 1그룹 = %s, want warn", c.Status)
	}
}

// TestConflictsDupOnlyOk — regression: a purely duplicate group (similar summaries) is not a
// contradiction candidate, so it does not warn.
func TestConflictsDupOnlyOk(t *testing.T) {
	same := "flint 백업은 매일 자동이다"
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{"backup-policy", []string{same, same}},
			{"port-config", []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}, nil
	}})
	if c.Status != "warn" {
		t.Errorf("혼합 그룹 = %s, want warn", c.Status)
	}
	c2 := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{"backup-policy", []string{same, same}}}, nil
	}})
	if c2.Status != "ok" {
		t.Errorf("순수 중복 그룹 = %s, want ok(T2 출력과 동치)", c2.Status)
	}
}
