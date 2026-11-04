package cli

import (
	"context"
	"strings"
	"testing"
)

func TestZeroNearWarn(t *testing.T) {
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:2001-01-01", `{"read":20,"read_zero_near":8}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "warn" {
		t.Errorf("40%% = %s, want warn", c.Status)
	}
}

func TestZeroNearSampleFloorOk(t *testing.T) {
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:2001-01-01", `{"read":5,"read_zero_near":3}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "ok" {
		t.Errorf("표본 5 = %s, want ok", c.Status)
	}
}
