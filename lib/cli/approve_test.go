package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"flint/lib/db"
)

// fakeApprove — T17 admin Approve fake. errErr 미지정 시 성공,
// SUPERLITE_REAL=1이면 실 Repo에 위임(실DB 미기동 시 스킵).
type fakeApprove struct {
	err     error
	gotID   int
	calledN int
}

// withApproveFake — approveFn seam 교체. 기본 fake, FLINT_TEST_DSN 설정 +
// SUPERLITE_REAL=1이면 stub 미설정 시 실구현 위임(위임 DSN도 FLINT_TEST_DSN).
// FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(N1 — seamRealDSN).
func withApproveFake(t *testing.T, f *fakeApprove) {
	t.Helper()
	prev := approveFn
	dsn, real := seamRealDSN()
	if real {
		repo, err := db.NewRepo(context.Background(), dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		repo.Close()
	}
	approveFn = func(ctx context.Context, id int) error {
		f.calledN++
		f.gotID = id
		if f.err != nil {
			return f.err
		}
		if !real {
			return nil
		}
		repo, err := db.NewRepo(ctx, dsn)
		if err != nil {
			return err
		}
		defer repo.Close()
		return repo.Approve(ctx, id)
	}
	t.Cleanup(func() { approveFn = prev })
}

// mustApprove — Lookup("approve") 경로 실행(등록·디스패치 동시 검증).
func mustApprove(t *testing.T, args []string) (string, int) {
	t.Helper()
	h, ok := Lookup("approve")
	if !ok {
		t.Fatal("approve 미등록")
	}
	var out bytes.Buffer
	code := h(context.Background(), nil, args, strings.NewReader(""), &out)
	return out.String(), code
}

// TestApproveOK — 성공 시 {"status":"approved","id":N} exit 0(Review Focus: exit 매핑).
func TestApproveOK(t *testing.T) {
	f := &fakeApprove{}
	withApproveFake(t, f)
	out, code := mustApprove(t, []string{"--id", "7"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"approved","id":7}` {
		t.Errorf("out = %q, want approved JSON", out)
	}
	if f.gotID != 7 || f.calledN != 1 {
		t.Errorf("Approve 호출 (id=%d, n=%d), want (7, 1)", f.gotID, f.calledN)
	}
}

// TestApproveMissing — ErrNotFound → 한 줄 JSON 에러 exit 1.
func TestApproveMissing(t *testing.T) {
	withApproveFake(t, &fakeApprove{err: db.ErrNotFound})
	out, code := mustApprove(t, []string{"--id", "999"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("한 줄이어야 함: %q", out)
	}
	if !strings.Contains(lines[0], `"error":"not_found"`) {
		t.Errorf("out = %q, want not_found 에러", out)
	}
}

// TestApproveUsage — --id 누락·비정수·알 수 없는 플래그 → exit 1.
func TestApproveUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no flag", nil},
		{"missing value", []string{"--id"}},
		{"non int", []string{"--id", "abc"}},
		{"unknown flag", []string{"--wat", "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withApproveFake(t, &fakeApprove{})
			out, code := mustApprove(t, tc.args)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
			}
			if !strings.Contains(out, `"error"`) {
				t.Errorf("out = %q, want 에러 JSON", out)
			}
		})
	}
}

// TestApproveOtherError — 일반 에러는 errLine 위임·MapExit exit 1.
func TestApproveOtherError(t *testing.T) {
	withApproveFake(t, &fakeApprove{err: errors.New("boom")})
	out, code := mustApprove(t, []string{"--id", "3"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("out = %q, want 에러 JSON", out)
	}
}
