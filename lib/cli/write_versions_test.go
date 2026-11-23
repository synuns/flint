package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"strings"
	"testing"
)

type dupMetaStore struct {
	*fakeStore
	meta DupMeta
}

func (d *dupMetaStore) DupRowByHash(ctx context.Context, hash string) (DupMeta, bool, error) {
	return d.meta, d.meta.ID > 0, nil
}

func withDupMeta(t *testing.T, d *dupMetaStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

func TestWriteDupFeedback(t *testing.T) {
	f := &dupMetaStore{fakeStore: &fakeStore{}, meta: DupMeta{ID: 7, Topic: "doltgres 포트 충돌", State: "active"}}
	withDupMeta(t, f)

	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"kind":"knowledge","topic":"doltgres 포트 충돌","summary":"5434 점유 시 이동","tags":["doltgres"],"confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "duplicate" {
		t.Fatalf("status=%v, want duplicate", m["status"])
	}
	if v, _ := m["id"].(float64); int(v) != 7 {
		t.Fatalf("id=%v, want 7", m["id"])
	}
	if m["topic"] != "doltgres 포트 충돌" {
		t.Fatalf("topic=%v, want 기존 행 주제", m["topic"])
	}
	if m["state"] != "active" {
		t.Fatalf("state=%v, want active", m["state"])
	}
}

func TestWriteDupMetaAbsent(t *testing.T) {
	f := &fakeStore{exists: true}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"kind":"knowledge","topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("out=%s, want bare duplicate(폴백 바이트 불변)", out)
	}
}

type usageCall struct{ kind, source string }

type writeUsageRecorder struct {
	Store
	calls []usageCall
	err   error
}

func (u *writeUsageRecorder) RecordUsage(ctx context.Context, kind, source string) error {
	u.calls = append(u.calls, usageCall{kind, source})
	return u.err
}

func withRecorder(t *testing.T, f *fakeStore) *writeUsageRecorder {
	t.Helper()
	rec := &writeUsageRecorder{Store: f}
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return rec, nil }
	t.Cleanup(func() { newStore = prev })
	return rec
}

func TestParseSourceFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		err  error
	}{
		{"hook", []string{"--source", "hook"}, "hook", nil},
		{"manual", []string{"--source", "manual"}, "manual", nil},
		{"mcp", []string{"--source", "mcp"}, "mcp", nil},
		{"default", nil, "manual", nil},
		{"other-flags", []string{"write", "knowledge", "--session", "s"}, "manual", nil},
		{"invalid", []string{"--source", "bad"}, "", ErrInvalidSource},
		{"empty", []string{"--source", ""}, "", ErrInvalidSource},
		{"dangling", []string{"--source"}, "", ErrInvalidSource},
	}
	for _, tc := range cases {
		got, err := ParseSourceFlag(tc.args)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Fatalf("%s: ParseSourceFlag(%v) = (%q,%v), want (%q,%v)",
				tc.name, tc.args, got, err, tc.want, tc.err)
		}
	}
}

func TestWriteSourceInvalidExit3(t *testing.T) {
	f := &fakeStore{}
	withRecorder(t, f)
	out, code := mustRun(t, []string{"write", "knowledge", "--source", "bad"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "error") {
		t.Fatalf("출력=%q, want error JSON", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("무효 소스 시 삽입 금지; 삽입됨 %d건", len(f.inserted))
	}
}

func TestWriteRecordsUsage(t *testing.T) {
	t.Run("default-manual", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		_, code := mustRun(t, []string{"write", "knowledge"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if len(rec.calls) != 1 || rec.calls[0] != (usageCall{"write", "manual"}) {
			t.Fatalf("calls=%v, want [write manual]", rec.calls)
		}
	})
	t.Run("source-hook", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		_, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if len(rec.calls) != 1 || rec.calls[0] != (usageCall{"write", "hook"}) {
			t.Fatalf("calls=%v, want [write hook]", rec.calls)
		}
	})
	t.Run("usage-error-silent", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{})
		rec.err = errors.New("usage down")
		out, code := mustRun(t, []string{"write", "knowledge"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d, want 0(계측 실패 조용히); out=%s", code, out)
		}
	})
}

func TestWriteDupCountsOnceEach(t *testing.T) {

	payload := `{"topic":"t","summary":"s","confidence":0.9}`
	t.Run("exists-hash", func(t *testing.T) {
		rec := withRecorder(t, &fakeStore{exists: true})
		out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"}, payload)
		if code != 0 || strings.TrimSpace(out) != `{"status":"duplicate"}` {
			t.Fatalf("exit=%d out=%s, want duplicate/0", code, out)
		}
		want := []usageCall{{"write", "hook"}, {"dup", ""}, {"dup_hook", ""}}
		if len(rec.calls) != 3 || rec.calls[0] != want[0] || rec.calls[1] != want[1] || rec.calls[2] != want[2] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
	t.Run("insert-batch-dup", func(t *testing.T) {

		rs := &raceDupStore{fakeStore: &fakeStore{}, racedDups: 1}
		rec := &writeUsageRecorder{Store: rs}
		prev := newStore
		newStore = func(ctx context.Context, dsn string) (Store, error) { return rec, nil }
		t.Cleanup(func() { newStore = prev })
		out, code := mustRun(t, []string{"write", "knowledge", "--source", "mcp"},
			`{"topic":"t","summary":"s","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("exit=%d out=%s, want 0", code, out)
		}
		if !strings.Contains(out, "duplicate") {
			t.Fatalf("out=%s, want duplicate", out)
		}
		want := []usageCall{{"write", "mcp"}, {"dup", ""}, {"dup_mcp", ""}}
		if len(rec.calls) != 3 || rec.calls[0] != want[0] || rec.calls[1] != want[1] || rec.calls[2] != want[2] {
			t.Fatalf("calls=%v, want %v", rec.calls, want)
		}
	})
}

func TestWriteNoUsageStoreNoop(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge", "--source", "hook"},
		`{"topic":"t","summary":"s","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(no-op 계측); out=%s", code, out)
	}
}

func TestDuplicateSingleLineCarriesExistingID(t *testing.T) {
	if got := store.OutputJSON("duplicate", "", "", 109); got != `{"status":"duplicate","id":109}` {
		t.Errorf("duplicate id 노출 실패: %s", got)
	}
}

func TestDuplicateZeroIDStaysBare(t *testing.T) {
	if got := store.OutputJSON("duplicate", "", "", 0); got != `{"status":"duplicate"}` {
		t.Errorf("id 0 duplicate 출력 변형: %s", got)
	}
}

type dupIDStore struct {
	*fakeStore
	id int
}

func (d *dupIDStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return d.id, true, nil
}

func (d *dupIDStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if strings.Contains(sql, "content_hash = ANY($1)") {
		if hashes, ok := args[0].([]string); ok {
			rows := make([][]any, 0, len(hashes))
			for _, h := range hashes {
				rows = append(rows, []any{h, d.id, "", ""})
			}
			return rows, nil
		}
	}
	return d.fakeStore.Query(ctx, sql, args...)
}

func withDupID(t *testing.T, d *dupIDStore) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return d, nil }
	t.Cleanup(func() { newStore = prev })
}

func TestWriteDuplicateIncludesID(t *testing.T) {
	ds := &dupIDStore{fakeStore: &fakeStore{}, id: 109}
	withDupID(t, ds)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"dup-id","summary":"재전송","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate","id":109}` {
		t.Fatalf("출력=%q, want 기존 id 노출", out)
	}
	if len(ds.inserted) != 0 || len(ds.commits) != 0 {
		t.Fatalf("재전송은 0건 커밋: 삽입 %d·커밋 %d", len(ds.inserted), len(ds.commits))
	}
}

func TestWriteDuplicateMultiStaysBare(t *testing.T) {
	ds := &dupIDStore{fakeStore: &fakeStore{}, id: 109}
	withDupID(t, ds)
	jsonl := `{"topic":"dup-m1","summary":"a","confidence":0.9}` + "\n" +
		`{"topic":"dup-m2","summary":"b","confidence":0.9}`
	out, code := mustRun(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("출력=%q, want id 없는 duplicate 한 줄", out)
	}
}

func runScratch(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func TestWriteCorrectionE2ERealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	const topic = "v1110-t2-e2e-correction"
	stdin := fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 줄"}`+"\n", topic)
	out, code := runScratch(t, []string{"write", "knowledge", "--source", "correction"}, stdin)
	if code != 0 {
		t.Fatalf("적립 exit=%d, want 0; out=%s", code, out)
	}
	var w map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &w); err != nil {
		t.Fatalf("적립 출력 파싱: %v — %s", err, out)
	}
	if w["status"] != "committed" || w["state"] != "pending" {
		t.Fatalf("적립 출력=%v, want committed/pending(기본 0.7 < floor)", w)
	}
	idf, ok := w["id"].(float64)
	if !ok || idf < 1 {
		t.Fatalf("id=%v, want 1 이상", w["id"])
	}
	id := int(idf)

	fields := []string{"--fields", "id,topic,confidence,tags"}
	out, code = runScratch(t, append([]string{"read", topic, "--json", "--status", "pending"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("pending 회상=%q, want topic·correction 태그·confidence 0.7", out)
	}

	out, code = runScratch(t, []string{"approve", "--id", fmt.Sprint(id)}, "")
	if code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Fatalf("approve exit=%d out=%q, want approved", code, out)
	}

	out, code = runScratch(t, append([]string{"read", topic, "--json"}, fields...), "")
	if code != 0 {
		t.Fatalf("active read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) || !strings.Contains(out, "correction") || !strings.Contains(out, "0.7") {
		t.Fatalf("active 회상=%q, want topic·correction 태그·confidence 0.7(승격 후 active)", out)
	}
}

func TestGapCandidateE2ERealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	out, code := runScratch(t, []string{"write", "knowledge"},
		`{"topic":"합성 시드 지식","summary":"합성 데모 시드 줄","confidence":0.9}`+"\n")
	if code != 0 || !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("시드 적립 exit=%d out=%q", code, out)
	}

	query := strings.Repeat("데모", 45)
	for i := 1; i <= 3; i++ {
		out, code = runScratch(t, []string{"read", query, "--json"}, "")
		if code != 0 {
			t.Fatalf("read %d exit=%d, want 0(제로히트); out=%s", i, code, out)
		}
	}

	fields := []string{"--json", "--fields", "id,topic,summary,confidence,tags", "--status", "pending"}
	out, code = runScratch(t, append([]string{"read", "격차 후보"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending read exit=%d, want 0; out=%s", code, out)
	}
	wantTopic := "격차 후보: " + render.TruncateRunes(knowledge.NormalizeGapQuery(query), 60)
	if !strings.Contains(out, wantTopic) {
		t.Fatalf("pending 회상 토픽 누락: want %q in %q", wantTopic, out)
	}
	if !strings.Contains(out, "회상 실패 3회 — 지식 격차 후보") || !strings.Contains(out, "gap-candidate") || !strings.Contains(out, "0.4") {
		t.Fatalf("pending 회상=%q, want 요약 3회·gap-candidate 태그·confidence 0.4", out)
	}
	id := extractJSONInt(t, out, "id")

	out, code = runScratch(t, []string{"read", query, "--json"}, "")
	if code != 0 {
		t.Fatalf("read 4 exit=%d, want 0; out=%s", code, out)
	}
	out, code = runScratch(t, append([]string{"read", "격차 후보"}, fields...), "")
	if code != 0 {
		t.Fatalf("pending 재회상 exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, "회상 실패 4회 — 지식 격차 후보") {
		t.Fatalf("pending 갱신 실패: %q, want 요약 4회(카운터 리셋 아님·갱신 지속)", out)
	}
	if !strings.Contains(out, wantTopic) {
		t.Fatalf("pending 갱신 후 토픽 변형: %q", out)
	}

	out, code = runScratch(t, []string{"approve", "--id", fmt.Sprint(id)}, "")
	if code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Fatalf("approve exit=%d out=%q, want approved", code, out)
	}

	out, code = runScratch(t, []string{"read", "격차 후보", "--json", "--fields", "id,topic,summary,tags"}, "")
	if code != 0 {
		t.Fatalf("active read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, wantTopic) || !strings.Contains(out, "gap-candidate") {
		t.Fatalf("active 회상=%q, want 토픽·gap-candidate 태그", out)
	}

	out, code = runScratch(t, []string{"read", query, "--json"}, "")
	if code != 0 {
		t.Fatalf("read 5 exit=%d, want 0; out=%s", code, out)
	}
	out, code = runScratch(t, []string{"read", "격차 후보", "--json", "--status", "pending", "--fields", "id,topic,summary"}, "")
	if code != 0 || strings.Contains(out, "격차 후보") {
		t.Fatalf("active NOOP 위반: pending 잔존 exit=%d out=%q", code, out)
	}
}

func extractJSONInt(t *testing.T, out, key string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, `"`+key+`":`)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len(key)+3:])
		end := strings.IndexAny(rest, ",}")
		if end < 0 {
			continue
		}
		cell := strings.Trim(strings.TrimSpace(rest[:end]), `"`)
		var n int
		if _, err := fmt.Sscanf(cell, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	t.Fatalf("id 추출 실패: %q", out)
	return 0
}

const (
	echoE2ETopic     = "데모 프로젝트 운영 기록"
	echoE2EDupSum    = "회의록 오늘 확정된 마일스톤은 3개다"
	echoE2EContraSum = "회의록 다음 주는 전체 휴가 기간이다"
	echoE2EOtherSum  = "텔레비전 편성표 조정 작업 기록과 성수기 특별 편성 반영시 검토해야 할 광고 단가 구간표 초안 내용을 함께 기록한다"
)

func TestWriteEchoE2ERealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	run := func(topic, summary string, conf float64) map[string]any {
		t.Helper()
		var out strings.Builder
		line := fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":%f}`+"\n", topic, summary, conf)
		code := Run([]string{"write", "knowledge"}, strings.NewReader(line), &out)
		if code != 0 {
			t.Fatalf("write exit=%d out=%s", code, out.String())
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
			t.Fatalf("출력 파싱: %v — %s", err, out.String())
		}
		return m
	}

	m := run(echoE2ETopic, echoE2EDupSum, 0.9)
	if m["status"] != "committed" || m["state"] != "active" {
		t.Fatalf("적립 #1=%v, want committed/active", m)
	}
	seedID, _ := m["id"].(float64)
	if seedID < 1 {
		t.Fatalf("적립 #1 id=%v, want 1 이상", m["id"])
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("빈 저장소 첫 쓰기에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EDupSum, 0.9)
	if m["status"] != "duplicate" {
		t.Fatalf("재전송=%v, want duplicate", m)
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("정확-hash 중복 응답에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EContraSum, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("상이 주장 적립=%v, want committed", m)
	}
	echo, ok := m["echo"].([]any)
	if !ok || len(echo) < 1 {
		t.Fatalf("echo=%v, want 1건 이상", m["echo"])
	}
	c, _ := echo[0].(map[string]any)
	if c["id"] != seedID {
		t.Errorf("echo 후보 id=%v, want 시드 %v", c["id"], seedID)
	}
	if c["relation"] != "contradicts" {
		t.Errorf("echo relation=%v, want contradicts", c["relation"])
	}
	if _, ok := c["score"]; !ok {
		t.Errorf("echo 후보 score 필드 부재: %v", c)
	}
	if _, ok := c["similarity"]; ok {
		t.Errorf("echo 후보에 similarity 필드는 부재해야 함: %v", c)
	}

	m = run(echoE2ETopic, echoE2EOtherSum, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("이질 요약 적립=%v, want committed", m)
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("임계 미달 쓰기에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EContraSum+" (개정안)", 0.4)
	if m["status"] != "committed" || m["state"] != "pending" {
		t.Fatalf("저신뢰 적립=%v, want committed/pending", m)
	}
	pendID, _ := m["id"].(float64)
	var out strings.Builder
	code := Run([]string{"approve", "--id", fmt.Sprint(int(pendID)), "--supersede", fmt.Sprint(int(seedID))},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("approve --supersede exit=%d out=%s", code, out.String())
	}

	var ro strings.Builder
	if code := Run([]string{"read", echoE2ETopic, "--json"}, strings.NewReader(""), &ro); code != 0 {
		t.Fatalf("read exit=%d out=%s", code, ro.String())
	}
	if strings.Contains(ro.String(), fmt.Sprintf(`"id":%q`, fmt.Sprint(int(seedID)))) {
		t.Errorf("슈퍼시드된 시드 행이 여전히 활성: %s", ro.String())
	}
	if !strings.Contains(ro.String(), fmt.Sprintf(`"id":%q`, fmt.Sprint(int(pendID)))) {
		t.Errorf("승격 행이 활성 뷰에 부재: %s", ro.String())
	}
}
