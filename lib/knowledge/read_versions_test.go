package knowledge

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/wire"
)

type readBudgetRecorder struct {
	*fakeStore
	bumped  [][]int
	events  []db.ReadEvent
	usages  [][2]bool
	metaOps []struct {
		sql  string
		args []any
	}
}

func (r *readBudgetRecorder) BumpReads(ctx context.Context, ids []int) error {
	r.bumped = append(r.bumped, ids)
	return nil
}

func (r *readBudgetRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	r.usages = append(r.usages, [2]bool{zero, near})
	return nil
}

func (r *readBudgetRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func newReadBudgetRecorder(hit bool) *readBudgetRecorder {
	r := &readBudgetRecorder{fakeStore: &fakeStore{}}
	r.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INSERT INTO meta") ||
			strings.Contains(sql, "SELECT value FROM meta") {
			r.metaOps = append(r.metaOps, struct {
				sql  string
				args []any
			}{sql, args})
			return nil, nil
		}
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {

			return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
		}
		if strings.Contains(sql, "FROM recall_knowledge") || strings.Contains(sql, "FROM active_knowledge") {
			if hit {
				return hitRowsFixture, nil
			}
			return nil, nil
		}
		return nil, nil
	}
	return r
}

func TestReadWritesReduced(t *testing.T) {

	hit := newReadBudgetRecorder(true)
	var out strings.Builder
	if code := RunRead(context.Background(), hit, []string{"백업 정책"}, nil, &out); code != 0 {
		t.Fatalf("hit read exit=%d: %s", code, out.String())
	}

	zn := newReadBudgetRecorder(false)
	var out2 strings.Builder
	if code := RunRead(context.Background(), zn, []string{"백업 복구 절차"}, nil, &out2); code != 0 {
		t.Fatalf("zero-near read exit=%d, want 0(제로히트 exit 0 — v1.5.3): %s", code, out2.String())
	}

	for name, r := range map[string]*readBudgetRecorder{"hit": hit, "zero-near": zn} {
		if len(r.events) != 1 {
			t.Errorf("%s: read_events 기록 %d건, want 1(계측 원천)", name, len(r.events))
		}
		if len(r.usages) != 0 {
			t.Errorf("%s: RecordReadUsage %d회 호출, want 0(read kind daily 쓰기 폐지): %v", name, len(r.usages), r.usages)
		}
		for _, op := range r.metaOps {
			if strings.Contains(op.sql, "read_stats") {
				t.Errorf("%s: read_stats 질의 잔존: %s", name, op.sql)
			}
			for _, a := range op.args {
				if s, ok := a.(string); ok && (s == "read_stats" || s == "last_read_at" || strings.HasPrefix(s, "daily:")) {
					t.Errorf("%s: 폐지 키 meta 쓰기(%s): %s", name, s, op.sql)
				}
			}
		}
		for _, q := range r.fakeStore.queries {
			if strings.Contains(q, "read_stats") {
				t.Errorf("%s: read_stats SQL 잔존: %s", name, q)
			}
		}
	}
	if ev := hit.events[0]; ev.Hits != 2 || ev.Zero || ev.Near {
		t.Errorf("hit read 이벤트 = %+v, want {Hits:2 Zero:false Near:false}", ev)
	}
	if ev := zn.events[0]; ev.Hits != 0 || !ev.Zero || !ev.Near {
		t.Errorf("zero-near read 이벤트 = %+v, want {Hits:0 Zero:true Near:true}", ev)
	}
}

type v1200Recorder struct {
	*fakeStore
	events []db.ReadEvent
	kreads []kreadsCall
}

type kreadsCall struct {
	ids     []int
	session string
	kind    string
}

func (r *v1200Recorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func (r *v1200Recorder) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	r.kreads = append(r.kreads, kreadsCall{ids, session, kind})
	return nil
}

func activeCountQueryRows(n int32) [][]any {
	return [][]any{{n}}
}

func TestReadEventColumns(t *testing.T) {
	ctx := context.Background()
	t.Run("히트 — bytes=출력 len·source=cli·zero_reason 공란", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if readQueryMatch(sql) {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.events) != 1 {
			t.Fatalf("read_events 기록 %d건, want 1", len(st.events))
		}
		ev := st.events[0]
		if ev.Bytes != len(out.String()) {
			t.Errorf("bytes = %d, want %d(len 최종 출력)", ev.Bytes, len(out.String()))
		}
		if ev.Source != "cli" {
			t.Errorf("source = %q, want cli(세션 라벨 부재)", ev.Source)
		}
		if ev.ZeroReason != "" {
			t.Errorf("zero_reason = %q, want \"\"(zero=0)", ev.ZeroReason)
		}
	})
	t.Run("세션 라벨 — source=hook", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if readQueryMatch(sql) {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--session", "s1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if ev := st.events[0]; ev.Source != "hook" {
			t.Errorf("source = %q, want hook(세션 라벨 채택)", ev.Source)
		}
	})
	t.Run("--source mcp — 명시 라벨 우선", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if readQueryMatch(sql) {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source", "mcp", "--session", "s1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if ev := st.events[0]; ev.Source != "mcp" {
			t.Errorf("source = %q, want mcp(명시 우선)", ev.Source)
		}
	})
	t.Run("--source 무효값 — exit 3", func(t *testing.T) {
		st := &fakeStore{}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source", "bogus", "q"}, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("exit = %d, want 3(무효 source)", code)
		}
	})
	t.Run("--source 값 없음 — exit 3", func(t *testing.T) {
		st := &fakeStore{}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"--source"}, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("exit = %d, want 3(빈 source)", code)
		}
	})
	t.Run("제로히트 — active 0 → unowned", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(0), nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "unowned" {
			t.Errorf("zero_reason = %q, want unowned(active 0 < floor 1)", ev.ZeroReason)
		}
	})
	t.Run("제로히트 — active≥1 → owned_miss", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(2), nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "owned_miss" {
			t.Errorf("zero_reason = %q, want owned_miss(active 2 ≥ floor 1)", ev.ZeroReason)
		}
	})
	t.Run("제로히트 — zero_owned_floor 오버라이드(3)·active 2 → unowned", func(t *testing.T) {
		st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "count(*)"):
				return activeCountQueryRows(2), nil
			case strings.Contains(sql, "SELECT value FROM meta"):
				return [][]any{{"3"}}, nil
			}
			return nil, nil
		}}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if ev := st.events[0]; ev.ZeroReason != "unowned" {
			t.Errorf("zero_reason = %q, want unowned(active 2 < floor 3)", ev.ZeroReason)
		}
	})
}

func TestZeroReasonDecompose(t *testing.T) {
	cases := []struct {
		name          string
		zero          bool
		active, floor int
		want          string
	}{
		{"zero=0 — 분해 없음", false, 0, 1, ""},
		{"zero·active 0 — 미보유", true, 0, 1, "unowned"},
		{"zero·active 1(기본 임계) — 보유탐색실패", true, 1, 1, "owned_miss"},
		{"zero·floor 3·active 2 — 미보유", true, 2, 3, "unowned"},
		{"zero·floor 3·active 3 — 보유탐색실패", true, 3, 3, "owned_miss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ZeroReason(tc.zero, tc.active, tc.floor); got != tc.want {
				t.Errorf("ZeroReason(%v, %d, %d) = %q, want %q", tc.zero, tc.active, tc.floor, got, tc.want)
			}
		})
	}
}

func TestReadRecordsKnowledgeReads(t *testing.T) {
	ctx := context.Background()
	st := &v1200Recorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if readQueryMatch(sql) {
			return hitRowsFixture, nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunRead(ctx, st, []string{"--session", "kr-1", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if len(st.kreads) != 1 {
		t.Fatalf("knowledge_reads 기록 %d건, want 1", len(st.kreads))
	}
	call := st.kreads[0]
	if len(call.ids) != 2 || call.ids[0] != 1 || call.ids[1] != 2 {
		t.Errorf("kreads ids = %v, want [1 2](반환 id 전수)", call.ids)
	}
	if call.session != "kr-1" || call.kind != "read" {
		t.Errorf("kreads session·kind = %q·%q, want kr-1·read", call.session, call.kind)
	}
}

func TestRunReadMinConfidenceNaNInfRefusedV1201(t *testing.T) {
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf"} {
		st := &fakeStore{}
		var out strings.Builder
		code := RunRead(context.Background(), st,
			[]string{"query", "--min-confidence", v}, strings.NewReader(""), &out)
		if code != 3 {
			t.Errorf("--min-confidence %s exit=%d want 3 (out=%s)", v, code, out.String())
		}
	}
}

type v1380CaptureStore struct {
	*fakeStore
	sql        []string
	args       [][]any
	asofTables []string
}

func (c *v1380CaptureStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	c.sql = append(c.sql, sql)
	c.args = append(c.args, args)
	if c.queryFn != nil {
		return c.queryFn(sql, args)
	}
	return nil, nil
}

func (c *v1380CaptureStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	c.asofTables = append(c.asofTables, q.Table)
	return nil, nil
}

func runCaptureRead(t *testing.T, st *v1380CaptureStore, args []string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := RunRead(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestReadDefaultExcludesEvent(t *testing.T) {
	t.Run("BuildRead — FROM recall_knowledge·claim 열·read_count 정렬 부재", func(t *testing.T) {
		sql, _ := query.BuildRead("백업", query.Filters{})
		if !strings.Contains(sql, "FROM recall_knowledge") {
			t.Errorf("BuildRead SQL이 recall_knowledge 뷰를 사용해야 함: %s", sql)
		}
		if strings.Contains(sql, "FROM active_knowledge") {
			t.Errorf("기본 경로에서 active_knowledge 금지: %s", sql)
		}
		if !strings.Contains(sql, "claim") {
			t.Errorf("SELECT에 claim 열 필요(--fields claim projection): %s", sql)
		}
		if strings.Contains(sql, "read_count DESC") {
			t.Errorf("recall 뷰에 read_count 없음 — 정렬에서 제외해야 함: %s", sql)
		}
	})
	t.Run("RunRead 무인자 — 질의가 recall_knowledge로 향함", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		_, code := runCaptureRead(t, st, []string{"백업 정책"})
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		found := false
		for _, sql := range st.sql {
			if strings.Contains(sql, "FROM recall_knowledge") {
				found = true
			}
			if strings.Contains(sql, "claim = ") || strings.Contains(sql, "claim = $") {
				t.Errorf("무인자 read에 claim 필터 금지(뷰가 event 제외): %s", sql)
			}
		}
		if !found {
			t.Fatalf("recall_knowledge 질의 부재: %v", st.sql)
		}
	})
	t.Run("BuildReadFallback — 기본은 recall_knowledge", func(t *testing.T) {
		sql, _, ok := query.BuildReadFallback("두 단어 이상", query.Filters{})
		if !ok || !strings.Contains(sql, "FROM recall_knowledge") {
			t.Errorf("Fallback 기본 FROM = recall_knowledge 기대: ok=%v %s", ok, sql)
		}
	})
}

func TestReadClaimEventFilter(t *testing.T) {
	t.Run("BuildRead --claim event — FROM knowledge·claim 필터", func(t *testing.T) {
		sql, args := query.BuildRead("백업", query.Filters{Claim: "event"})
		if !strings.Contains(sql, "FROM knowledge") {
			t.Errorf("claim 지정 경로는 event 포함 조회라 knowledge 테이블 기대: %s", sql)
		}
		if !strings.Contains(sql, "invalid_at IS NULL") {
			t.Errorf("claim 지정 경로에 무효 사실 차단 부재: %s", sql)
		}
		if strings.Contains(sql, "FROM recall_knowledge") {
			t.Errorf("recall 뷰는 event를 이미 제외 — 사용 금지: %s", sql)
		}
		if !strings.Contains(sql, "claim = $") {
			t.Errorf("claim 파라미터 필터 부재: %s", sql)
		}
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == "event" {
				found = true
			}
		}
		if !found {
			t.Errorf("args에 event 부재: %v", args)
		}
	})
	t.Run("RunRead --claim event — 같은 질의 발화", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		_, code := runCaptureRead(t, st, []string{"--claim", "event", "데모 이벤트"})
		if code != 0 {
			t.Fatalf("exit = %d: %s", code, "")
		}
		found := false
		for _, sql := range st.sql {
			if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "claim = $") {
				found = true
			}
		}
		if !found {
			t.Fatalf("claim=event 질의 부재: %v", st.sql)
		}
	})
	t.Run("RunRead --claim 무효값 — exit 3", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		var out bytes.Buffer
		code := RunRead(context.Background(), st, []string{"--claim", "bogus", "q"},
			strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("exit = %d, want 3(무효 claim)", code)
		}
	})
}

func TestReadClaimFilterDecision(t *testing.T) {
	t.Run("BuildRead --claim decision — args 전달", func(t *testing.T) {
		sql, args := query.BuildRead("", query.Filters{Claim: "decision"})
		if !strings.Contains(sql, "FROM knowledge") || !strings.Contains(sql, "claim = $") {
			t.Fatalf("claim 필터 질의 아님: %s", sql)
		}
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == "decision" {
				found = true
			}
		}
		if !found {
			t.Errorf("args에 decision 부재: %v", args)
		}
	})
	t.Run("RunRead --claim decision — 같은 질의 발화", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		_, code := runCaptureRead(t, st, []string{"--claim", "decision", "정책"})
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		found := false
		for _, sql := range st.sql {
			if strings.Contains(sql, "claim = $") {
				found = true
			}
		}
		if !found {
			t.Fatalf("claim 필터 질의 부재: %v", st.sql)
		}
	})
	t.Run("BuildReadStatus + claim — status 경로에도 claim 필터", func(t *testing.T) {
		sql, args := query.BuildReadStatus("", query.Filters{Status: "pending", Claim: "lesson"})
		if !strings.Contains(sql, "FROM knowledge") {
			t.Fatalf("status 경로 FROM knowledge 유지 기대: %s", sql)
		}
		if !strings.Contains(sql, "claim = $") {
			t.Errorf("status 경로 claim 필터 부재: %s", sql)
		}
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == "lesson" {
				found = true
			}
		}
		if !found {
			t.Errorf("args에 lesson 부재: %v", args)
		}
	})
}

func TestReadAsOfTargetsRecallView(t *testing.T) {
	injectAsOfSeams(t, "abc123")
	t.Run("기본 + as-of — aq.Table = recall_knowledge", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		_, code := runCaptureRead(t, st, []string{"--as-of", "2026-01-01", "백업"})
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if len(st.asofTables) == 0 || st.asofTables[0] != "recall_knowledge" {
			t.Errorf("as-of 테이블 = %v, want [recall_knowledge]", st.asofTables)
		}
	})
	t.Run("--claim event + as-of — aq.Table = knowledge", func(t *testing.T) {
		st := &v1380CaptureStore{fakeStore: &fakeStore{}}
		_, code := runCaptureRead(t, st, []string{"--claim", "event", "--as-of", "2026-01-01", "백업"})
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if len(st.asofTables) == 0 || st.asofTables[0] != "knowledge" {
			t.Errorf("as-of 테이블 = %v, want [knowledge]", st.asofTables)
		}
	})
}

func TestReadFieldsClaimAllowed(t *testing.T) {
	st := &v1380CaptureStore{fakeStore: &fakeStore{}}
	_, code := runCaptureRead(t, st, []string{"--json", "--fields", "id,claim", "백업"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0(fields에 claim 허용)", code)
	}
}

func recallWiringQueryFn(fallback [][]any, recall [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " FROM recall_events GROUP BY"):
			return recall, nil
		case strings.Contains(sql, " OR "):
			return fallback, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			return [][]any{{"0"}}, nil
		}
		return nil, nil
	}
}

func TestRunReadRecallSignalWiringOrdersRows(t *testing.T) {
	defer query.SetRecallCounts(nil)
	fallbackRows := func() [][]any {
		return [][]any{
			{"1", "backup policy demo", "synthetic note", "", "", "", "decision", "0.9", "", "active", int32(2)},
			{"2", "backup policy demo", "synthetic note", "", "", "", "decision", "0.9", "", "active", int32(2)},
		}
	}

	st := &fakeStore{queryFn: recallWiringQueryFn(fallbackRows(), [][]any{{int64(2), int64(3), "2026-11-27 00:00:00"}})}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"backup", "policy"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 라인 수 = %d, want 2: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "2|") {
		t.Fatalf("recall 3건 행(id=2)이 상위여야 함: 상위=%q", lines[0])
	}

	stNo := &fakeStore{queryFn: recallWiringQueryFn(fallbackRows(), nil)}
	var outNo strings.Builder
	if code := RunRead(context.Background(), stNo,
		[]string{"backup", "policy"}, strings.NewReader(""), &outNo); code != 0 {
		t.Fatalf("exit = %d: %s", code, outNo.String())
	}
	linesNo := strings.Split(strings.TrimRight(outNo.String(), "\n"), "\n")
	if !strings.HasPrefix(linesNo[0], "1|") {
		t.Fatalf("recall 무신호 시 기존 순위 유지: 상위=%q", linesNo[0])
	}
}

func TestReadVersionsCountInvariant(t *testing.T) {
	const want = 17
	const self = "TestReadVersionsCountInvariant"
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("테스트 파일 조사 실패: %v", err)
	}
	fset := token.NewFileSet()
	got := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s 파싱 실패: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if fn := fd.Name.Name; fn != self && strings.Contains(fn, "TestRead") {
				got++
			}
		}
	}
	if got != want {
		t.Fatalf("TestRead 계열 카운트 = %d, want %d(통합 전후 불변)", got, want)
	}
}
