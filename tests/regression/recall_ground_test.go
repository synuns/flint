package regression

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/internal/testengine"
)

const groundTopicPrefix = "reg-ground-"

const groundOpsGuardDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

func groundOpsGuardMatch(dsn string) bool {
	ops, err := pgx.ParseConfig(groundOpsGuardDSN)
	if err != nil {
		return false
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return false
	}
	host := func(h string) string {
		if h == "localhost" {
			return "127.0.0.1"
		}
		return h
	}
	return host(cfg.Host) == host(ops.Host) && cfg.Port == ops.Port && cfg.Database == ops.Database
}

type groundSeedRow struct {
	name       string
	tags       string
	entity     string
	domain     string
	confidence float64
	summary    string
}

func groundSeedRows() []groundSeedRow {
	return []groundSeedRow{
		{"alpha", ":demo-alpha:", "demo-entity-1", "main", 0.9, "ground alpha baseline demo note"},
		{"bravo", ":demo-beta:", "demo-entity-1", "main", 0.85, "bravo-key ground summary token note"},
		{"charlie", ":groundtag-a:", "demo-entity-2", "demo-a", 0.8, "ground charlie tag coverage note"},
		{"delta", ":demo-delta:", "demo-entity-2", "demo-b", 0.9, "ground delta domain isolation note"},
		{"echo", ":demo-echo:", "demo-entity-3", "main", 0.9, "ground echo entity filter note"},
		{"foxtrot", ":groundtag-b:", "demo-entity-3", "main", 0.9, "ground foxtrot combined filter note"},
		{"golf", ":demo-golf:", "demo-entity-1", "demo-a", 0.7, "ground golf low confidence note"},
		{"hotel", ":demo-hotel:", "demo-entity-2", "demo-b", 0.75, "ground hotel second demo-b row note"},
		{"india", ":demo-india:", "demo-entity-3", "demo-a", 0.65, "ground india confidence below note"},
		{"juliet", ":groundtag-high:", "demo-entity-1", "main", 0.99, "ground juliet high confidence note"},
		{"tango", ":groundtag-high:", "demo-entity-4", "main", 0.6, "ground tango low confidence tagged note"},
		{"kilo", ":demo-kilo:", "demo-entity-2", "demo-a", 0.9, "ground kilo domain keyword note"},
		{"lima", ":demo-lima:", "demo-entity-3", "main", 0.5, "ground lima low floor note"},
	}
}

type groundQueryCase struct {
	name      string
	args      []string
	wantNames []string
	keyField  string
	keyValue  string
	coverage  string
}

func groundQueryCases() []groundQueryCase {
	rows := groundSeedRows()
	byName := func(n string) groundSeedRow {
		for _, r := range rows {
			if r.name == n {
				return r
			}
		}
		return groundSeedRow{}
	}
	sum := func(n string) string { return byName(n).summary }
	return []groundQueryCase{
		{
			name:      "topic-keyword",
			args:      []string{"read", "--json", "--fields", "topic,summary", groundTopicPrefix + "alpha"},
			wantNames: []string{"alpha"},
			keyField:  "summary", keyValue: sum("alpha"),
			coverage: "topic keyword",
		},
		{
			name:      "summary-keyword",
			args:      []string{"read", "--json", "--fields", "topic,summary", "bravo-key"},
			wantNames: []string{"bravo"},
			keyField:  "summary", keyValue: sum("bravo"),
			coverage: "summary keyword",
		},
		{
			name:      "tag-only",
			args:      []string{"read", "--json", "--fields", "topic", "--tag", "groundtag-a"},
			wantNames: []string{"charlie"},
			coverage:  "tag filter",
		},
		{
			name:      "domain-only",
			args:      []string{"read", "--json", "--fields", "topic", "--domain", "demo-b"},
			wantNames: []string{"delta", "hotel"},
			coverage:  "domain filter",
		},
		{
			name:      "entity-only",
			args:      []string{"read", "--json", "--fields", "topic", "--entity", "demo-entity-3"},
			wantNames: []string{"echo", "foxtrot", "india", "lima"},
			coverage:  "entity filter",
		},
		{
			name:      "keyword+tag",
			args:      []string{"read", "--json", "--fields", "topic", groundTopicPrefix + "foxtrot", "--tag", "groundtag-b"},
			wantNames: []string{"foxtrot"},
			coverage:  "tag+keyword",
		},
		{
			name:      "confidence+tag",
			args:      []string{"read", "--json", "--fields", "topic", "--tag", "groundtag-high", "--min-confidence", "0.95"},
			wantNames: []string{"juliet"},
			coverage:  "min-confidence+tag",
		},
		{
			name:      "keyword+domain",
			args:      []string{"read", "--json", "--fields", "topic", groundTopicPrefix + "kilo", "--domain", "demo-a"},
			wantNames: []string{"kilo"},
			coverage:  "domain+keyword",
		},
	}
}

func groundSeed(t *testing.T) func() {
	t.Helper()
	pool := regDrillDB(t)
	ctx := context.Background()
	cleanup := func() {
		_, err := pool.Exec(ctx, `DELETE FROM knowledge WHERE topic LIKE '`+groundTopicPrefix+`%'`)
		if err != nil {
			t.Fatalf("ground 시드 청소: %v", err)
		}
	}
	cleanup()
	for _, r := range groundSeedRows() {
		topic := groundTopicPrefix + r.name
		h := sha256.Sum256([]byte(topic))
		if _, err := pool.Exec(ctx, `INSERT INTO knowledge
			(topic, summary, tags, entity, domain, confidence, content_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			topic, r.summary, r.tags, r.entity, r.domain, r.confidence, hex.EncodeToString(h[:])); err != nil {
			t.Fatalf("ground 시드 %s: %v", r.name, err)
		}
	}
	return cleanup
}

func parseGroundRows(t *testing.T, out string) []map[string]string {
	t.Helper()
	rows := make([]map[string]string, 0, 4)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"topic"`) {
			continue
		}
		m := make(map[string]string, 4)
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("read JSON 행 파싱 실패(%q): %v", line, err)
		}
		rows = append(rows, m)
	}
	return rows
}

func judgeGroundCase(t *testing.T, qc groundQueryCase) bool {
	t.Helper()
	out, code := regDrillRun(qc.args...)
	if code != 0 {
		t.Errorf("[%s] exit=%d, want 0; out=%s", qc.name, code, out)
		return false
	}
	rows := parseGroundRows(t, out)
	gotTopics := make(map[string]bool, len(rows))
	for _, r := range rows {
		topic := r["topic"]
		if !strings.HasPrefix(topic, groundTopicPrefix) {
			t.Errorf("[%s] 정답 외 비지반 행 도달: %q — 정확 일치 판정 위반", qc.name, topic)
			return false
		}
		gotTopics[strings.TrimPrefix(topic, groundTopicPrefix)] = true
	}
	want := make(map[string]bool, len(qc.wantNames))
	for _, n := range qc.wantNames {
		want[n] = true
	}
	for n := range gotTopics {
		if !want[n] {
			t.Errorf("[%s] 정답 외 행 도달: %s (want %v)", qc.name, n, qc.wantNames)
			return false
		}
	}
	for _, n := range qc.wantNames {
		if !gotTopics[n] {
			t.Errorf("[%s] 정답 행 미도달: %s (got %v)", qc.name, n, gotTopics)
			return false
		}
	}
	if qc.keyField != "" {
		name := qc.wantNames[0]
		topic := groundTopicPrefix + name
		found := false
		for _, r := range rows {
			if r["topic"] == topic {
				if r[qc.keyField] != qc.keyValue {
					t.Errorf("[%s] 키 필드 불일치(%s): got %q want %q",
						qc.name, qc.keyField, r[qc.keyField], qc.keyValue)
					return false
				}
				found = true
			}
		}
		if !found {
			t.Errorf("[%s] 키 필드 검증 행 없음: %s", qc.name, topic)
			return false
		}
	}
	return true
}

func TestGroundGuardHostPort(t *testing.T) {
	variants := []string{
		"postgresql://flint:flint-dev@127.0.0.1:5434/flint",
		"postgresql://flint:flint-dev@localhost:5434/flint?sslmode=disable",
	}
	for _, v := range variants {
		if v == groundOpsGuardDSN {
			t.Fatal("변형 DSN이 원본과 문자열 동일 — 변형 선정 오류(구 가드의 문자열 동등 맹점 소실)")
		}
		if !groundOpsGuardMatch(v) {
			t.Errorf("운영 DSN 변형이 가드 통과 — seed/cleanup SQL이 운영 엔진 도달 위험: %s", v)
		}
	}
	for _, safe := range []string{
		testengine.TestDSNDefault(),
		"postgresql://flint:flint-dev@localhost:5434/flint_demo",
	} {
		if groundOpsGuardMatch(safe) {
			t.Errorf("비운영 DSN을 운영으로 오판: %s", safe)
		}
	}
}

func TestRecallGround(t *testing.T) {
	if dsn := regTestDSN(); groundOpsGuardMatch(dsn) {
		t.Fatalf("ground DSN이 운영 DSN(host:port·db 일치)과 충돌 — 엔진 격리 위반: %s", dsn)
	}
	cleanup := groundSeed(t)
	defer cleanup()

	passed := 0
	cases := groundQueryCases()
	verdicts := make([]string, len(cases))
	for i, qc := range cases {
		if judgeGroundCase(t, qc) {
			passed++
			verdicts[i] = "PASS"
		} else {
			verdicts[i] = "MISS"
		}
	}
	t.Logf("%-18s %-22s %s", "query", "coverage", "verdict")
	for i, qc := range cases {
		t.Logf("%-18s %-22s %s", qc.name, qc.coverage, verdicts[i])
	}
	if passed != len(cases) {
		t.Fatalf("recall_ground_pass=%d recall_ground_total=%d — 정확 일치 도달 미달", passed, len(cases))
	}
}

func TestRecallGroundIsolated(t *testing.T) {
	t.Setenv("FLINT_DSN", groundOpsGuardDSN)
	t.Setenv("FLINT_TEST_DSN", "")
	if got := regTestDSN(); got != testengine.TestDSNDefault() {
		t.Errorf("ground DSN = %q, want 전용 엔진 %q (운영 FLINT_DSN 무시)", got, testengine.TestDSNDefault())
	}
	if got := regTestDSN(); got == groundOpsGuardDSN {
		t.Error("ground DSN이 운영 DSN(5434)에 접촉")
	}

	override := "postgresql://flint:t@127.0.0.1:5499/flint_ci"
	t.Setenv("FLINT_TEST_DSN", override)
	if got := regTestDSN(); got != override {
		t.Errorf("ground DSN = %q, want FLINT_TEST_DSN 우선 %q", got, override)
	}
	if got := regTestDSN(); got == groundOpsGuardDSN {
		t.Error("override 경유에서도 ground DSN이 운영 DSN에 접촉")
	}
}
