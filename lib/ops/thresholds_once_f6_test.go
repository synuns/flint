// thresholds_once_f6_test.go — F6(성능) 회귀: thresholds IN 쿼리
// (SELECT key, value FROM meta WHERE key IN …)는 커맨드 실행당 1회.
// report는 --days 상한·stale 컷오프·효과 판정 창이, doctor는 검사 6종이
// 각자 재조회하던 것을 커맨드 스코프 1회 로드로 공유한다(secrets는 F4
// 소유 영역 — 자체 로드 유지로 전체 doctor 실행은 2회).
package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

// thresholdsLoads — fake가 실행한 thresholds IN 쿼리 수. hot_stats 조회
// (IN ('hot_stats'))도 같은 문법이라 thresholds 레지스트리 키('stale_days')
// 동봉 여부로 구분한다.
func thresholdsLoads(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	return n
}

// TestReportThresholdsLoadedOnce — Review Focus: report --days 실행에서
// thresholds IN 쿼리 1회(수정 전 3회 — 상한·stale·효과 각자 로드).
func TestReportThresholdsLoadedOnce(t *testing.T) {
	f := parityFake(10, 3, 1, 2) // read 10·zero 3·near 1·폴백 2 픽스처
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "7"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	if n := thresholdsLoads(f); n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(커맨드 스코프 공유)", n)
	}
}

// f6DoctorFn — 검사 6종(last-seen·backup-age·zero-near·pending-age·
// event-volume·meta-hygiene)이 모두 thresholds를 소비하는 응답:
// last_backup·pending 최근 1건·계측 테이블 0행.
func f6DoctorFn() func(string, []any) ([][]any, error) {
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour).Format(time.RFC3339)
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"9"}}, nil
		case strings.Contains(sql, "pg_indexes"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "last_write_at"):
			return [][]any{
				{"last_write_at", fresh},
				{"last_hot_at", fresh},
				{"last_read_at", fresh},
			}, nil
		case strings.Contains(sql, "last_backup"):
			return [][]any{{"last_backup", fresh}}, nil
		case strings.Contains(sql, "count(*) FROM read_events"):
			return [][]any{{int64(0)}}, nil // event-volume 3테이블 합계
		case strings.Contains(sql, "valid_at"):
			return [][]any{{now.Add(-24 * time.Hour)}}, nil // pending 존재
		}
		return nil, nil // thresholds·daily·read_events·meta 키 — 미설정
	}
}

// TestDoctorThresholdsSharedAcrossChecks — Review Focus: thresholds 소비
// 검사 6종을 실행 스코프 메모(doctorThresholds)로 돌리면 IN 쿼리 1회
// (수정 전 검사당 1회 = 6회).
func TestDoctorThresholdsSharedAcrossChecks(t *testing.T) {
	f := &fakeStore{queryFn: f6DoctorFn()}
	th := doctorThresholds(context.Background(), f)
	ctx := context.Background()
	n := 0
	for _, s := range doctorChecks() {
		if s.ThFn == nil {
			continue
		}
		runCheckSpec(ctx, f, th, s)
		n++
	}
	if n != 6 {
		t.Fatalf("ThFn 검사 %d종, want 6종(last-seen·backup-age·zero-near·pending-age·event-volume·meta-hygiene)", n)
	}
	if got := thresholdsLoads(f); got != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(실행 스코프 공유)", got)
	}
}

// TestDoctorFullRunThresholdsQueries — 전체 doctor 실행: 공유 1회 +
// checkSecrets 자체 로드 1회(F4 소유 — ThFn 전환 금지 영역) = 2회.
// secrets가 ThFn으로 편입되면 이 단언을 1로 조인다.
func TestDoctorFullRunThresholdsQueries(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir()) // 스킬 미탐지 — 별도 체인 생략
	f := &fakeStore{queryFn: f6DoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 && !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("doctor exit=%d: %s", code, out)
	}
	if got := thresholdsLoads(f); got != 2 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 2회(공유 1 + secrets 자체 1)", got)
	}
}
