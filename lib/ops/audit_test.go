// audit_test.go — v1.7.2 T2: audit 기본 쿼리 telemetry 커밋 제외 +
// --telemetry 플래그로 포함(--stat 동일). telemetry 커밋은 CommitTelemetry가
// 'telemetry: <cmd>' 접두로 남긴다(lib/db/repo.go 계약).
package ops

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// telemetryAuditRows — dolt_log 픽스처: telemetry 커밋 3 + 감사 커밋 2.
func telemetryAuditRows() [][]any {
	return [][]any{
		{"hash5", "committer-c", "2026-11-17 10:00:00", "telemetry: read"},
		{"hash4", "committer-c", "2026-11-17 09:00:00", "telemetry: write"},
		{"hash3", "committer-c", "2026-11-17 08:00:00", "telemetry: hot"},
		{"hash2", "committer-b", "2026-11-17 07:00:00", "knowledge: s [session:S2]"},
		{"hash1", "committer-a", "2026-11-17 06:00:00", "knowledge: t [session:S1]"},
	}
}

// telemetryAuditFake — 기본 쿼리의 message NOT LIKE 'telemetry: %'를 흉내
// 내는 queryFn. dolt_log 질의만 필터링하고 dolt_diff 질의는 빈 결과.
func telemetryAuditFake(t *testing.T, rows [][]any) (*fakeStore, *[]string) {
	t.Helper()
	logQueries := &[]string{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "dolt_diff") {
			return nil, nil
		}
		*logQueries = append(*logQueries, sql)
		if !strings.Contains(sql, "NOT LIKE") {
			return rows, nil
		}
		out := make([][]any, 0, len(rows))
		for _, r := range rows {
			if msg, ok := r[3].(string); ok && strings.HasPrefix(msg, "telemetry: ") {
				continue
			}
			out = append(out, r)
		}
		return out, nil
	}
	return f, logQueries
}

// decodeAuditLog — RunAudit 출력 파싱해 log 슬라이스만 반환.
func decodeAuditLog(t *testing.T, out string) []map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("출력 파싱: %v — %q", err, out)
	}
	lg, ok := m["log"].([]any)
	if !ok {
		t.Fatalf("log 키 부재: %q", out)
	}
	entries := make([]map[string]any, 0, len(lg))
	for _, e := range lg {
		entries = append(entries, e.(map[string]any))
	}
	return entries
}

// TestAuditTelemetryExcludedByDefault — Review Focus: telemetry 커밋 다수
// 상태에서 기본 목록에 telemetry 0건. 발행 SQL이 NOT LIKE 조건을 포함해야
// 한다(필터가 DB 측에서 일어남 — 클라이언트 후처리가 아님).
func TestAuditTelemetryExcludedByDefault(t *testing.T) {
	f, logQueries := telemetryAuditFake(t, telemetryAuditRows())
	var out bytes.Buffer
	if code := RunAudit(f, nil, &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	entries := decodeAuditLog(t, out.String())
	if len(entries) != 2 {
		t.Fatalf("log=%d행, want 2(telemetry 3건 제외)", len(entries))
	}
	for _, e := range entries {
		if msg := e["message"].(string); strings.HasPrefix(msg, "telemetry: ") {
			t.Fatalf("기본 목록에 telemetry 커밋 노출: %q", msg)
		}
	}
	if len(*logQueries) == 0 || !strings.Contains((*logQueries)[0], "NOT LIKE") {
		t.Fatalf("기본 쿼리에 NOT LIKE 부재: %v", *logQueries)
	}
}

// TestAuditTelemetryFlagIncludes — Review Focus: --telemetry 시 telemetry
// 커밋 포함(발행 SQL에서 NOT LIKE 조건 소거).
func TestAuditTelemetryFlagIncludes(t *testing.T) {
	f, logQueries := telemetryAuditFake(t, telemetryAuditRows())
	var out bytes.Buffer
	if code := RunAudit(f, []string{"--telemetry"}, &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	entries := decodeAuditLog(t, out.String())
	if len(entries) != 5 {
		t.Fatalf("log=%d행, want 5(telemetry 포함 전체)", len(entries))
	}
	telemetry := 0
	for _, e := range entries {
		if strings.HasPrefix(e["message"].(string), "telemetry: ") {
			telemetry++
		}
	}
	if telemetry != 3 {
		t.Fatalf("telemetry 커밋 %d건, want 3", telemetry)
	}
	if len(*logQueries) == 0 || strings.Contains((*logQueries)[0], "NOT LIKE") {
		t.Fatalf("--telemetry 쿼리에 NOT LIKE 잔존: %v", *logQueries)
	}
}

// TestAuditStatAppliesTelemetryFilter — --stat도 동일 필터: 기본은 NOT LIKE,
// --telemetry 조합은 제외.
func TestAuditStatAppliesTelemetryFilter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantNot bool
		wantLen int
	}{
		{"stat 기본 제외", []string{"--stat"}, true, 2},
		{"stat telemetry 포함", []string{"--stat", "--telemetry"}, false, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, logQueries := telemetryAuditFake(t, telemetryAuditRows())
			var out bytes.Buffer
			if code := RunAudit(f, tc.args, &out); code != 0 {
				t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
			}
			entries := decodeAuditLog(t, out.String())
			if len(entries) != tc.wantLen {
				t.Fatalf("log=%d행, want %d", len(entries), tc.wantLen)
			}
			hasNot := len(*logQueries) > 0 && strings.Contains((*logQueries)[0], "NOT LIKE")
			if hasNot != tc.wantNot {
				t.Fatalf("NOT LIKE 포함=%v, want %v — SQL: %v", hasNot, tc.wantNot, *logQueries)
			}
		})
	}
}
