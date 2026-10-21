package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"flint/lib/db"
	"flint/lib/store"
)

// fakeMaint — fakeStore에 admin 연산(Counts/Invalidate/Merge)을 얹은 fake.
type fakeMaint struct {
	*fakeStore

	counts    db.Counts
	countsErr error

	invalErr error
	mergeErr error

	invalidateCalls []int
	invalidateMsgs  []string
	mergeCalls      [][2]int
}

func (f *fakeMaint) Counts(ctx context.Context) (db.Counts, error) {
	return f.counts, f.countsErr
}

func (f *fakeMaint) Invalidate(ctx context.Context, id int, reason string) error {
	f.invalidateCalls = append(f.invalidateCalls, id)
	f.invalidateMsgs = append(f.invalidateMsgs, reason)
	return f.invalErr
}

func (f *fakeMaint) Merge(ctx context.Context, fromID, toID int) error {
	f.mergeCalls = append(f.mergeCalls, [2]int{fromID, toID})
	return f.mergeErr
}

func runMaintainArgs(t *testing.T, f *fakeMaint, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runMaintain(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// Review Focus(state): 경계 — pending 49/50은 ok, 51부터 review-needed.
// 임계값은 meta에서 읽는다(기본 pending_max=50).
func TestMaintainCheckBoundary(t *testing.T) {
	cases := []struct {
		name    string
		pending int
		want    string
	}{
		{"49 ok", 49, "ok"},
		{"50 경계 ok", 50, "ok"},
		{"51 review-needed", 51, "review-needed"},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{Pending: c.pending}}
		out, code := runMaintainArgs(t, f, "--check")
		if code != 0 {
			t.Errorf("%s: exit=%d, want 0", c.name, code)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
			t.Fatalf("%s: JSON 아님: %q", c.name, out)
		}
		if m["status"] != c.want {
			t.Errorf("%s: status=%v, want %q", c.name, m["status"], c.want)
		}
		if c.want == "review-needed" {
			if got, _ := m["pending"].(float64); int(got) != c.pending {
				t.Errorf("%s: pending 필드=%v, want %d", c.name, m["pending"], c.pending)
			}
		}
	}
}

// dup_suspect 경계(기본 20) — 20 ok, 21 review-needed.
func TestMaintainCheckDupBoundary(t *testing.T) {
	for _, c := range []struct {
		dup  int
		want string
	}{{20, "ok"}, {21, "review-needed"}} {
		f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{DupSuspect: c.dup}}
		out, code := runMaintainArgs(t, f, "--check")
		if code != 0 {
			t.Errorf("dup=%d: exit=%d, want 0", c.dup, code)
		}
		if !strings.Contains(out, `"status":"`+c.want+`"`) {
			t.Errorf("dup=%d: 출력 %q, want %q", c.dup, out, c.want)
		}
	}
}

// 임계값을 meta에서 읽는다 — pending_max=5면 pending 6이 review-needed.
func TestMaintainCheckThresholdsFromMeta(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			return [][]any{{"pending_max", "5"}}, nil
		},
	}, counts: db.Counts{Pending: 6}}
	out, code := runMaintainArgs(t, f, "--check")
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"review-needed"`) {
		t.Errorf("meta 임계값 미반영: %q", out)
	}
}

// review-needed 보고는 양쪽 수치를 모두 포함하고 exit 0이다.
func TestMaintainCheckReportFields(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, counts: db.Counts{Pending: 60, DupSuspect: 25}}
	out, code := runMaintainArgs(t, f, "--check")
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m["pending"] != float64(60) || m["dup_suspect"] != float64(25) {
		t.Errorf("필드 누락: %q", out)
	}
}

// --invalidate --id N --reason — 갱신 경로 호출 + exit 0.
func TestMaintainInvalidateOK(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--invalidate", "--id", "7", "--reason", "중복")
	if code != 0 {
		t.Errorf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.invalidateCalls) != 1 || f.invalidateCalls[0] != 7 {
		t.Errorf("Invalidate 호출 = %v, want [7]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 1 || f.invalidateMsgs[0] != "중복" {
		t.Errorf("reason 전달 누락: %v", f.invalidateMsgs)
	}
	if !strings.Contains(out, `"status":"invalidated"`) {
		t.Errorf("출력 %q", out)
	}
}

// 없는 id 무효화 — ErrNotFound → exit 1 한 줄 JSON.
func TestMaintainInvalidateNotFound(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, invalErr: db.ErrNotFound}
	out, code := runMaintainArgs(t, f, "--invalidate", "--id", "99", "--reason", "x")
	if code != 1 {
		t.Errorf("exit=%d, want 1", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}
}

// --merge --from N --to M — 병합 경로 호출 + exit 0.
func TestMaintainMergeOK(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--merge", "--from", "3", "--to", "9")
	if code != 0 {
		t.Errorf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.mergeCalls) != 1 || f.mergeCalls[0] != [2]int{3, 9} {
		t.Errorf("Merge 호출 = %v, want [[3 9]]", f.mergeCalls)
	}
	if !strings.Contains(out, `"status":"merged"`) {
		t.Errorf("출력 %q", out)
	}
}

// --merge 대상 부재 — ErrNotFound → exit 1.
func TestMaintainMergeNotFound(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, mergeErr: db.ErrNotFound}
	_, code := runMaintainArgs(t, f, "--merge", "--from", "1", "--to", "2")
	if code != 1 {
		t.Errorf("exit=%d, want 1", code)
	}
}

// 모드·인자 누락은 사용법 에러 exit 1.
func TestMaintainUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"--id", "5"},
		{"--invalidate"},             // --id 없음
		{"--merge", "--from", "3"},   // --to 없음
		{"--check", "--bogus"},       // 알 수 없는 플래그
		{"--check", "--id", "abc"},   // 숫자 아님 — id는 check에서 무시되지만 파싱은 실패해야
	}
	for _, args := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		out, code := runMaintainArgs(t, f, args...)
		if code != 1 {
			t.Errorf("%v: exit=%d, want 1 (출력 %q)", args, code, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%v: 에러 JSON 아님: %q", args, out)
		}
	}
}

// Counts 실패 — errLine 전파·MapExit 코드.
func TestMaintainCheckCountError(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}, countsErr: errors.New("boom")}
	out, code := runMaintainArgs(t, f, "--check")
	if code == 0 {
		t.Errorf("exit=0, want != 0 (출력 %q)", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("에러 미전파: %q", out)
	}
}

// 등록 확인 — registry에 maintain이 있어야 Run이 디스패치한다.
func TestMaintainRegistered(t *testing.T) {
	if _, ok := Lookup("maintain"); !ok {
		t.Fatal("maintain 미등록")
	}
}

// 스키마 버전 게이트 — realStore는 MaintainStore를 만족해야 한다(컴파일 보증).
var _ MaintStore = (*realStore)(nil)

var _ = store.DefaultThresholds // import 방지(향후 확장용)
