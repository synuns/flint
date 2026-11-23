package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDoctorMentionsExpire(t *testing.T) {
	old := time.Now().Add(-10 * 24 * time.Hour)
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "pending") {
			t.Errorf("예상 밖 조회: %s", sql)
		}
		return [][]any{{old}}, nil
	}}
	c := checkPendingAge(context.Background(), f)
	if c.Status != "warn" {
		t.Fatalf("status=%q, want warn (detail %q)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "expire-pending") {
		t.Errorf("detail에 만료 명령 안내 없음: %q", c.Detail)
	}

	fresh := time.Now().Add(-1 * 24 * time.Hour)
	f2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{fresh}}, nil
	}}
	c2 := checkPendingAge(context.Background(), f2)
	if c2.Status != "ok" {
		t.Errorf("신선 pending status=%q, want ok (detail %q)", c2.Status, c2.Detail)
	}

	f3 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	c3 := checkPendingAge(context.Background(), f3)
	if c3.Status != "ok" {
		t.Errorf("pending 없음 status=%q, want ok (detail %q)", c3.Status, c3.Detail)
	}
}
