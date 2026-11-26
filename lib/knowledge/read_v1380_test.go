package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/wire"
)

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
