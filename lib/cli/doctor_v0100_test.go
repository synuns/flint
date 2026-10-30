package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func backupAgeQueryFn(lastISO string, fail bool) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_backup") {
			if fail {
				return nil, context.DeadlineExceeded
			}
			if lastISO == "" {
				return nil, nil
			}
			return [][]any{{"last_backup", lastISO}}, nil
		}
		return nil, nil
	}
}

func TestBackupAgeFresh(t *testing.T) {
	fresh := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn(fresh, false)})
	if c.Status != "ok" {
		t.Errorf("1시간 경과 = %s, want ok (%s)", c.Status, c.Detail)
	}
}

func TestBackupAgeStale(t *testing.T) {
	stale := time.Now().UTC().Add(-73 * time.Hour).Format(time.RFC3339)
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn(stale, false)})
	if c.Status != "warn" {
		t.Errorf("73시간 경과 = %s, want warn", c.Status)
	}
}

func TestBackupAgeMissing(t *testing.T) {
	c := checkBackupAge(context.Background(), &fakeStore{queryFn: backupAgeQueryFn("", false)})
	if c.Status != "warn" {
		t.Errorf("미설정 = %s, want warn", c.Status)
	}
	c = checkBackupAge(context.Background(), nil)
	if c.Status != "warn" {
		t.Errorf("nil store = %s, want warn", c.Status)
	}
}
