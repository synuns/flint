package knowledge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func TestHotProjectPriorityIntPointerRows(t *testing.T) {
	injectHotSeams(t)
	now := time.Now().UTC()
	p9, p7a, p7b := 9, 7, 7
	open := []map[string]any{
		{"id": 1, "project_id": &p9, "task": "타프로젝트최신", "next_action": "", "updated_at": now.Add(-time.Minute)},
		{"id": 2, "project_id": &p7a, "task": "현재프로젝트오래됨", "next_action": "", "updated_at": now.Add(-5 * time.Hour)},
		{"id": 3, "project_id": &p7b, "task": "현재프로젝트최신", "next_action": "", "updated_at": now.Add(-time.Hour)},
		{"id": 4, "project_id": (*int)(nil), "task": "프로젝트없음", "next_action": "", "updated_at": now.Add(-time.Minute)},
	}
	th := store.DefaultThresholds()
	th.StateSnapshotMax = 4
	lines := hotSnapshotLines(th, DefaultHotBudget, open, 7, now)
	want := []string{
		"state|현재프로젝트최신|",
		"state|현재프로젝트오래됨|",
		"state|타프로젝트최신|",
		"state|프로젝트없음|",
	}
	if len(lines) != len(want) {
		t.Fatalf("행 수 = %d, want %d: %v", len(lines), len(want), lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("행 %d = %q, want %q(*int project_id도 project 7 우선·nil은 0 취급)", i, lines[i], w)
		}
	}
}

type stateSessionFake struct {
	*fakeStore
	rec map[string]any
}

func (f *stateSessionFake) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	f.rec = rec
	return 1, "open", nil
}

func TestStateOpenRecordsSessionLabel(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()

	t.Run("--session 플래그를 session_label로 기록", func(t *testing.T) {
		f := &stateSessionFake{fakeStore: &fakeStore{}}
		var out bytes.Buffer
		if code := RunState(ctx, f, []string{"open", "--task", "합성 과업", "--project", "7", "--session", "s-9"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("state open exit = %d: %s", code, out.String())
		}
		if f.rec["session_label"] != "s-9" {
			t.Errorf("session_label = %v, want s-9", f.rec["session_label"])
		}
		if f.rec["project_id"] != 7 {
			t.Errorf("project_id = %v, want 7", f.rec["project_id"])
		}
		if got := out.String(); !strings.Contains(got, `"status":"open"`) {
			t.Errorf("출력 = %q, want {\"id\":..,\"status\":\"open\"} 기존 계약", got)
		}
	})

	t.Run("--session 미지정 시 라벨 파일 폴백", func(t *testing.T) {
		dir := t.TempDir()
		prev := wire.ConfigDir
		wire.ConfigDir = func() string { return dir }
		t.Cleanup(func() { wire.ConfigDir = prev })
		if err := os.WriteFile(filepath.Join(dir, "session-sfile.label"), []byte("s-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := &stateSessionFake{fakeStore: &fakeStore{}}
		var out bytes.Buffer
		if code := RunState(ctx, f, []string{"open", "--task", "합성 과업"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("state open exit = %d: %s", code, out.String())
		}
		if f.rec["session_label"] != "s-file" {
			t.Errorf("session_label = %v, want s-file(라벨 파일 폴백)", f.rec["session_label"])
		}
	})

	t.Run("플래그가 라벨 파일보다 우선", func(t *testing.T) {
		dir := t.TempDir()
		prev := wire.ConfigDir
		wire.ConfigDir = func() string { return dir }
		t.Cleanup(func() { wire.ConfigDir = prev })
		if err := os.WriteFile(filepath.Join(dir, "session-sfile.label"), []byte("s-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := &stateSessionFake{fakeStore: &fakeStore{}}
		var out bytes.Buffer
		if code := RunState(ctx, f, []string{"open", "--task", "합성 과업", "--session", "s-flag"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("state open exit = %d: %s", code, out.String())
		}
		if f.rec["session_label"] != "s-flag" {
			t.Errorf("session_label = %v, want s-flag(플래그 우선)", f.rec["session_label"])
		}
	})

	t.Run("빈 라벨·개행 라벨은 exit 3·미기록", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
		}{
			{"빈 값", []string{"open", "--task", "합성 과업", "--session", ""}},
			{"개행", []string{"open", "--task", "합성 과업", "--session", "a\nb"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := &stateSessionFake{fakeStore: &fakeStore{}}
				var out bytes.Buffer
				if code := RunState(ctx, f, tc.args, strings.NewReader(""), &out); code != 3 {
					t.Fatalf("exit = %d, want 3: %s", code, out.String())
				}
				if f.rec != nil {
					t.Errorf("검증 실패 후 OpenWorkState 호출됨: %v", f.rec)
				}
			})
		}
	})

	t.Run("라벨 파일 부재 시 session_label 미설정(기존 동작)", func(t *testing.T) {
		f := &stateSessionFake{fakeStore: &fakeStore{}}
		var out bytes.Buffer
		if code := RunState(ctx, f, []string{"open", "--task", "합성 과업"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("state open exit = %d: %s", code, out.String())
		}
		if _, ok := f.rec["session_label"]; ok {
			t.Errorf("session_label = %v, want 미설정(NULL)", f.rec["session_label"])
		}
	})
}

type hotProjectFake struct {
	*hotStateFake
	projectID   int
	resolveErr  error
	lastSession string
}

func (f *hotProjectFake) LatestProjectForSession(ctx context.Context, session string) (int, error) {
	f.lastSession = session
	if f.resolveErr != nil {
		return 0, f.resolveErr
	}
	return f.projectID, nil
}

func hotProjectStore(t *testing.T, rows [][]any, open []map[string]any, projectID int) *hotProjectFake {
	t.Helper()
	return &hotProjectFake{hotStateFake: hotStateStore(t, rows, open), projectID: projectID}
}

func hotProjectOpenFixture(now time.Time) []map[string]any {
	return []map[string]any{
		synthState(1, 9, "타프로젝트최신", "", now.Add(-time.Minute)),
		synthState(2, 7, "현재프로젝트오래됨", "", now.Add(-5*time.Hour)),
		synthState(3, 7, "현재프로젝트최신", "", now.Add(-time.Hour)),
	}
}

func hotProjectRowsFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		{"decision", "합성토픽", "합성요약", ts, int32(3), ts, ts},
	}
}

func TestHotProjectPriorityActive(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := hotProjectRowsFixture(now)
	open := hotProjectOpenFixture(now)

	t.Run("세션 라벨 해석 project 7 우선 정렬", func(t *testing.T) {
		SessionLabelFn = func([]string) string { return "s-7" }
		f := hotProjectStore(t, rows, open, 7)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		got := out.String()
		idxCur := strings.Index(got, "state|현재프로젝트최신")
		idxOld := strings.Index(got, "state|현재프로젝트오래됨")
		idxOther := strings.Index(got, "state|타프로젝트최신")
		if idxCur < 0 || idxOld < 0 || idxOther < 0 {
			t.Fatalf("state 행 누락: %q", got)
		}
		if !(idxCur < idxOld && idxOld < idxOther) {
			t.Errorf("state 순서 = 최신@%d 오래됨@%d 타프로젝트@%d, want project 7 2행 선행 후 타프로젝트", idxCur, idxOld, idxOther)
		}
		if f.lastSession != "s-7" {
			t.Errorf("LatestProjectForSession 세션 = %q, want s-7", f.lastSession)
		}
	})

	t.Run("--session 미지정 시 라벨 파일 폴백", func(t *testing.T) {
		SessionLabelFn = func([]string) string { return "" }
		dir := t.TempDir()
		prev := wire.ConfigDir
		wire.ConfigDir = func() string { return dir }
		t.Cleanup(func() { wire.ConfigDir = prev })
		if err := os.WriteFile(filepath.Join(dir, "session-s7.label"), []byte("s-7\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := hotProjectStore(t, rows, open, 7)
		var out bytes.Buffer
		if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
		}
		if f.lastSession != "s-7" {
			t.Fatalf("폴백 세션 = %q, want s-7", f.lastSession)
		}
		got := out.String()
		idxCur := strings.Index(got, "state|현재프로젝트최신")
		idxOther := strings.Index(got, "state|타프로젝트최신")
		if idxCur < 0 || idxOther < 0 || idxCur > idxOther {
			t.Errorf("폴백 세션으로 project 7 우선 미적용: 최신@%d 타프로젝트@%d", idxCur, idxOther)
		}
	})
}

func TestHotUnresolvedByteIdentical(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := hotProjectRowsFixture(now)
	open := hotProjectOpenFixture(now)
	want := hotFullOutput(hotSuffixedLines(rows, now)) +
		"state|타프로젝트최신|\n" +
		"state|현재프로젝트최신|\n" +
		"state|현재프로젝트오래됨|\n"

	cases := []struct {
		name      string
		label     string
		projectID int
		resolveEr error
		wantCall  bool
	}{
		{"해석 실패(매칭 없음 → 0)", "s-none", 0, nil, true},
		{"빈 세션(스킵)", "", 0, nil, false},
		{"스토어 오류(무시)", "s-err", 7, errors.New("synthetic resolve failure"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SessionLabelFn = func([]string) string { return tc.label }
			f := hotProjectStore(t, rows, open, tc.projectID)
			f.resolveErr = tc.resolveEr
			var out bytes.Buffer
			if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
			}
			if out.String() != want {
				t.Errorf("출력 = %q, want %q(미해석 시 기존 출력 바이트 동일)", out.String(), want)
			}
			if tc.wantCall && f.lastSession != tc.label {
				t.Errorf("LatestProjectForSession 세션 = %q, want %q", f.lastSession, tc.label)
			}
			if !tc.wantCall && f.lastSession != "" {
				t.Errorf("빈 세션인데 LatestProjectForSession 호출됨: %q", f.lastSession)
			}
		})
	}
}
