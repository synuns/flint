package cli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/synuns/flint/lib/query"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHotHeadline(t *testing.T) {
	if got := hotHeadline("normal", 20, 7, 3, nil); got != "[flint] active 20 · pending 7 · recent decisions 3 — expand: flint hot --full" {
		t.Errorf("normal 헤드라인 = %q", got)
	}
	if got := hotHeadline("minimal", 20, 7, 3, []string{"x"}); got != "[flint] active 20 · pending 7" {
		t.Errorf("minimal 헤드라인 = %q", got)
	}
	if got := hotHeadline("other", 1, 2, 3, nil); !strings.Contains(got, "expand") {
		t.Errorf("미확인 프로파일 = normal 취급 아님: %q", got)
	}
}

func hotProfileQueryFn(profile string, hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 && fmt.Sprint(args[0]) == "exposure_profile":
			if profile == "" {
				return nil, nil
			}
			return [][]any{{profile}}, nil
		}
		return nil, nil
	}
}

func TestHotSummaryHeadlineByProfile(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"project", "p1", "active", "2026-10-23"},
		{"pending", "토픽", "요약", "2026-10-22"},
		{"pending", "토픽2", "요약2", "2026-10-21"},
		{"decision", "토픽3", "요약3", "2026-10-20"},
	}
	cases := []struct{ name, profile, want string }{
		{"미설정 normal", "", "[flint] active 1 · pending 2 · recent decisions: 토픽3 — recall: flint read <query>"},
		{"minimal", "minimal", "[flint] active 1 · pending 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			f := &fakeStore{queryFn: hotProfileQueryFn(tc.profile, hotRows)}
			if code := runHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("runHot exit = %d, want 0", code)
			}
			if got := strings.TrimSpace(out.String()); got != tc.want {
				t.Fatalf("헤드라인 = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHotFullUnchanged(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22"},
		{"decision", "토픽2", "요약2", "2026-10-21"},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if want := query.FormatRows(hotRows, hotFields, false); out.String() != want {
		t.Fatalf("--full 출력 = %q, want %q(기존 전체 출력)", out.String(), want)
	}

	var out2 bytes.Buffer
	f2 := &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}
	if code := runHot(ctx, f2, []string{"--session", "S1", "--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot --session --full exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Fatalf("--session 병용 시 --full 출력 상이: %q vs %q", out2.String(), out.String())
	}
}

func TestPendingAgeSuffix(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if got := pendingAgeSuffix(now.Add(-30*time.Hour), now); got != "·+1d" {
		t.Errorf("30시간 = %q, want ·+1d", got)
	}
	if got := pendingAgeSuffix(now.Add(-2*time.Hour), now); got != "" {
		t.Errorf("2시간 = %q, want 빈 문자열", got)
	}
	if got := pendingAgeSuffix(now.Add(-49*time.Hour), now); got != "·+2d" {
		t.Errorf("49시간 = %q, want ·+2d", got)
	}
}

func pendingAgeQueryFn(hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		}
		return nil, nil
	}
}

func TestHotFullPendingAgeAppended(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약", validAt, int32(3), validAt, validAt},
		{"decision", "토픽2", "요약2", validAt, int32(7), validAt, validAt},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	gotLines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantLines := strings.Split(strings.TrimSuffix(query.FormatRows(hotRows, hotFields, false), "\n"), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("행 수 = %d, want %d: %q", len(gotLines), len(wantLines), out.String())
	}

	if gotLines[0] != wantLines[0]+"·+2d" {
		t.Errorf("pending 행 = %q, want %q+·+2d", gotLines[0], wantLines[0])
	}
	if gotLines[1] != wantLines[1] {
		t.Errorf("decision 행 변형 = %q, want %q", gotLines[1], wantLines[1])
	}
}

func TestHotFullPendingAgeOmittedWithoutValidAt(t *testing.T) {
	ctx := context.Background()
	noCell := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	notTime := [][]any{{"pending", "토픽", "요약", "2026-10-22", int32(3), "not-time", nil}}
	want := query.FormatRows(noCell, hotFields, false)
	for name, qfn := range map[string]func(string, []any) ([][]any, error){
		"valid_at 셀 부재":  pendingAgeQueryFn(noCell),
		"time.Time 아닌 값": pendingAgeQueryFn(notTime),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			f := &fakeStore{queryFn: qfn}
			if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("runHot --full exit = %d, want 0", code)
			}
			if out.String() != want {
				t.Fatalf("출력 = %q, want %q(기존 전체 출력 승계)", out.String(), want)
			}
		})
	}
}

func hotN3QueryFn(hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		}
		return nil, nil
	}
}

func TestHotHeadlineSkipsReadBump(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(hotRows)}}
	var out bytes.Buffer
	if code := runHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if len(f.bumped) != 0 {
		t.Errorf("헤드라인 경로 BumpReads 호출 금지(실제 주입 없음), got %v", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]](유입 1회 기록은 유지)", f.usage)
	}
	if !strings.Contains(out.String(), "[flint]") {
		t.Errorf("헤드라인 미출력: %q", out.String())
	}
}

func TestHotFullStillBumpsInjected(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(hotRows)}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 2 ||
		f.bumped[0][0] != 3 || f.bumped[0][1] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 7](--full 주입 지식 전수)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}

func TestHotFullSuffixSurvivorMatchesOutput(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-72 * time.Hour)
	hotRows := [][]any{
		{"pending", "펜딩토픽", "요약", validAt, int32(10), validAt, validAt},
		{"decision", "잘린토픽", "요약2", "2026-10-21", int32(11), nil, nil},
		{"decision", "더잘린토픽", "요약3", "2026-10-20", int32(12), nil, nil},
	}
	suffix := "·+3d"
	baseLines := strings.Split(strings.TrimSuffix(
		query.FormatRows(hotRows, hotFields, false), "\n"), "\n")

	budget := len([]rune(baseLines[0])) + len([]rune(suffix)) + 1
	qfn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 &&
			fmt.Sprint(args[0]) == "hot_budget":
			return [][]any{{strconv.Itoa(budget)}}, nil
		}
		return nil, nil
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: qfn}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}

	if !strings.Contains(out.String(), "펜딩토픽") || !strings.Contains(out.String(), suffix) {
		t.Fatalf("첫 행(접미 포함) 미노출: %q", out.String())
	}
	if strings.Contains(out.String(), "잘린토픽") || strings.Contains(out.String(), "더잘린토픽") {
		t.Fatalf("예산 절단된 행이 출력에 남음: %q", out.String())
	}

	if len(f.bumped) != 1 || len(f.bumped[0]) != 1 || f.bumped[0][0] != 10 {
		t.Errorf("BumpReads ids = %v, want [10](접미 포함 생존 행만 — 잘린 id 11·12 제외)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}

func TestHotHeadlineTopics(t *testing.T) {
	withT := []string{"doltgres 포트 충돌", "CI 멱등 가드", "seed DSN 가드"}
	got := hotHeadline("normal", 5, 2, 3, withT)
	want := "[flint] active 5 · pending 2 · recent decisions: doltgres 포트 충돌 · CI 멱등 가드 · seed DSN 가드 — recall: flint read <query>"
	if got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}

	if got := hotHeadline("normal", 5, 2, 0, nil); got != "[flint] active 5 · pending 2 · recent decisions 0 — expand: flint hot --full" {
		t.Fatalf("빈 주제 폴백=%q", got)
	}

	if got := hotHeadline("minimal", 5, 2, 3, withT); got != "[flint] active 5 · pending 2" {
		t.Fatalf("minimal=%q", got)
	}
}

func TestHotTopicsFromRows(t *testing.T) {
	long := strings.Repeat("주", 20)
	rows := [][]any{
		{"project", "p1", "s", "2026-11-06"},
		{"decision", "최신 결정", "s", "2026-11-06"},
		{"decision", "최신 결정", "s2", "2026-11-05"},
		{"decision", long, "s", "2026-11-04"},
		{"decision", "네번째", "s", "2026-11-03"},
		{"pending", "p2", "s", "2026-11-02"},
	}
	got := hotTopics(rows, 3)
	if len(got) != 3 {
		t.Fatalf("주제 수=%d, want 3: %v", len(got), got)
	}
	if got[0] != "최신 결정" {
		t.Fatalf("첫 주제=%q, want 최신순", got[0])
	}
	if got[1] != string([]rune(long)[:16])+"…" {
		t.Fatalf("절산 주제=%q, want 16rune+…", got[1])
	}
	if got[2] != "네번째" {
		t.Fatalf("셋째 주제=%q", got[2])
	}
}

func TestHotHeadlineRunPath(t *testing.T) {
	hotRows := [][]any{
		{"decision", "검색 품질 트리거", "요약", "2026-11-05"},
		{"project", "p1", "active", "2026-11-04"},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: hotProfileQueryFn("", hotRows)}
	if code := runHot(context.Background(), f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d", code)
	}
	want := "[flint] active 1 · pending 0 · recent decisions: 검색 품질 트리거 — recall: flint read <query>"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}
}

type hotEventRecorder struct {
	*fakeStore
	events []string
}

func (h *hotEventRecorder) RecordHotEvent(ctx context.Context, ids string) error {
	h.events = append(h.events, ids)
	return nil
}

func TestHotFullRecordsExposure(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.events) == 0 {
		t.Fatal("--full 경로가 hot_events를 기록하지 않음")
	}

	for _, ev := range st.events {
		if ids := parseIDsCSV(ev); len(ids) == 0 {
			t.Errorf("노출 기록 %q가 id로 파싱 불가", ev)
		}
	}
}

func TestHotHeadlineNoExposureRecord(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.events) != 0 {
		t.Errorf("헤드라인 경로 노출 기록 = %v, want 없음(주입 없음)", st.events)
	}
}

func TestHotHeadlineLimitWidened(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d, want 0; out=%s", code, out.String())
	}
	found := false
	for _, q := range st.queries {
		if strings.Contains(q, "FROM hot_view") {
			found = true
			if !strings.Contains(q, "LIMIT 50") {
				t.Errorf("헤드라인 쿼리 상한 = %q, want LIMIT 50(카운트 왜곡 방지)", q)
			}
		}
	}
	if !found {
		t.Fatal("hot_view 조회 미관측")
	}

	st2 := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out2 strings.Builder
	if code := runHot(ctx, st2, []string{"--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot --full exit=%d, want 0; out=%s", code, out2.String())
	}
	for _, q := range st2.queries {
		if strings.Contains(q, "FROM hot_view") && strings.Contains(q, "LIMIT 50") {
			t.Errorf("--full 주입 경로가 헤드라인 상한 사용: %q(예산 계약 위반)", q)
		}
	}
}
