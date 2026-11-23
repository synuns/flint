package knowledge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
)

func mdSampleRows() []ExportRow {
	superseded := 90
	return []ExportRow{
		{
			ID:           88,
			Topic:        "배포 관례",
			Content:      "본문 1줄",
			Kind:         "rule",
			Status:       "active",
			Confidence:   0.9,
			Tags:         []string{"deploy", "ops"},
			Entities:     []string{"deploy"},
			Domain:       "ops",
			Source:       "manual",
			ContentHash:  "abc123",
			CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
			SupersededBy: &superseded,
		},
		{
			ID:         91,
			Topic:      "리뷰 관례",
			Content:    "본문 2행",
			Kind:       "rule",
			Status:     "active",
			Confidence: 0.8,
			Tags:       []string{"review"},
			Source:     "hook",
			CreatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
			UpdatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		},
	}
}

func TestFormatMDStructure(t *testing.T) {
	out := FormatMD(mdSampleRows())
	s := string(out)

	if !bytes.HasPrefix(out, []byte("# flint export\n")) {
		t.Fatalf("헤더 라인 누락: %q", firstLine(s))
	}
	if !regexp.MustCompile(`(?m)^created: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(s) {
		t.Errorf("생성일(UTC RFC3339) 라인 누락:\n%s", s)
	}
	if want := "items: 2"; !bytes.Contains(out, []byte(want)) {
		t.Errorf("항목 수 라인 %q 누락", want)
	}
	if got := bytes.Count(out, []byte("\n## #")); got != 2 {
		t.Errorf("섹션 수 = %d, want 2", got)
	}

	wantMeta1 := "status: active · confidence: 0.9 · tags: deploy|ops · source: manual · created: 2026-01-02T03:04:05Z · superseded_by: → #90"
	if !bytes.Contains(out, []byte(wantMeta1)) {
		t.Errorf("메타 라인(계보 링크 포함) 불일치:\nwant %q\n전체:\n%s", wantMeta1, s)
	}
	wantMeta2 := "status: active · confidence: 0.8 · tags: review · source: hook · created: 2026-03-04T05:06:07Z"
	if !bytes.Contains(out, []byte(wantMeta2)) {
		t.Errorf("메타 라인(superseded 없음) 불일치:\nwant %q", wantMeta2)
	}
	if !bytes.Contains(out, []byte("## #88 배포 관례")) {
		t.Errorf("섹션 헤딩 `## #88 배포 관례` 누락")
	}
}

func TestFormatMDNoSupersededLeak(t *testing.T) {
	out := string(FormatMD(mdSampleRows()))
	if n := regexp.MustCompile(`superseded_by: → #`).FindAllString(out, -1); len(n) != 1 {
		t.Errorf("superseded_by 링크 개수 = %d, want 1(nil은 미출력):\n%s", len(n), out)
	}
}

func TestFormatMDEmpty(t *testing.T) {
	out := FormatMD(nil)
	s := string(out)
	if !bytes.HasPrefix(out, []byte("# flint export\n")) {
		t.Fatalf("빈 결과에도 헤더 필요: %q", s)
	}
	if !bytes.Contains(out, []byte("items: 0")) {
		t.Errorf("빈 결과 items: 0 누락:\n%s", s)
	}
	if bytes.Contains(out, []byte("\n## #")) {
		t.Errorf("빈 결과에 섹션 존재:\n%s", s)
	}
}

func TestFormatMDContentVerbatim(t *testing.T) {
	rows := []ExportRow{{
		ID:        7,
		Topic:     "C# 네이밍 #관례",
		Content:   "1줄\n2줄\n\n4줄",
		Status:    "active",
		Source:    "manual",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
	out := string(FormatMD(rows))
	if !bytes.Contains([]byte(out), []byte("## #7 C# 네이밍 #관례")) {
		t.Errorf("topic 헤딩 원문 누락:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("1줄\n2줄\n\n4줄")) {
		t.Errorf("content 개행 원문 미보존:\n%q", out)
	}
}

func TestFormatMDChainNotation(t *testing.T) {
	out := string(FormatMD(mdSampleRows(), map[int][]int{88: {90, 91}}))
	want := "superseded_by: → #90 → #91 (depth 2)"
	if !strings.Contains(out, want) {
		t.Errorf("체인 표기 %q 누락:\n%s", want, out)
	}
	if n := strings.Count(out, "superseded_by:"); n != 1 {
		t.Errorf("superseded_by 프래그먼트 수 = %d, want 1(무체인 행 미노출):\n%s", n, out)
	}
}

func TestFormatMDChainTerminalOneHop(t *testing.T) {
	out := string(FormatMD(mdSampleRows(), map[int][]int{88: {90}}))
	want := "superseded_by: → #90 (depth 1)"
	if !strings.Contains(out, want) {
		t.Errorf("1홉 종착 표기 %q 불일치:\n%s", want, out)
	}
}

func TestFormatMDChainFallbackNoEntry(t *testing.T) {
	out := string(FormatMD(mdSampleRows(), map[int][]int{91: {99}}))
	want := "superseded_by: → #90"
	if !strings.Contains(out, want) {
		t.Errorf("폴백 1홉 표기 %q 불일치:\n%s", want, out)
	}
	if strings.Contains(out, "(depth") {
		t.Errorf("폴백 표기에 depth 주석 누출:\n%s", out)
	}
}

func TestSupersedeChainsBuildsMap(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "FROM supersede_chains") {
			t.Errorf("뷰 미소비(SQL): %s", sql)
		}
		if !strings.Contains(sql, "depth > 0") {
			t.Errorf("depth 0(루트 자신) 미제외: %s", sql)
		}

		return [][]any{
			{int32(7), int64(8), int32(1)},
			{int32(7), int64(9), int32(2)},
			{int64(9), int64(10), int64(1)},
		}, nil
	}}
	m, err := SupersedeChains(context.Background(), st)
	if err != nil {
		t.Fatalf("SupersedeChains: %v", err)
	}
	want := map[int][]int{7: {8, 9}, 9: {10}}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("체인 맵 = %v, want %v", m, want)
	}
}

func TestSupersedeChainsQueryError(t *testing.T) {
	boom := errors.New("boom")
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, boom
	}}
	if _, err := SupersedeChains(context.Background(), st); err != boom {
		t.Errorf("오류 전파 불일치: %v", err)
	}
}

func mdSlowDSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") != "1" || os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	return os.Getenv("FLINT_TEST_DSN")
}

const mdChainHashPrefix = "mdchain-"

func seedMDChainRows(t *testing.T, pool *pgxpool.Pool, n int) []int {
	t.Helper()
	ctx := context.Background()
	del := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM knowledge WHERE content_hash LIKE $1`, mdChainHashPrefix+"%")
	}
	del()
	t.Cleanup(del)
	ids := make([]int, n)
	for i := range ids {
		hash := fmt.Sprintf("%s%02d-%d", mdChainHashPrefix, i, time.Now().UnixNano())
		var id int
		if err := pool.QueryRow(ctx, `INSERT INTO knowledge
			(topic, summary, tags, confidence, origin, status, content_hash)
			VALUES ($1, $2, ':demo:', 0.9, 'agent', 'active', $3) RETURNING id`,
			fmt.Sprintf("체인 데모 %02d", i), "합성 요약", hash).Scan(&id); err != nil {
			t.Skipf("fixture 시딩 실패(스키마 미구비 의심): %v", err)
		}
		ids[i] = id
	}
	return ids
}

type mdHop struct {
	id    int
	depth int
}

func mdChainRows(t *testing.T, pool *pgxpool.Pool, root int) []mdHop {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id, depth FROM supersede_chains WHERE root_id = $1 ORDER BY depth`, root)
	if err != nil {
		t.Fatalf("뷰 질의 root=%d: %v", root, err)
	}
	defer rows.Close()
	var out []mdHop
	for rows.Next() {
		var h mdHop
		if err := rows.Scan(&h.id, &h.depth); err != nil {
			t.Fatalf("뷰 스캔: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("뷰 순회: %v", err)
	}
	return out
}

func mdHopsEqual(got []mdHop, want []mdHop) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSlowSupersedeChainsView(t *testing.T) {
	dsn := mdSlowDSN(t)
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real db unavailable: %v", err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("pool unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	ids := seedMDChainRows(t, pool, 6)
	root, mid, end, solo, c1, c2 := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]
	setRef := func(from, to int) {
		if _, err := pool.Exec(ctx,
			`UPDATE knowledge SET superseded_by = $2 WHERE id = $1`, from, to); err != nil {
			t.Fatalf("참조 설정 %d→%d: %v", from, to, err)
		}
	}
	setRef(root, mid)
	setRef(mid, end)

	setRef(c1, c2)
	setRef(c2, c1)

	if got := mdChainRows(t, pool, root); !mdHopsEqual(got, []mdHop{{root, 0}, {mid, 1}, {end, 2}}) {
		t.Errorf("3홉 전개 = %v, want [%d 0 · %d 1 · %d 2]", got, root, mid, end)
	}
	if got := mdChainRows(t, pool, solo); !mdHopsEqual(got, []mdHop{{solo, 0}}) {
		t.Errorf("단독 행 전개 = %v, want [%d 0]", got, solo)
	}

	if got := mdChainRows(t, pool, c1); !mdHopsEqual(got, []mdHop{{c1, 0}, {c2, 1}}) {
		t.Errorf("사이클 c1 전개 = %v, want [%d 0 · %d 1]", got, c1, c2)
	}
	if got := mdChainRows(t, pool, c2); !mdHopsEqual(got, []mdHop{{c2, 0}, {c1, 1}}) {
		t.Errorf("사이클 c2 전개 = %v, want [%d 0 · %d 1]", got, c2, c1)
	}

	metaVal := func(key string) string {
		var v string
		if err := pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, key).Scan(&v); err != nil {
			t.Fatalf("meta %s: %v", key, err)
		}
		return v
	}
	hash1 := metaVal("views_hash")
	if hash1 == "" {
		t.Fatal("views_hash 미기록")
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("두 번째 Migrate: %v", err)
	}
	if hash2 := metaVal("views_hash"); hash2 != hash1 {
		t.Errorf("재적용 멱등 위반: views_hash %q → %q", hash1, hash2)
	}

	if sig := metaVal("views_sig"); !strings.Contains(sig, "supersede_chains:root_id,id,depth") {
		t.Errorf("views_sig에 supersede_chains 시그니처 부재:\n%s", sig)
	}

	chains, err := SupersedeChains(ctx, &exportPoolStore{pool: pool})
	if err != nil {
		t.Fatalf("SupersedeChains(실DB): %v", err)
	}
	row := []ExportRow{{
		ID:           root,
		Topic:        "체인 데모",
		Status:       "invalidated",
		Source:       "agent",
		CreatedAt:    time.Date(2026, 11, 22, 1, 2, 3, 0, time.UTC),
		SupersededBy: &mid,
	}}
	out := string(FormatMD(row, chains))
	want := fmt.Sprintf("superseded_by: → #%d → #%d (depth 2)", mid, end)
	if !strings.Contains(out, want) {
		t.Errorf("실DB 체인 표기 %q 누락:\n%s", want, out)
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
