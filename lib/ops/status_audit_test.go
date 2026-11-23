package ops

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

func statusSeams(t *testing.T, parse func(context.Context, wire.Store, string) (string, error)) {
	t.Helper()
	prevParse, prevWD := ParseAsOfSpecFn, WindowedDailyFn
	prevRZP, prevHot := ReadZeroPctFn, HotStatsFn
	prevLoadHot, prevLoadRead := LoadHotStatsFromMetaFn, LoadReadStatsFromMetaFn
	ParseAsOfSpecFn = parse
	WindowedDailyFn = func(context.Context, wire.Store, int) ([]DailyRow, error) { return nil, nil }
	ReadZeroPctFn = func(calls, zero int) int { return 0 }
	HotStatsFn = func() (int, int, int) { return 0, 0, 0 }
	LoadHotStatsFromMetaFn = func(context.Context, wire.Store) {}
	LoadReadStatsFromMetaFn = func(context.Context, wire.Store) (int, int) { return 0, 0 }
	t.Cleanup(func() {
		ParseAsOfSpecFn, WindowedDailyFn = prevParse, prevWD
		ReadZeroPctFn, HotStatsFn = prevRZP, prevHot
		LoadHotStatsFromMetaFn, LoadReadStatsFromMetaFn = prevLoadHot, prevLoadRead
	})
}

type statusStore struct {
	*fakeStore
	wire.HistoryStubs
	gotLog   []wire.LogQuery
	gotAsOf  []wire.AsOfQuery
	logRows  []wire.LogEntry
	asOfRows [][]any
}

func (s *statusStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	s.gotLog = append(s.gotLog, q)
	return s.logRows, nil
}

func (s *statusStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	s.gotAsOf = append(s.gotAsOf, q)
	return s.asOfRows, nil
}

func statusFake(schemaVersion string) *statusStore {
	return &statusStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'schema_version'") {
			return [][]any{{schemaVersion}}, nil
		}
		return nil, nil
	}}, asOfRows: [][]any{{int64(7)}}}
}

func TestStatusAcceptsLatestSchemaVersion(t *testing.T) {
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return "", nil })
	want := strconv.Itoa(db.LatestSchemaVersion)
	var out strings.Builder
	if code := RunStatus(context.Background(), statusFake(want), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("schema_version=%s(최신) exit=%d, want 0 — %s", want, code, out.String())
	}
	var got StatusOutput
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("출력 파싱 실패: %v — %s", err, out.String())
	}
	if got.SchemaVersion != want {
		t.Errorf("schema_version=%q, want %q", got.SchemaVersion, want)
	}
}

func TestStatusSchemaVersionBoundsStillRejected(t *testing.T) {
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return "", nil })
	wantMsg := "1~" + strconv.Itoa(db.LatestSchemaVersion)
	for _, v := range []string{"0", strconv.Itoa(db.LatestSchemaVersion + 1), "abc", ""} {
		var out strings.Builder
		if code := RunStatus(context.Background(), statusFake(v), nil, strings.NewReader(""), &out); code != 3 {
			t.Errorf("schema_version=%q exit=%d, want 3 — %s", v, code, out.String())
		}
		if !strings.Contains(out.String(), wantMsg) {
			t.Errorf("schema_version=%q 안내 문구에 파생 범위 %q 없음 — %s", v, wantMsg, out.String())
		}
	}
}

func TestStatusSchemaAllowlistNotHardcoded(t *testing.T) {
	src, err := os.ReadFile("status.go")
	if err != nil {
		t.Fatalf("status.go 읽기 실패: %v", err)
	}
	if hit := regexp.MustCompile(`version\s*!=\s*"[0-9]"`).Find(src); hit != nil {
		t.Fatalf("하드코딩 버전 목록 재발: %q — 허용 범위는 db.LatestSchemaVersion 파생", hit)
	}
}

func TestStatusAsOfPollutedHashRejected(t *testing.T) {
	bad := "ab12'; DROP TABLE knowledge;--"
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return bad, nil })
	st := statusFake(strconv.Itoa(db.LatestSchemaVersion))
	var out strings.Builder
	code := RunStatus(context.Background(), st, []string{"--as-of", "ab12"}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("오염 해시 exit=%d, want 1 — %s", code, out.String())
	}
	if !strings.Contains(out.String(), "invalid characters") {
		t.Errorf("문자셋 위반 문구 미포함 — %s", out.String())
	}
	if len(st.gotAsOf) != 0 {
		t.Fatalf("오염 해시로 QueryAsOf 호출됨: %+v", st.gotAsOf)
	}
	for _, q := range st.gotAsOf {
		if q.Hash == bad {
			t.Fatalf("오염 해시가 QueryAsOf에 전달됨: %q", q.Hash)
		}
	}
}

func TestStatusAsOfValidHashCountedFromSnapshot(t *testing.T) {
	hash := "abc123def"
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return hash, nil })
	st := statusFake(strconv.Itoa(db.LatestSchemaVersion))
	var out strings.Builder
	if code := RunStatus(context.Background(), st, []string{"--as-of", "abc"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("정상 해시 exit=%d, want 0 — %s", code, out.String())
	}
	if len(st.gotAsOf) != 1 {
		t.Fatalf("QueryAsOf %d회, want 1", len(st.gotAsOf))
	}
	q := st.gotAsOf[0]
	if q.Hash != hash {
		t.Errorf("AsOfQuery.Hash=%q, want %q", q.Hash, hash)
	}
	if q.Table != "knowledge" {
		t.Errorf("AsOfQuery.Table=%q, want knowledge", q.Table)
	}
	if len(q.Columns) != 1 || q.Columns[0] != "count(*)" {
		t.Errorf("AsOfQuery.Columns=%v, want [count(*)]", q.Columns)
	}
	if !strings.Contains(q.Where, "status = 'pending'") || !strings.Contains(q.Where, "invalid_at IS NULL") {
		t.Errorf("AsOfQuery.Where=%q, want pending·invalid_at 조건", q.Where)
	}
	var got struct {
		Pending int `json:"pending"`
		AsOf    struct {
			Hash string `json:"hash"`
		} `json:"as_of"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("출력 파싱 실패: %v — %s", err, out.String())
	}
	if got.Pending != 7 || got.AsOf.Hash != hash {
		t.Errorf("pending=%d as_of.hash=%q, want 7/%q", got.Pending, got.AsOf.Hash, hash)
	}
}

func TestStatusAsOfCommitDateFromLogEntries(t *testing.T) {
	hash := "abc123def"
	at := time.Date(2026, 11, 17, 5, 4, 3, 0, time.UTC)
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return hash, nil })
	st := statusFake(strconv.Itoa(db.LatestSchemaVersion))
	st.logRows = []wire.LogEntry{{Hash: hash, Committer: "flint", Message: "demo", Date: at}}
	var out strings.Builder
	if code := RunStatus(context.Background(), st, []string{"--as-of", "abc"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0 — %s", code, out.String())
	}
	if len(st.gotLog) != 1 || st.gotLog[0].Hash != hash {
		t.Fatalf("LogEntries{Hash} 전달 mismatch: %+v", st.gotLog)
	}
	var got struct {
		AsOf struct {
			CommitDate string `json:"commit_date"`
		} `json:"as_of"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("출력 파싱 실패: %v — %s", err, out.String())
	}
	if got.AsOf.CommitDate != at.Format(time.RFC3339) {
		t.Errorf("commit_date=%q, want %q", got.AsOf.CommitDate, at.Format(time.RFC3339))
	}
}
