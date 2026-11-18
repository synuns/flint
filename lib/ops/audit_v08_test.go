// audit_v08_test.go — v1.8.0 T8: audit 목록 조회를 wire.LogQuery 기반
// fake로 검증(audit_test.go v1.7.2 T2 계승 — dolt_log 원시 SQL 단언을
// LogEntries 전달 조건 단언으로 환치). telemetry 커밋은 CommitTelemetry가
// 'telemetry: <cmd>' 접두로 남긴다(lib/db/repo.go 계약 — MessageNotLike).
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// auditLogRows — LogEntries 픽스처: telemetry 커밋 3 + 감사 커밋 2(최신순).
func auditLogRows() []wire.LogEntry {
	base := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	msgs := []struct {
		hash, committer, msg string
		off                  time.Duration
	}{
		{"hash5", "committer-c", "telemetry: read", 4 * time.Hour},
		{"hash4", "committer-c", "telemetry: write", 3 * time.Hour},
		{"hash3", "committer-c", "telemetry: hot", 2 * time.Hour},
		{"hash2", "committer-b", "knowledge: s [session:S2]", time.Hour},
		{"hash1", "committer-a", "knowledge: t [session:S1]", 0},
	}
	out := make([]wire.LogEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, wire.LogEntry{Hash: m.hash, Committer: m.committer,
			Message: m.msg, Date: base.Add(m.off)})
	}
	return out
}

// auditFakeStore — LogEntries·DiffStat 훅 오버라이드 fake(*fakeStore 승격
// 대신 명시 정의가 우선 — Query 등 기존 동작은 승격 유지). logQ·diffHashes는
// 수신 조건 기록(전달 단언). LogEntries fake는 MessageNotLike·MessageLike
// 필터와 LatestN clamp를 흉내 낸다.
type auditFakeStore struct {
	*fakeStore
	wire.HistoryStubs // DiffRows·DiffCommits 기본구현(오버라이드 대상 3종은 아래 명시 정의가 우선)
	logQ              []wire.LogQuery
	logEntriesErr     error
	diffCalls         int
	diffHashes        []string
	diffRows          []wire.TableDiffStat
	diffErr           error
}

func (f *auditFakeStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	f.logQ = append(f.logQ, q)
	if f.logEntriesErr != nil {
		return nil, f.logEntriesErr
	}
	rows := auditLogRows()
	out := make([]wire.LogEntry, 0, len(rows))
	for _, r := range rows {
		if q.MessageNotLike != "" && strings.HasPrefix(r.Message, strings.TrimSuffix(q.MessageNotLike, "%")) {
			continue
		}
		if q.MessageLike != "" && !strings.Contains(r.Message, strings.TrimSuffix(strings.TrimPrefix(q.MessageLike, "%"), "%")) {
			continue
		}
		out = append(out, r)
	}
	if q.LatestN > 0 && len(out) > q.LatestN {
		out = out[:q.LatestN]
	}
	return out, nil
}

func (f *auditFakeStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return nil, wire.ErrHistoryUnsupported
}

func (f *auditFakeStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	f.diffCalls++
	f.diffHashes = append(f.diffHashes, hashes...)
	if f.diffErr != nil {
		return nil, f.diffErr
	}
	return f.diffRows, nil
}

// newAuditFake — 기본 fakeStore 영동작 + 히스토리 훅.
func newAuditFake() *auditFakeStore {
	return &auditFakeStore{fakeStore: &fakeStore{}}
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

// lastLogQ — fake가 받은 LogEntries 조건(미수신 시 fatal).
func lastLogQ(t *testing.T, f *auditFakeStore) wire.LogQuery {
	t.Helper()
	if len(f.logQ) == 0 {
		t.Fatalf("LogEntries 미호출")
	}
	return f.logQ[len(f.logQ)-1]
}

// TestAuditTelemetryExcludedByDefault — Review Focus: telemetry 커밋 다수
// 상태에서 기본 목록에 telemetry 0건. 필터는 DB 측 조건으로 전달돼야
// 한다(MessageNotLike — 클라이언트 후처리가 아님).
func TestAuditTelemetryExcludedByDefault(t *testing.T) {
	f := newAuditFake()
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
	if q := lastLogQ(t, f); q.MessageNotLike != "telemetry: %" {
		t.Fatalf("MessageNotLike=%q, want %q", q.MessageNotLike, "telemetry: %")
	}
}

// TestAuditTelemetryFlagIncludes — Review Focus: --telemetry 시 telemetry
// 커밋 포함(전달 조건에서 MessageNotLike 소거).
func TestAuditTelemetryFlagIncludes(t *testing.T) {
	f := newAuditFake()
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
	if q := lastLogQ(t, f); q.MessageNotLike != "" {
		t.Fatalf("--telemetry 조건에 MessageNotLike 잔존: %q", q.MessageNotLike)
	}
}

// TestAuditStatAppliesTelemetryFilter — --stat도 동일 필터: 기본은
// MessageNotLike, --telemetry 조합은 제외.
func TestAuditStatAppliesTelemetryFilter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantNotLike string
		wantLen     int
	}{
		{"stat 기본 제외", []string{"--stat"}, "telemetry: %", 2},
		{"stat telemetry 포함", []string{"--stat", "--telemetry"}, "", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFake()
			var out bytes.Buffer
			if code := RunAudit(f, tc.args, &out); code != 0 {
				t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
			}
			entries := decodeAuditLog(t, out.String())
			if len(entries) != tc.wantLen {
				t.Fatalf("log=%d행, want %d", len(entries), tc.wantLen)
			}
			if q := lastLogQ(t, f); q.MessageNotLike != tc.wantNotLike {
				t.Fatalf("MessageNotLike=%q, want %q", q.MessageNotLike, tc.wantNotLike)
			}
		})
	}
}

// TestAuditSessionFilterPassedAsMessageLike — Review Focus: --session 필터는
// LogQuery.MessageLike('%[session:ID]%' 패턴)로 이관돼 DB 측 조건으로
// 전달된다(클라이언트 후처리·SQL 잔류 금지 계약).
func TestAuditSessionFilterPassedAsMessageLike(t *testing.T) {
	f := newAuditFake()
	var out bytes.Buffer
	if code := RunAudit(f, []string{"--session", "S2"}, &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	entries := decodeAuditLog(t, out.String())
	if len(entries) != 1 || entries[0]["message"] != "knowledge: s [session:S2]" {
		t.Fatalf("session 필터 결과 mismatch: %v", entries)
	}
	if q := lastLogQ(t, f); q.MessageLike != `%[session:S2]%` {
		t.Fatalf("MessageLike=%q, want %%[session:S2]%%", q.MessageLike)
	}
}

// TestAuditSessionEscapesLikeMeta — R40-C: --session ID에 LIKE 메타문자
// (%·_)가 포함되면 이스케이프되어 리터럴 일치로만 매칭된다(파라미터화
// 유지 — 패턴 조립 시 메타문자만 \로 이스케이프).
func TestAuditSessionEscapesLikeMeta(t *testing.T) {
	f := newAuditFake()
	var out bytes.Buffer
	if code := RunAudit(f, []string{"--session", `a%b_c\d`}, &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	want := `%[session:a\%b\_c\\d]%`
	if q := lastLogQ(t, f); q.MessageLike != want {
		t.Fatalf("MessageLike=%q, want %q — 메타문자 미이스케이프", q.MessageLike, want)
	}
}

// TestAuditLimitClamp — limit clamp 계약(기본 20·0/음수 → 20·상한 100)은
// LogQuery.LatestN 전달 값으로 담보.
func TestAuditLimitClamp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{nil, 20},
		{[]string{"--limit", "0"}, 20},
		{[]string{"--limit", "-3"}, 20},
		{[]string{"--limit", "5"}, 5},
		{[]string{"--limit", "500"}, 100},
	} {
		f := newAuditFake()
		var out bytes.Buffer
		if code := RunAudit(f, tc.args, &out); code != 0 {
			t.Fatalf("%v exit=%d, want 0; out=%s", tc.args, code, out.String())
		}
		if q := lastLogQ(t, f); q.LatestN != tc.want {
			t.Fatalf("%v LatestN=%d, want %d", tc.args, q.LatestN, tc.want)
		}
	}
}

// TestAuditStatMapsDiffStat — Review Focus: --stat 부록은 DiffStat 배치
// 조회([]TableDiffStat) → log[].stat 매핑. 살균된 CommitHash로 해시 목록을
// 건네고 data_change·schema_change·date_rel이 첨부된다.
func TestAuditStatMapsDiffStat(t *testing.T) {
	f := newAuditFake()
	f.diffRows = []wire.TableDiffStat{
		{CommitHash: "hash2", Table: "knowledge", DataChange: true, SchemaChange: false},
		{CommitHash: "hash2", Table: "meta", DataChange: false, SchemaChange: true},
	}
	var out bytes.Buffer
	if code := RunAudit(f, []string{"--stat"}, &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	entries := decodeAuditLog(t, out.String())
	if len(entries) != 2 {
		t.Fatalf("log=%d행, want 2", len(entries))
	}
	if f.diffCalls != 1 {
		t.Fatalf("DiffStat 호출 %d회, want 1(배치)", f.diffCalls)
	}
	if strings.Join(f.diffHashes, ",") != "hash2,hash1" {
		t.Fatalf("DiffStat 해시 목록 mismatch: %v", f.diffHashes)
	}
	byHash := map[string]map[string]any{}
	for _, e := range entries {
		byHash[e["commit_hash"].(string)] = e
	}
	h2 := byHash["hash2"]
	if h2 == nil {
		t.Fatalf("hash2 미노출: %v", entries)
	}
	stat, ok := h2["stat"].(map[string]any)
	if !ok {
		t.Fatalf("hash2 stat 부재: %v", h2)
	}
	kn, ok := stat["knowledge"].(map[string]any)
	if !ok || kn["data_change"] != true || kn["schema_change"] != false {
		t.Fatalf("knowledge stat mismatch: %v", stat)
	}
	meta, ok := stat["meta"].(map[string]any)
	if !ok || meta["data_change"] != false || meta["schema_change"] != true {
		t.Fatalf("meta stat mismatch: %v", stat)
	}
	if h2["date_rel"] == "" {
		t.Fatalf("date_rel 미기재: %v", h2)
	}
	// stat 미수신 커밋(hash1)은 stat 키 자체가 없어야 한다(omitempty).
	if _, has := byHash["hash1"]["stat"]; has {
		t.Fatalf("hash1에 stat 잔류: %v", byHash["hash1"])
	}
}

// TestAuditDiffStatErrorPropagates — DiffStat 오류는 기존 질의 오류와 같은
// exit 매핑(store.MapExit)으로 전파.
func TestAuditDiffStatErrorPropagates(t *testing.T) {
	f := newAuditFake()
	f.diffErr = context.DeadlineExceeded
	var out bytes.Buffer
	if code := RunAudit(f, []string{"--stat"}, &out); code == 0 {
		t.Fatalf("exit=0, want != 0; out=%s", out.String())
	}
}

// TestAuditLogEntriesErrorPropagates — LogEntries 오류 전파(exit 매핑).
func TestAuditLogEntriesErrorPropagates(t *testing.T) {
	f := newAuditFake()
	f.fakeStore.queryErr = nil
	f.logQ = nil
	f.diffHashes = nil
	// LogEntries 훅 오류 주입 — auditFakeStore.LogEntries를 치환.
	prev := f.logEntriesErr
	f.logEntriesErr = context.DeadlineExceeded
	t.Cleanup(func() { f.logEntriesErr = prev })
	var out bytes.Buffer
	if code := RunAudit(f, nil, &out); code == 0 {
		t.Fatalf("exit=0, want != 0; out=%s", out.String())
	}
}
