package query

import (
	"regexp"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/sqlfs"
)

func TestWhyProvenanceFields(t *testing.T) {
	base := []WhyEvidence{{ID: "k1", Tokens: "topic_prefix×2+cover×1", TokenScore: 3}}
	if got, want := FormatWhyRows(base, false), "why|id=k1|topic_prefix×2+cover×1|score=3\n"; got != want {
		t.Fatalf("미세팅 텍스트 하위 호환 = %q, want %q", got, want)
	}

	rrfBase := []WhyEvidence{{
		ID:       "k1",
		RRF:      []RRFSignalEvidence{{Name: "match", Value: 2, Rank: 1, Contrib: 0.5}},
		RRFScore: 0.5,
	}}
	rrfWant := "why|id=k1|match=2/r1/0.5|score=0.5\n"
	if got := FormatWhyRows(rrfBase, false); got != rrfWant {
		t.Fatalf("미세팅 rrf 하위 호환 = %q, want %q", got, rrfWant)
	}

	ev := []WhyEvidence{{ID: "k1", Source: "manual", Confidence: 0.9, Tokens: "none", TokenScore: 0}}
	got := FormatWhyRows(ev, false)
	if want := "why|id=k1|none|score=0|source=manual|confidence=0.9\n"; got != want {
		t.Errorf("provenance 표기 = %q, want %q", got, want)
	}

	ev[0].Confidence = 0
	got = FormatWhyRows(ev, false)
	if want := "why|id=k1|none|score=0|source=manual\n"; got != want {
		t.Errorf("confidence 0 omitempty = %q, want %q", got, want)
	}

	rrfProv := []WhyEvidence{{
		ID:         "k1",
		Source:     "human",
		Confidence: 1,
		RRF:        []RRFSignalEvidence{{Name: "match", Value: 2, Rank: 1, Contrib: 0.5}},
		RRFScore:   0.5,
	}}
	got = FormatWhyRows(rrfProv, false)
	if want := "why|id=k1|match=2/r1/0.5|score=0.5|source=human|confidence=1\n"; got != want {
		t.Errorf("rrf provenance 표기 = %q, want %q", got, want)
	}

	if strings.Contains(FormatWhyRows([]WhyEvidence{{ID: "k1", Source: "a|b", Confidence: 0.9}}, false), "a|b") {
		t.Error("source에 파이프 문자 살아있음 — noPipe 누락")
	}
}

func TestHotViewColumns(t *testing.T) {
	sql := sqlfs.ViewsSQL()
	re := regexp.MustCompile(`(?s)CREATE OR REPLACE VIEW hot_view AS\s*(.*);`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		t.Fatal("views.sql에 hot_view 정의 없음")
	}
	block := m[1]
	arms := strings.Split(block, "UNION ALL")
	if len(arms) != 3 {
		t.Fatalf("hot_view 팔 수 = %d, want 3", len(arms))
	}
	wantPrefix := [][]string{
		{"'project' AS kind", "p.name AS topic", "p.status AS summary", "p.updated AS ts",
			"NULL::int AS id", "NULL::timestamptz AS valid_at", "NULL::timestamptz AS ingested_at"},
		{"'pending' AS kind", "k.topic", "k.summary", "k.ingested_at", "k.id", "k.valid_at", "k.ingested_at"},
		{"'decision' AS kind", "k.topic", "k.summary", "k.valid_at", "k.id", "k.valid_at", "k.ingested_at"},
	}
	wantTail := []string{"NULL::text AS source, NULL::float8 AS confidence", "k.origin AS source, k.confidence AS confidence", "k.origin AS source, k.confidence AS confidence"}
	for i, arm := range arms {
		selectList := arm[:strings.Index(arm, "\nFROM")]
		pos := 0
		for _, col := range wantPrefix[i] {
			idx := strings.Index(selectList[pos:], col)
			if idx < 0 {
				t.Errorf("팔 %d: 기존 컬럼 %q 누락 또는 순서 어긋남:\n%s", i, col, selectList)
				break
			}
			pos += idx + len(col)
		}
		if !strings.HasSuffix(strings.TrimSpace(selectList), wantTail[i]) {
			t.Errorf("팔 %d: provenance 컬럼이 select 말미에 없음:\n%s", i, selectList)
		}
	}
	if idx := strings.LastIndex(sql, "CREATE OR REPLACE VIEW"); strings.Contains(sql[idx:], "hot_view") {
		last := sql[idx:]
		if !strings.HasPrefix(last, "CREATE OR REPLACE VIEW hot_view") {
			t.Errorf("hot_view가 마지막 뷰 정의가 아님 — 단일 정의 게이트 위반:\n%s", last[:60])
		}
	}
}

func TestWhyJSONMode(t *testing.T) {
	got := FormatWhyRows([]WhyEvidence{{ID: "1", Tokens: "t", TokenScore: 1}}, true)
	want := `{"why":{"id":"1","tokens":"t","token_score":1}}` + "\n"
	if got != want {
		t.Errorf("json 미세팅 하위 호환 = %q, want %q", got, want)
	}
	got = FormatWhyRows([]WhyEvidence{{ID: "1", Source: "manual", Confidence: 0.9, Tokens: "t", TokenScore: 1}}, true)
	want = `{"why":{"id":"1","tokens":"t","token_score":1,"source":"manual","confidence":0.9}}` + "\n"
	if got != want {
		t.Errorf("json provenance = %q, want %q", got, want)
	}
	got = FormatWhyRows([]WhyEvidence{{ID: "1", Confidence: 0}}, true)
	want = `{"why":{"id":"1"}}` + "\n"
	if got != want {
		t.Errorf("json confidence 0 omitempty = %q, want %q", got, want)
	}
}
