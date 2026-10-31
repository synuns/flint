package cli

import (
	"context"
	"testing"
)

func TestConflictsZeroOk(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 그룹 0
	}})
	if c.Status != "ok" {
		t.Errorf("0그룹 = %s, want ok", c.Status)
	}
}

func TestConflictsWarn(t *testing.T) {
	c := checkConflicts(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{"topic-a"}}, nil
	}})
	if c.Status != "warn" {
		t.Errorf("1그룹 = %s, want warn", c.Status)
	}
}
