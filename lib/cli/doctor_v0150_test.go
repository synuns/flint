package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestZeroNearWarn(t *testing.T) {

	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":20,"read_zero_near":8}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "warn" {
		t.Errorf("40%% = %s, want warn", c.Status)
	}
}

func TestZeroNearSampleFloorOk(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	c := checkZeroNear(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "daily:") {
			return [][]any{{"daily:" + today, `{"read":5,"read_zero_near":3}`}}, nil
		}
		return nil, nil
	}})
	if c.Status != "ok" {
		t.Errorf("표본 5 = %s, want ok", c.Status)
	}
}
