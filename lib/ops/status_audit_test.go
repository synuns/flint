// status_audit_test.go — 회귀 2건.
// ① schema_version 허용 범위는 db.LatestSchemaVersion 파생 — 하드코딩
//
//	목록("1"~"8")은 v9 DB를 exit 3으로 오판했다. 가드 승격: 파생 비교
//	동작 + 소스 스캔(문자열 리터럴 버전 비교 재발 시 FAIL).
//
// ② --as-of 카운트 질의 접합 전 asOfHashRe 문자셋 재검증(read --as-of와
//
//	동일 규칙): 오염 해시(작은따옴표 포함)는
//	거부되고 질의에 접합되지 않는다. v1.8.0 T8 — 카운트는
//	QueryAsOf(AsOfQuery{Hash,Table,Columns,Where}) 이관, 접합 단언은
// 전달 조건 단언으로 환치(문자셋 재검증 잔류 계약 유지).
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

// statusSeams — RunStatus가 참조하는 ops seam을 테스트용 기본 구현으로
// 교체했다가 원복한다(cli root.go init과 동일 구성 — 파서만 per-test 지정).
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

// statusStore — meta(schema_version)는 *fakeStore Query 승격, 히스토리
// 경로(LogEntries{Hash}·QueryAsOf)는 명시 오버라이드 훅. 수신 조건은
// gotLog·gotAsOf에 기록돼 전달 단언에 쓰인다.
type statusStore struct {
	*fakeStore
	wire.HistoryStubs // 오버라이드 대상 외(DiffRows·DiffCommits·DiffStat) 기본구현
	gotLog  []wire.LogQuery
	gotAsOf []wire.AsOfQuery
	logRows []wire.LogEntry
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

// statusFake — meta schema_version·AS OF 카운트(7)만 응답하는 fake.
func statusFake(schemaVersion string) *statusStore {
	return &statusStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'schema_version'") {
			return [][]any{{schemaVersion}}, nil
		}
		return nil, nil
	}}, asOfRows: [][]any{{int64(7)}}}
}

// TestStatusAcceptsLatestSchemaVersion — 최신 스키마 버전 DB는 exit 0.
// 하드코딩 목록("1"~"8")이면 v9에서 exit 3(감사 F1 원증).
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

// TestStatusSchemaVersionBoundsStillRejected — 파생 범위 밖(0·초과·비정수·
// 빈 값)은 여전히 exit 3, 안내 문구도 파생 범위(1~최신)를 말한다.
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

// TestStatusSchemaAllowlistNotHardcoded — 가드: status.go에 문자열 리터럴
// 버전 비교(`version != "8"` 따위)가 재발하면 FAIL. 허용 범위은
// db.LatestSchemaVersion 파생이어야 한다(감사 F1 재발 방지).
func TestStatusSchemaAllowlistNotHardcoded(t *testing.T) {
	src, err := os.ReadFile("status.go")
	if err != nil {
		t.Fatalf("status.go 읽기 실패: %v", err)
	}
	if hit := regexp.MustCompile(`version\s*!=\s*"[0-9]"`).Find(src); hit != nil {
		t.Fatalf("하드코딩 버전 목록 재발: %q — 허용 범위는 db.LatestSchemaVersion 파생", hit)
	}
}

// TestStatusAsOfPollutedHashRejected — 파서가 오염 해시(작은따옴표 포함)를
// 돌려주면 카운트 질의 전 거부(exit 1·문자셋 위반 문구) — QueryAsOf에는
// 오염값이 어떤 형태로도 전달되지 않는다(인라인 주입 차단 계약 승계).
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

// TestStatusAsOfValidHashCountedFromSnapshot — 정상 해시는 QueryAsOf 카운트
// 조회가 knowledge 테이블·count(*)·pending 조건으로 발생하고 pending(스냅샷
// 수)·as_of.hash를 보고한다(재검증이 정상 경로를 막지 않음).
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

// TestStatusAsOfCommitDateFromLogEntries — as_of 날짜 첨부는
// LogEntries{Hash}에서 파생(RFC3339 — 기존 FmtAsOfDate(time.Time) 동치).
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
