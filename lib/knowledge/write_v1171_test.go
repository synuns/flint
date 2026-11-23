package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

func TestWriteCorrectionOriginDefault(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1171 교정 데모","summary":"합성 데모 교정 행"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["origin"]; got != "correction" {
		t.Fatalf("origin=%v, want correction(approve 관측 키의 원천)", got)
	}
	if got := f.inserted[0]["status"]; got != "pending" {
		t.Fatalf("status=%v, want pending(기본 confidence 0.7 < conf_floor)", got)
	}
}

func TestWriteCorrectionOriginExplicitRespected(t *testing.T) {
	f := &fakeStore{}
	out, code := run(t, f, []string{"knowledge", "--source", "correction"},
		`{"topic":"v1171 교정 데모","summary":"합성 데모 교정 행","origin":"human"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if got := f.inserted[0]["origin"]; got != "human" {
		t.Fatalf("origin=%v, want human(명시값 존중)", got)
	}
}

func TestGapCandidateOriginGap(t *testing.T) {
	f := &gapAccrualStore{fakeStore: &fakeStore{}, dupRows: map[string]DupMeta{}}
	maybeAccrueGapCandidate(context.Background(), f, "데모 쿼리", 3)
	if len(f.inserted) != 1 {
		t.Fatalf("삽입=%d건, want 1", len(f.inserted))
	}
	if got := f.inserted[0]["origin"]; got != "gap" {
		t.Fatalf("origin=%v, want gap(approve 관측 키의 원천)", got)
	}
	if got := f.inserted[0]["content_hash"]; got != gapCandidateHash(NormalizeGapQuery("데모 쿼리")) {
		t.Fatalf("content_hash=%v, want N-invariant gapCandidateHash(불변)", got)
	}
}

func TestWriteEchoNoExtraThresholdLoad(t *testing.T) {
	t.Cleanup(func() { echoScanCap, echoDupThreshold = 0, 0 })
	const thSQL = "SELECT key, value FROM meta WHERE key IN ("
	loads := 0
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

		if strings.Contains(sql, thSQL) && !strings.Contains(sql, "vocab_") {
			loads++
		}
		return nil, nil
	}}
	out, code := run(t, f, []string{"knowledge"},
		`{"topic":"v1171 에코 데모","summary":"합성 데모 행","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if loads != 2 {
		t.Fatalf("threshold 로드=%d회, want 2(행 상한+쓰기 결정 — 에코가 추가 로드하지 않음)", loads)
	}
}

func TestEchoScanBoundsSeamSkipsMetaLoad(t *testing.T) {
	t.Cleanup(func() { echoScanCap, echoDupThreshold = 0, 0 })
	const thSQL = "SELECT key, value FROM meta WHERE key IN ("
	var queries []string
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		queries = append(queries, sql)
		return nil, nil
	}}
	SetEchoScanBounds(500, 0.6)
	if _, err := EchoScan(context.Background(), f, echoTestTopic, echoDupSummary); err != nil {
		t.Fatalf("EchoScan 에러: %v", err)
	}
	joined := strings.Join(queries, ";")
	if strings.Contains(joined, thSQL) {
		t.Fatalf("주입된 바운드에도 meta threshold 쿼리 발화: %q", joined)
	}
	if !strings.Contains(joined, "LIMIT 500") {
		t.Fatalf("스캔 SQL에 주입 LIMIT 500 부재: %q", joined)
	}

	echoScanCap, echoDupThreshold = 0, 0
	queries = nil
	if _, err := EchoScan(context.Background(), f, echoTestTopic, echoDupSummary); err != nil {
		t.Fatalf("EchoScan(파생) 에러: %v", err)
	}
	if !strings.Contains(strings.Join(queries, ";"), "LIMIT 5000") {
		t.Fatalf("미주입 시 기본 LIMIT 5000 부재: %q", strings.Join(queries, ";"))
	}
}

func TestWriteCorrectionApproveFunnelRealSlow(t *testing.T) {
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	const marker = "v1171-r64-합성-"
	t.Cleanup(func() {
		_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)
	})
	_, _ = st.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE $1 || '%'`, marker)

	topic := marker + time.Now().UTC().Format("150405.000000000")
	var out strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge", "--source", "correction"},
		strings.NewReader(fmt.Sprintf(`{"topic":%q,"summary":"합성 데모 교정 행","confidence":0.1}`, topic)), &out); code != 0 {
		t.Fatalf("write exit=%d out=%s", code, out.String())
	}
	var w map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &w); err != nil {
		t.Fatalf("write 출력 파싱: %v (%s)", err, out.String())
	}
	if w["state"] != "pending" {
		t.Fatalf("write state=%v, want pending(명시 confidence 0.1)", w["state"])
	}
	id := int(w["id"].(float64))

	rows, err := st.Query(ctx, `SELECT origin, status FROM knowledge WHERE id = $1`, id)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 조회 실패: %v rows=%d", err, len(rows))
	}
	if got := fmt.Sprint(rows[0][0]); got != "correction" {
		t.Fatalf("저장 origin=%q, want correction", got)
	}

	today := time.Now().UTC().Format("2006-01-02")
	key := "daily:correction_approved:" + today
	before := metaCounterValue(ctx, t, st, key)
	var aout strings.Builder
	if code := RunApprove(ctx, st, []string{"--id", fmt.Sprint(id)},
		strings.NewReader(""), &aout); code != 0 {
		t.Fatalf("approve exit=%d out=%s", code, aout.String())
	}
	after := metaCounterValue(ctx, t, st, key)
	if after != before+1 {
		t.Fatalf("correction_approved 증분=%d, want 1(before=%d after=%d)", after-before, before, after)
	}
}

func metaCounterValue(ctx context.Context, t *testing.T, st wire.Store, key string) int {
	t.Helper()
	rows, err := st.Query(ctx, `SELECT value FROM meta WHERE key = $1`, key)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	n := 0
	if _, err := fmt.Sscan(fmt.Sprint(rows[0][0]), &n); err != nil {
		return 0
	}
	return n
}
