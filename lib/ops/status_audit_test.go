// status_audit_test.go — 감사 F1(v1.0.1 round1) 회귀 2건.
// ① schema_version 허용 범위는 db.LatestSchemaVersion 파생 — 하드코딩
//
//	목록("1"~"8")은 v9 DB를 exit 3으로 오판했다. 가드 승격: 파생 비교
//	동작 + 소스 스캔(문자열 리터럴 버전 비교 재발 시 FAIL).
//
// ② --as-of 카운트 질의 접합 전 asOfHashRe 문자셋 재검증(v0.28.0 H③
//
//	승계 — read --as-of와 동일 규칙): 오염 해시(작은따옴표 포함)는
//	거부되고 질의에 접합되지 않는다.
package ops

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

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

// statusFake — meta schema_version·AS OF 카운트(7)만 응답하는 fakeStore.
// 수신 SQL은 f.queries에 기록돼 접합 단얨에 쓰인다.
func statusFake(schemaVersion string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'schema_version'") {
			return [][]any{{schemaVersion}}, nil
		}
		if strings.Contains(sql, "AS OF '") {
			return [][]any{{int64(7)}}, nil
		}
		return nil, nil
	}}
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
// 돌려주면 접합 전 거부(exit 1·문자셋 위반 문구) — 어떤 질의에도
// 오염값이 접합되지 않는다(인라인 주입 차단).
func TestStatusAsOfPollutedHashRejected(t *testing.T) {
	bad := "ab12'; DROP TABLE knowledge;--"
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return bad, nil })
	st := statusFake(strconv.Itoa(db.LatestSchemaVersion))
	var out strings.Builder
	code := RunStatus(context.Background(), st, []string{"--as-of", "ab12"}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("오염 해시 exit=%d, want 1 — %s", code, out.String())
	}
	if !strings.Contains(out.String(), "문자셋 위반") {
		t.Errorf("문자셋 위반 문구 미포함 — %s", out.String())
	}
	for _, q := range st.queries {
		if strings.Contains(q, bad) {
			t.Fatalf("오염 해시가 질의에 접합됨: %q", q)
		}
	}
}

// TestStatusAsOfValidHashCountedFromSnapshot — 정상 해시는 AS OF 카운트
// 질의가 접합되고 pending(스냅샷 수)·as_of.hash를 보고한다(재검증이
// 정상 경로를 막지 않음).
func TestStatusAsOfValidHashCountedFromSnapshot(t *testing.T) {
	hash := "abc123def"
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return hash, nil })
	st := statusFake(strconv.Itoa(db.LatestSchemaVersion))
	var out strings.Builder
	if code := RunStatus(context.Background(), st, []string{"--as-of", "abc"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("정상 해시 exit=%d, want 0 — %s", code, out.String())
	}
	joined := false
	for _, q := range st.queries {
		if strings.Contains(q, "AS OF '"+hash+"'") {
			joined = true
		}
	}
	if !joined {
		t.Error("AS OF 접합 카운트 질의 미발생")
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
