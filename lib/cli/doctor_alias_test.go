// doctor_alias_test.go — v0.40.0 T1: lib/ops로 이동한 doctor_test.go의
// 테스트 헬퍼(runDoctorArgs·freshDoctorFn·setHarnessEnvs)를 잔류 cli
// 테스트(doctor_v07·doctor_v094·rules_sync_v097 등)에 재제공한다
// (이동분 동반 — v0.39 관례). 실구현은 ops 별칭(runDoctor) 경유.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// runDoctorArgs — fake store으로 runDoctor(=ops.RunDoctor) 실행.
// 출력·exit 반환.
func runDoctorArgs(t *testing.T, f *fakeStore, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := runDoctor(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// freshDoctorFn — 전 검사 통과 기준 응답(1시간 전 last-seen).
func freshDoctorFn() func(string, []any) ([][]any, error) {
	h := time.Now().Add(-time.Hour)
	return doctorQueryFn(h, h, h)
}

// doctorQueryFn — 검사별 meta/pg_indexes 응답을 주입하는 공용 queryFn.
// schema=4·인덱스 3행·last-seen 3키(경과 조절)를 반환한다.
func doctorQueryFn(lastWrite, lastHot, lastRead time.Time) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"4"}}, nil
		case strings.Contains(sql, "pg_indexes"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "last_write_at"):
			return [][]any{
				{"last_write_at", lastWrite.UTC().Format(time.RFC3339)},
				{"last_hot_at", lastHot.UTC().Format(time.RFC3339)},
				{"last_read_at", lastRead.UTC().Format(time.RFC3339)},
			}, nil
		}
		return nil, nil
	}
}

// setHarnessEnvs — 하니스 5종 env를 전부 빈 임시 디렉터리로 격리.
// harnessChecks 단위 테스트의 전제(실홈 설정 파일 영향 차단).
func setHarnessEnvs(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		"FLINT_CODEX_HOME", "FLINT_GEMINI_HOME", "FLINT_OPENCODE_HOME",
		"FLINT_COPILOT_HOME", "FLINT_CURSOR_DIR",
	} {
		t.Setenv(e, t.TempDir())
	}
}
