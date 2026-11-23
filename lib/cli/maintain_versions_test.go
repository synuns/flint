package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStaleCutoffTimestamptzParam(t *testing.T) {
	var staleSQL string
	var staleArgs []any
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		staleSQL, staleArgs = sql, args
		return nil, nil
	}
	if _, code := runMaintainArgs(t, f, "--stale", "--days", "30"); code != 0 {
		t.Fatalf("exit=%d, want 0; out 확인", code)
	}
	if staleSQL == "" {
		t.Fatal("stale 조회 미발생")
	}
	if strings.Contains(staleSQL, "interval") {
		t.Errorf("SQL에 interval 연산 잔존(Doltgres $1 interval 추론 결함): %q", staleSQL)
	}
	if len(staleArgs) != 2 {
		t.Fatalf("인자 수=%d, want 2(cutoff, limit): %v", len(staleArgs), staleArgs)
	}
	cut, ok := staleArgs[0].(time.Time)
	if !ok {
		t.Fatalf("첫 인자가 timestamptz 컷오프가 아님: %T(%v)", staleArgs[0], staleArgs[0])
	}
	if el := time.Since(cut); el < 29*24*time.Hour || el > 31*24*time.Hour {
		t.Errorf("컷오프 경과=%v, want 약 30일(--days 30)", el)
	}
	if lim, ok := staleArgs[1].(int); !ok || lim != 20 {
		t.Errorf("limit 인자=%v, want 20(기본)", staleArgs[1])
	}
}

func TestRecheckConditionsAndDistillCapRealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	st, err := newRealStore(ctx, testengine.Scratch(t))
	if err != nil {
		t.Fatalf("newRealStore: %v", err)
	}
	t.Cleanup(st.Close)
	rs := st.(*realStore)

	seed := `INSERT INTO knowledge (topic, summary, tags, confidence, status, content_hash)
	         VALUES ($1, $2, ':demo:', 0.9, 'active', $3)`
	if _, err := rs.pool.Exec(ctx, seed, "r96-realslow-demo", "synthetic recheck body", "hash-r96-real"); err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}
	idRow, err := st.Query(ctx, `SELECT id, content_hash FROM knowledge WHERE topic = 'r96-realslow-demo'`)
	if err != nil || len(idRow) != 1 {
		t.Fatalf("seed lookup: %v rows=%d", err, len(idRow))
	}
	origHash := fmt.Sprint(idRow[0][1])

	var out strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--recheck", fmt.Sprint(idRow[0][0]), "--conditions", "when demo holds (r96 real)", "--yes"}, nil, &out); code != 0 {
		t.Fatalf("backfill exit=%d out=%s", code, out.String())
	}
	got, err := st.Query(ctx, `SELECT conditions, content_hash FROM knowledge WHERE topic = 'r96-realslow-demo'`)
	if err != nil || len(got) != 1 {
		t.Fatalf("post-update lookup: %v", err)
	}
	if c, _ := got[0][0].(string); c != "when demo holds (r96 real)" {
		t.Fatalf("conditions round-trip = %q", c)
	}
	if h := fmt.Sprint(got[0][1]); h != origHash {
		t.Fatalf("content_hash moved: %q -> %q (conditions must be hash-excluded)", origHash, h)
	}
	runs, err := st.Query(ctx, `SELECT count(*) FROM maintenance_runs WHERE kind = 'recheck'`)
	if err != nil || len(runs) != 1 || fmt.Sprint(runs[0][0]) != "1" {
		t.Fatalf("maintenance_runs kind=recheck: %v %v", runs, err)
	}

	out.Reset()
	if code := ops.RunMaintain(ctx, st, []string{"--recheck", fmt.Sprint(idRow[0][0])}, nil, &out); code != 0 || !strings.Contains(out.String(), "when demo holds (r96 real)") {
		t.Fatalf("report round-trip exit code/값 불일치: %d %s", code, out.String())
	}

	for _, s := range []string{"s2", "s3"} {
		if _, err := rs.pool.Exec(ctx, seed, "r96-realslow-"+s, strings.Repeat("body ", 80), "hash-r96-real-"+s); err != nil {
			t.Fatalf("seed %s: %v", s, err)
		}
	}
	if _, err := rs.pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('distill_max_runes', '600')`); err != nil {
		t.Fatalf("seed meta cap: %v", err)
	}
	dir := t.TempDir()
	stderr := new(strings.Builder)
	prevErr, prevNow := distillStderr, distillNowFn
	distillStderr = stderr
	fixed := time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)
	distillNowFn = func() time.Time { return fixed }
	t.Cleanup(func() { distillStderr, distillNowFn = prevErr, prevNow })
	var stdout strings.Builder
	if code := runDistill(ctx, st, []string{"--out", dir, "--min-score", "0"}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("distill exit=%d out=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stamped, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE distilled_at IS NOT NULL`)
	if err != nil || len(stamped) != 1 {
		t.Fatalf("stamped lookup: %v", err)
	}
	firstStamped := cellInt(t, stamped[0][0])
	if firstStamped == 3 || firstStamped == 0 {
		t.Fatalf("stamped rows = %d, want a partial count (1 or 2 — the cap 600 bound)", firstStamped)
	}
	if !strings.Contains(stderr.String(), "rows deferred") {
		t.Fatalf("deferred notice missing: %q", stderr.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "insights-") {
		t.Fatalf("insights file missing: %v", entries)
	}
	first, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || strings.Count(string(first), "\n## #") == 0 {
		t.Fatalf("insights body empty: %v", err)
	}

	stdout.Reset()
	if code := runDistill(ctx, st, []string{"--out", dir, "--min-score", "0"}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("distill rerun exit=%d out=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	again, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(again) != string(first) {
		t.Fatalf("same-minute rerun overwrote the earlier file (r98: must survive)")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("rerun files = %d, want 2 (the -2 suffix): %v", len(entries), entries)
	}

	stamped, err = st.Query(ctx, `SELECT count(*) FROM knowledge WHERE distilled_at IS NOT NULL`)
	if err != nil || len(stamped) != 1 {
		t.Fatalf("post-rerun stamped lookup: %v", err)
	}
	if next := cellInt(t, stamped[0][0]); next <= firstStamped {
		t.Fatalf("post-rerun stamped = %d (was %d), want an increase (deferred rows emit)", next, firstStamped)
	}
}

func cellInt(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint32:
		return int(n)
	case int:
		return n
	}
	t.Fatalf("count cell type %T (%v)", v, v)
	return 0
}

func TestClassifyConflictBoundary(t *testing.T) {
	if classifyConflict("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다") {
		t.Error("동일 요약 = 모순 아님")
	}
	if !classifyConflict("포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다") {
		t.Error("이질 요약 = 모순 후보")
	}
}

type fakeConflict struct {
	*fakeMaint

	conflictGroups []db.ConflictGroup
	conflictErr    error
	conflictCalls  []int
}

func (f *fakeConflict) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	f.conflictCalls = append(f.conflictCalls, limit)
	return f.conflictGroups, f.conflictErr
}

func runConflictArgs(t *testing.T, f *fakeConflict, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runMaintain(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestConflictSuspectsFlag(t *testing.T) {
	same := "flint 백업은 매일 자동이다"
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{same, same}},
			{Topic: "port-config", IDs: []int{3, 4},
				Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}}
	out, code := runConflictArgs(t, f, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"topic":"port-config"`) {
		t.Errorf("이질 그룹 미포함: %q", out)
	}
	if strings.Contains(out, "backup-policy") {
		t.Errorf("동일 요약 그룹이 모순 후보로 리스팅됨: %q", out)
	}
	if !strings.Contains(out, `"status":"conflict-suspects"`) {
		t.Errorf("status 불일치: %q", out)
	}

	if !strings.Contains(out, "포트 기본값은 5433이다") {
		t.Errorf("요약 미포함: %q", out)
	}
	if len(f.conflictCalls) != 1 || f.conflictCalls[0] != 10 {
		t.Errorf("ConflictGroups 호출 limit = %v, want [10]", f.conflictCalls)
	}

	f2 := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	_, code2 := runConflictArgs(t, f2, "--conflict-suspects", "--limit", "5")
	if code2 != 0 {
		t.Fatalf("--limit: exit=%d, want 0", code2)
	}
	if len(f2.conflictCalls) != 1 || f2.conflictCalls[0] != 5 {
		t.Errorf("--limit 전달 = %v, want [5]", f2.conflictCalls)
	}

	f3 := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{same, same}}}}
	out3, code3 := runConflictArgs(t, f3, "--conflict-suspects")
	if code3 != 0 {
		t.Fatalf("빈 결과: exit=%d, want 0 (출력 %q)", code3, out3)
	}
	if !strings.Contains(out3, `"groups":[]`) {
		t.Errorf("빈 groups 아님: %q", out3)
	}

	m := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	mout, mcode := runConflictArgs(t, m, "--conflict-suspects", "--id", "7", "--reason", "x")
	if mcode != 1 || !strings.Contains(mout, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", mcode, mout)
	}

	u := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}}}
	uout, ucode := runConflictArgs(t, u)
	if ucode != 3 || !strings.Contains(uout, "--conflict-suspects") {
		t.Errorf("usage 갱신 누락: exit=%d %q", ucode, uout)
	}
}

func TestConflictDiceMeasures(t *testing.T) {
	if got := conflictDice("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다"); got != 1 {
		t.Errorf("동일 문자열 Dice = %v, want 1", got)
	}
	got := conflictDice("포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다")
	if got >= conflictDupThreshold {
		t.Errorf("이질 쌍 Dice = %v, want <%v", got, conflictDupThreshold)
	}
	if conflictDupThreshold != 0.6 {
		t.Errorf("conflictDupThreshold = %v, want 0.6(룰링)", conflictDupThreshold)
	}
}

func TestConflictSummaryTruncation(t *testing.T) {
	long := strings.Repeat("가", 50)
	if got := conflictSummaryPrefix(long); len([]rune(got)) != 40 {
		t.Errorf("절단 길이 = %d, want 40", len([]rune(got)))
	}
	short := "짧은 요약"
	if got := conflictSummaryPrefix(short); got != short {
		t.Errorf("짧은 요약 변형 = %q, want 원문", got)
	}
}

func expireFixture(old, recent time.Time) *fakeMaint {
	return &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{int64(3), "backup-policy", "오래된 백업 정책 요약", old},
			{int64(4), "port-config", "최근 포트 정책", recent},
		}, nil
	}}}
}

func TestExpirePendingDryRun(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	f := expireFixture(old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}

	if !strings.Contains(out, `"id":3`) || !strings.Contains(out, `"age_days":7`) {
		t.Errorf("7일 행 미포함: %q", out)
	}
	if strings.Contains(out, `"id":4`) {
		t.Errorf("2일 행이 나열됨: %q", out)
	}
	if !strings.Contains(out, `"status":"expire-pending"`) {
		t.Errorf("status 불일치: %q", out)
	}

	if len(f.invalidateCalls) != 0 {
		t.Errorf("dry-run에서 Invalidate 실행: %v", f.invalidateCalls)
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에서 Commit 실행: %v", f.commits)
	}
}

func TestExpirePendingCommit(t *testing.T) {
	old := time.Now().Add(-7 * 24 * time.Hour)
	recent := time.Now().Add(-2 * 24 * time.Hour)
	f := expireFixture(old, recent)

	out, code := runMaintainArgs(t, f, "--expire-pending", "5", "--commit")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if len(f.invalidateCalls) != 1 {
		t.Fatalf("Invalidate 호출 수=%d, want 1", len(f.invalidateCalls))
	}

	if len(f.invalidateCalls[0]) != 1 || f.invalidateCalls[0][0] != 3 {
		t.Errorf("Invalidate 파라미터=%v, want [[3]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 1 || f.invalidateMsgs[0] != "expire-pending" {
		t.Errorf("Invalidate 사유=%v, want expire-pending", f.invalidateMsgs)
	}

	if !strings.Contains(out, `"status":"invalidated"`) || !strings.Contains(out, `"count":1`) {
		t.Errorf("전환 건수 출력 불일치: %q", out)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "expire-pending") {
		t.Errorf("커밋 메시지=%v, want expire-pending 1회", f.commits)
	}
}

func TestExpirePendingValidation(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--expire-pending")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("days 누락 거부 없음: exit=%d %q", code, out)
	}
	out, code = runMaintainArgs(t, f, "--expire-pending", "0")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("양수 아닌 days 거부 없음: exit=%d %q", code, out)
	}
	out, code = runMaintainArgs(t, f, "--commit")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("--commit 단독 거부 없음: exit=%d %q", code, out)
	}

	out, code = runMaintainArgs(t, f, "--expire-pending", "5", "--id", "7")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", code, out)
	}
}

func TestConflictThresholdOverride(t *testing.T) {
	summA, summB := "flint 백업은 매일 자동이다", "flint 백업은 매주 수동 점검이다"
	dice := conflictDice(summA, summB)
	if dice < conflictDupThreshold || dice >= 1 {
		t.Fatalf("픽스처 유사도 %v는 [기본 임계, 1) 구간 밖 — 검증 전제 위반", dice)
	}
	over := strconv.FormatFloat((dice+1)/2, 'f', -1, 64)

	base := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{summA, summB}}}}
	out, code := runConflictArgs(t, base, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("기본 exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"groups":[]`) {
		t.Errorf("기본 0.6에서 중복 그룹이 리스팅됨: %q", out)
	}

	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "conflict_dup_threshold") {
				return [][]any{{"conflict_dup_threshold", over}}, nil
			}
			return nil, nil
		},
	}}, conflictGroups: []db.ConflictGroup{
		{Topic: "backup-policy", IDs: []int{1, 2}, Summaries: []string{summA, summB}},
	}}
	out2, code2 := runConflictArgs(t, f, "--conflict-suspects")
	if code2 != 0 {
		t.Fatalf("오버라이드 exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if !strings.Contains(out2, `"topic":"backup-policy"`) {
		t.Errorf("오버라이드 %s에서 모순 후보 미리스팅(기본 0.6 잔존 의심): %q", over, out2)
	}
}

func TestConflictThresholdDefaultKept(t *testing.T) {
	f := &fakeConflict{fakeMaint: &fakeMaint{fakeStore: &fakeStore{}},
		conflictGroups: []db.ConflictGroup{
			{Topic: "port-config", IDs: []int{3, 4},
				Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}},
		}}
	out, code := runConflictArgs(t, f, "--conflict-suspects")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"topic":"port-config"`) {
		t.Errorf("기본 0.6 분류 회귀 — 이질 그룹 미포함: %q", out)
	}
}

func TestMaintainMetaAuditClassification(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta ORDER BY key") {
			return [][]any{
				{"schema_version", "8"},
				{"daily:2026-11-09", "{}"},
				{"read_stats", "{}"},
				{"last_brief_at", "x"},
				{"daily:write:2026-11-09", "1"},
				{"mystery_key", "x"},
			}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	code := runMaintain(context.Background(), f, []string{"--meta-audit"}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{`"known":3`, `"daily":2`, `"unknown":1`, `"mystery_key"`} {
		if !strings.Contains(s, want) {
			t.Errorf("분류 누락 %q: %s", want, s)
		}
	}
}

func TestMaintainPruneInvalidatedDryRunCommit(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'invalidated'") && strings.Contains(sql, "SELECT") {
			return [][]any{{int32(3)}, {int32(7)}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated", "30"}, nil, &out); code != 0 {
		t.Fatalf("dry-run exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"would_delete":2`) || !strings.Contains(out.String(), `"status":"dry-run"`) {
		t.Errorf("dry-run 출력: %s", out.String())
	}
	if len(f.pruneCalls) != 0 {
		t.Error("dry-run에서 DELETE 위임 실행")
	}

	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated", "30", "--commit"}, nil, &out); code != 0 {
		t.Fatalf("commit exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"deleted":2`) {
		t.Errorf("commit 출력: %s", out.String())
	}
	if len(f.pruneCalls) != 1 {
		t.Fatalf("DELETE 위임 실행 %d회, want 1", len(f.pruneCalls))
	}

	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated"}, nil, &out); code != 1 {
		t.Errorf("N 부재 exit=%d, want 1(사용법 오류): %s", code, out.String())
	}
}

func staleQueryFixture(t *testing.T, f *fakeMaint, metaRows, knolRows [][]any) {
	t.Helper()
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return metaRows, nil
		}
		return knolRows, nil
	}
}

func ago(d time.Duration) time.Time { return time.Now().Add(-d) }

func TestStaleBucketsAndStallWarnings(t *testing.T) {
	knol := [][]any{
		{1, "미참조 노트", nil, ago(100 * 24 * time.Hour), 0},
		{2, "읽었지만 오래됨", ago(95 * 24 * time.Hour), ago(100 * 24 * time.Hour), 5},
		{3, "활성", ago(time.Hour), ago(24 * time.Hour), 3},
	}
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	items, _ := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d건, want 2 (활성 제외): %s", len(items), out)
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != float64(1) || first["never_read"] != true {
		t.Errorf("never_read 버킷 구분 실패: %v", first)
	}
	second, _ := items[1].(map[string]any)
	if second["id"] != float64(2) || second["never_read"] != false {
		t.Errorf("read-stale 버킷: %v", second)
	}
	if first["topic"] == "" || first["stale_days"] == nil {
		t.Errorf("id/topic/경과 필드 누락: %v", first)
	}

	hotOld := time.Now().Add(-73 * time.Hour).UTC().Format(time.RFC3339)
	writeFresh := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	fc := &fakeMaint{fakeStore: &fakeStore{}}
	fc.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_drill") {
			return nil, nil
		}
		if strings.Contains(sql, "last_hot_at") {
			return [][]any{{"last_hot_at", hotOld}, {"last_write_at", writeFresh}}, nil
		}
		return nil, nil
	}
	outC, codeC := runMaintainArgs(t, fc, "--check")
	if codeC != 0 {
		t.Fatalf("--check exit=%d, want 0; out=%s", codeC, outC)
	}
	var mc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(outC)), &mc); err != nil {
		t.Fatalf("JSON 아님: %q", outC)
	}
	warns, _ := mc["warnings"].([]any)
	if len(warns) != 1 || !strings.Contains(jsonStr(t, warns[0]), "last_hot_at") {
		t.Fatalf("hot stall 경고 1건이어야 함(write는 이내): %s", outC)
	}
}

func jsonStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestStaleDaysOverride(t *testing.T) {
	knol := [][]any{
		{10, "40일 경과", nil, ago(40 * 24 * time.Hour), 0},
		{11, "20일 경과", nil, ago(20 * 24 * time.Hour), 0},
	}

	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"count":0`) {
		t.Fatalf("기본 90일에는 0건이어야 함: %s", out)
	}

	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f2, nil, knol)
	out2, _ := runMaintainArgs(t, f2, "--stale", "--days", "30")
	if !strings.Contains(out2, `"id":10`) || strings.Contains(out2, `"id":11`) {
		t.Fatalf("--days 30 미반영: %s", out2)
	}

	f3 := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f3, [][]any{{"stale_days", "30"}}, knol)
	out3, _ := runMaintainArgs(t, f3, "--stale")
	if !strings.Contains(out3, `"id":10`) || strings.Contains(out3, `"id":11`) {
		t.Fatalf("meta stale_days 미반영: %s", out3)
	}
}

func TestStaleDayBoundary(t *testing.T) {
	knol := [][]any{
		{20, "91일", nil, ago(91 * 24 * time.Hour), 0},
		{21, "89일", nil, ago(89 * 24 * time.Hour), 0},
	}
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, knol)
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(out, `"id":20`) {
		t.Errorf("91일 행 미포함: %s", out)
	}
	if strings.Contains(out, `"id":21`) {
		t.Errorf("89일(경계 이내) 행 포함됨: %s", out)
	}
}

func TestStaleServerDown(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryErr: store.ErrServerDown}}
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 JSON 아님: %q", out)
	}
}

func TestStaleLimitDefault(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, nil)
	runMaintainArgs(t, f, "--stale")
	found := false
	for _, q := range f.queries {
		if strings.Contains(q, "LIMIT") && strings.Contains(q, "knowledge") {
			found = true
		}
	}
	if !found {
		t.Fatalf("LIMIT 있는 stale 조회 미발생: %v", f.queries)
	}
}

func TestMaintainModeFlagValidation(t *testing.T) {
	bad := [][]string{
		{"--check", "--days", "5"},
		{"--check", "--limit", "5"},
		{"--check", "--id", "3"},
		{"--stale", "--id", "3"},
		{"--stale", "--reason", "x"},
		{"--invalidate", "--id", "3", "--reason", "x", "--days", "5"},
		{"--merge", "--from", "1", "--to", "2", "--limit", "4"},
	}
	for _, args := range bad {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		out, code := runMaintainArgs(t, f, args...)
		if code != 1 {
			t.Errorf("%v: exit=%d, want 1(모드 무관 플래그); out=%s", args, code, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%v: 에러 JSON 아님: %q", args, out)
		}
		if len(f.invalidateCalls) != 0 || len(f.mergeCalls) != 0 {
			t.Errorf("%v: 거부됐는데 갱신 실행됨(invalidate=%v merge=%v)",
				args, f.invalidateCalls, f.mergeCalls)
		}
	}

	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, nil)
	if _, code := runMaintainArgs(t, f, "--stale", "--days", "30", "--limit", "5"); code != 0 {
		t.Errorf("--stale --days/--limit: exit=%d, want 0", code)
	}
}

func v08StaleRows() [][]any {
	return [][]any{
		{1, "미참조 노트", nil, ago(100 * 24 * time.Hour), 0},
		{2, "읽었지만 오래됨", ago(95 * 24 * time.Hour), ago(100 * 24 * time.Hour), 5},
	}
}

func shortRetryDelays(t *testing.T) {
	t.Helper()
	prev := maintainRetryDelays
	maintainRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { maintainRetryDelays = prev })
}

func TestStaleListsReasonsSummary(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, v08StaleRows())
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := mustJSON(t, out)
	items, _ := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d건, want 2: %s", len(items), out)
	}
	first, _ := items[0].(map[string]any)
	if first["reason"] != "never_read" {
		t.Errorf("id=1 reason=%v, want never_read", first["reason"])
	}
	second, _ := items[1].(map[string]any)
	if second["reason"] != "stale" {
		t.Errorf("id=2 reason=%v, want stale", second["reason"])
	}
	if m["summary"] != "2 to invalidate" {
		t.Errorf("summary=%v, want %q", m["summary"], "2 to invalidate")
	}
}

func mustJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q (%v)", out, err)
	}
	return m
}

func TestInvalidateStaleDryRunDefault(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, v08StaleRows())
	out, code := runMaintainArgs(t, f, "--invalidate-stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := mustJSON(t, out); m["summary"] != "2 to invalidate" {
		t.Errorf("dry-run summary=%v", m["summary"])
	}
	for _, q := range f.queries {
		if strings.Contains(q, "UPDATE") {
			t.Errorf("dry-run에 UPDATE 발생: %q", q)
		}
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에 Commit 발생: %v", f.commits)
	}
}

func TestInvalidateStaleYesApplies(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.invalSeqErrs = []error{errors.New("40001 serialization failure")}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		return v08StaleRows(), nil
	}
	out, code := runMaintainArgs(t, f, "--invalidate-stale", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.invalidateCalls) != 2 {
		t.Errorf("Invalidate 시도=%d회, want 2(Retry 후 회복)", len(f.invalidateCalls))
	}
	if len(f.invalidateCalls[0]) != 2 || f.invalidateCalls[0][0] != 1 || f.invalidateCalls[0][1] != 2 {
		t.Errorf("Invalidate 파라미터=%v, want [[1 2]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 2 || f.invalidateMsgs[0] != "invalidate-stale" {
		t.Errorf("Invalidate 사유=%v, want invalidate-stale", f.invalidateMsgs)
	}
	if len(f.commits) != 1 {
		t.Fatalf("Commit 호출=%d회, want 1: %v", len(f.commits), f.commits)
	}
	if !strings.Contains(f.commits[0], "invalidate-stale") {
		t.Errorf("커밋 메시지=%q", f.commits[0])
	}
	if m := mustJSON(t, out); m["count"] != float64(2) {
		t.Errorf("count=%v, want 2", m["count"])
	}
}

func TestInvalidateIdsSnapshot(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id FROM knowledge") {
			return [][]any{{4}, {9}}, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}
	out, code := runMaintainArgs(t, f, "--invalidate", "--ids", "4,9", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "last_read_at") {
			t.Errorf("stale 재평가 질의 발생(스냅샷 위반): %q", q)
		}
	}
	if len(f.invalidateCalls) != 1 ||
		!reflect.DeepEqual(f.invalidateCalls[0], []int{4, 9}) {
		t.Errorf("Invalidate 파라미터=%v, want [[4 9]]", f.invalidateCalls)
	}
	if len(f.commits) != 1 {
		t.Errorf("Commit 호출=%d회, want 1", len(f.commits))
	}

	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	f2.queryFn = func(sql string, args []any) ([][]any, error) {
		return [][]any{{4}}, nil
	}
	out2, code2 := runMaintainArgs(t, f2, "--invalidate", "--ids", "4,9", "--yes")
	if code2 != 1 {
		t.Errorf("부재 exit=%d, want 1; out=%s", code2, out2)
	}
	if !strings.Contains(out2, `"error"`) || !strings.Contains(out2, "9") {
		t.Errorf("부재 안내 부재: %q", out2)
	}
	if len(f2.invalidateCalls) != 0 {
		t.Errorf("부재 시 Invalidate 발생: %v", f2.invalidateCalls)
	}
	if len(f2.commits) != 0 {
		t.Errorf("부재 시 Commit 발생: %v", f2.commits)
	}

	_, code3 := runMaintainArgs(t, &fakeMaint{fakeStore: &fakeStore{}},
		"--invalidate", "--ids", "4")
	if code3 != 1 {
		t.Errorf("--ids 단독 exit=%d, want 1", code3)
	}
}

func supersedeFixture(t *testing.T, f *fakeMaint, row []any) {
	t.Helper()
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status") {
			return [][]any{row}, nil
		}
		if strings.Contains(sql, "WITH RECURSIVE") {

			return [][]any{{false, false}}, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}
}

func TestSupersedeSetsFields(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	newAt := time.Now().Add(-24 * time.Hour)
	supersedeFixture(t, f, []any{"active", newAt})
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.supersedeCalls) != 1 {
		t.Fatalf("Supersede 호출=%d회, want 1", len(f.supersedeCalls))
	}
	if got := f.supersedeCalls[0]; got.oldID != 3 || got.newID != 7 || !got.at.Equal(newAt) {
		t.Errorf("Supersede 위임=%+v, want {3 7 %v}", got, newAt)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "supersede") {
		t.Errorf("Commit=%v", f.commits)
	}
	if !strings.Contains(out, `"status":"superseded"`) || !strings.Contains(out, `"superseded_by":7`) {
		t.Errorf("출력 미충족: %q", out)
	}
}

func TestSupersedeRejects(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		row  []any
		args []string
		want int
	}{
		{"NEW 부재", []any{"active", nil}, []string{"--supersede", "3", "--by", "99"}, 1},
		{"자기참조", []any{"active", now}, []string{"--supersede", "3", "--by", "3"}, 3},
		{"OLD 비active", []any{"invalidated", now}, []string{"--supersede", "3", "--by", "7"}, 1},
		{"OLD 부재", []any{nil, now}, []string{"--supersede", "3", "--by", "7"}, 1},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		supersedeFixture(t, f, c.row)
		out, code := runMaintainArgs(t, f, c.args...)
		if code != c.want {
			t.Errorf("%s: exit=%d, want %d; out=%s", c.name, code, c.want, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%s: 에러 JSON 아님: %q", c.name, out)
		}
		if len(f.commits) != 0 {
			t.Errorf("%s: Commit 발생: %v", c.name, f.commits)
		}
		if len(f.supersedeCalls) != 0 {
			t.Errorf("%s: Supersede 발생: %v", c.name, f.supersedeCalls)
		}
	}
}

func TestMaintainV08ModeFlagConflicts(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"stale에 --id", []string{"--stale", "--id", "5"}},
		{"invalidate-stale에 --id", []string{"--invalidate-stale", "--id", "5"}},
		{"invalidate-stale에 --reason", []string{"--invalidate-stale", "--reason", "x"}},
		{"supersede에 --days", []string{"--supersede", "3", "--by", "7", "--days", "5"}},
		{"check에 --yes", []string{"--check", "--yes"}},
		{"stale에 --yes", []string{"--stale", "--yes"}},
		{"merge에 --yes", []string{"--merge", "--from", "1", "--to", "2", "--yes"}},
		{"supersede에 --by 없음", []string{"--supersede", "3"}},
		{"check에 --ids", []string{"--check", "--ids", "1"}},
		{"ids 파싱 실패", []string{"--invalidate", "--ids", "1,x", "--yes"}},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		out, code := runMaintainArgs(t, f, c.args...)
		if code != 1 {
			t.Errorf("%s: exit=%d, want 1; out=%s", c.name, code, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%s: 에러 JSON 아님: %q", c.name, out)
		}
	}
}

func TestDupSuspectsJSONFormat(t *testing.T) {
	got := dupSuspectsJSON([]db.DupGroup{{Topic: "rv-e", IDs: []int{63, 64}}})
	want := `{"status":"dup-suspects","groups":[{"topic":"rv-e","ids":[63,64]}]}`
	if got != want {
		t.Errorf("dupSuspectsJSON = %s, want %s", got, want)
	}
}

func TestDupSuspectsJSONEmpty(t *testing.T) {
	if got := dupSuspectsJSON(nil); got != `{"status":"dup-suspects","groups":[]}` {
		t.Errorf("빈 그룹 = %s", got)
	}
}

func TestMaintainDupSuspectsFlag(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{},
		dupGroups: []db.DupGroup{{Topic: "rv-e", IDs: []int{63, 64}}}}
	out, code := runMaintainArgs(t, f, "--dup-suspects", "--limit", "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `{"status":"dup-suspects","groups":[{"topic":"rv-e","ids":[63,64]}]}`) {
		t.Errorf("출력 %q", out)
	}
	if len(f.dupGroupsCalls) != 1 || f.dupGroupsCalls[0] != 5 {
		t.Errorf("DupGroups 호출 limit = %v, want [5]", f.dupGroupsCalls)
	}

	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	out2, code2 := runMaintainArgs(t, f2, "--dup-suspects")
	if code2 != 0 {
		t.Fatalf("기본 limit: exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if len(f2.dupGroupsCalls) != 1 || f2.dupGroupsCalls[0] != 10 {
		t.Errorf("기본 limit 호출 = %v, want [10]", f2.dupGroupsCalls)
	}

	u := &fakeMaint{fakeStore: &fakeStore{}}
	uout, ucode := runMaintainArgs(t, u)
	if ucode != 3 || !strings.Contains(uout, "--dup-suspects") {
		t.Errorf("usage 갱신 누락: exit=%d %q", ucode, uout)
	}

	e := &fakeMaint{fakeStore: &fakeStore{}, dupGroupsErr: errors.New("boom")}
	eout, ecode := runMaintainArgs(t, e, "--dup-suspects")
	if ecode == 0 || !strings.Contains(eout, "boom") {
		t.Errorf("에러 미전파: exit=%d %q", ecode, eout)
	}

	m := &fakeMaint{fakeStore: &fakeStore{}}
	mout, mcode := runMaintainArgs(t, m, "--dup-suspects", "--id", "7", "--reason", "x")
	if mcode != 1 || !strings.Contains(mout, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", mcode, mout)
	}
}
