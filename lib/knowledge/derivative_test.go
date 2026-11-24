package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
)

const derivativeVaultSQLMark = "FROM vault_files"

func derivativeFake(vaultSummary string, hasRow bool) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, derivativeVaultSQLMark) {
			if !hasRow {
				return nil, nil
			}
			return [][]any{{vaultSummary}}, nil
		}
		return nil, nil
	}}
}

func derivativeJSONL(topic, summary string) string {
	return `{"topic":"` + topic + `","summary":"` + summary + `","confidence":0.8,"source_path":"notes/demo-note.md"}`
}

func TestDerivativeOfSourceDirect(t *testing.T) {
	ctx := context.Background()
	vaultSummary := "demo source body"
	runes := store.DefaultThresholds().VaultSummaryMaxRunes
	st := derivativeFake(vaultSummary, true)

	got, err := IsDerivativeOfSource(ctx, st, "demo-topic", vaultSummary, "notes/demo-note.md", runes)
	if err != nil || !got {
		t.Fatalf("요약 일치 → true, got=%v err=%v", got, err)
	}

	got, err = IsDerivativeOfSource(ctx, st, "demo-topic", "reworded body", "notes/demo-note.md", runes)
	if err != nil || got {
		t.Fatalf("요약 불일치 → false, got=%v err=%v", got, err)
	}

	got, err = IsDerivativeOfSource(ctx, st, "demo-topic", vaultSummary, "", runes)
	if err != nil || got {
		t.Fatalf("source_path 공백 → false, got=%v err=%v", got, err)
	}

	got, err = IsDerivativeOfSource(ctx, st, "demo-topic", "", "notes/demo-note.md", runes)
	if err != nil || got {
		t.Fatalf("빈 요약 → false, got=%v err=%v", got, err)
	}

	noRow := derivativeFake(vaultSummary, false)
	got, err = IsDerivativeOfSource(ctx, noRow, "demo-topic", vaultSummary, "notes/missing.md", runes)
	if err != nil || got {
		t.Fatalf("원본 없음 → false, got=%v err=%v", got, err)
	}
}

func TestDerivativeThreeBounds(t *testing.T) {
	body := "demo source body"

	match := derivativeFake(body, true)
	out, code := run(t, match, []string{"knowledge"}, derivativeJSONL("dv-match", body))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(match.inserted) != 1 {
		t.Fatalf("원본 일치 시 저장 1건: %d", len(match.inserted))
	}
	tags, _ := match.inserted[0]["tags"].(string)
	if !strings.Contains(tags, ":"+DerivativeTag+":") {
		t.Fatalf("원본 일치 시 파생 태그 누락: tags=%q", tags)
	}
	if origin, _ := match.inserted[0]["origin"].(string); origin != "agent" {
		t.Fatalf("origin 값 불변 위반: %q", origin)
	}
	if conf, _ := match.inserted[0]["confidence"].(float64); conf != 0.8 {
		t.Fatalf("confidence 불변 위반: %v", conf)
	}

	mismatch := derivativeFake("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", true)
	out, code = run(t, mismatch, []string{"knowledge"}, derivativeJSONL("dv-mismatch", body))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(mismatch.inserted) != 1 {
		t.Fatalf("불일치 시 일반 저장 1건: %d", len(mismatch.inserted))
	}
	tags, _ = mismatch.inserted[0]["tags"].(string)
	if strings.Contains(tags, DerivativeTag) {
		t.Fatalf("해시 불일치를 파생 오분류: tags=%q", tags)
	}

	absent := derivativeFake(body, false)
	out, code = run(t, absent, []string{"knowledge"}, derivativeJSONL("dv-absent", body))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(absent.inserted) != 1 {
		t.Fatalf("원본 없음 시 일반 저장 1건: %d", len(absent.inserted))
	}
	tags, _ = absent.inserted[0]["tags"].(string)
	if strings.Contains(tags, DerivativeTag) {
		t.Fatalf("원본 없는 신규 지식을 파생 오분류: tags=%q", tags)
	}
}

func TestDerivativeNoopBoundary(t *testing.T) {
	body := "demo source body"
	f := derivativeFake(body, true)
	f.exists = true

	out, code := run(t, f, []string{"knowledge"}, derivativeJSONL("dv-noop", body))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.TrimSpace(out) != `{"status":"duplicate"}` {
		t.Fatalf("content_hash 중복은 NOOP 우선: out=%q", out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("NOOP 경계에서 삽입 발생: %v", f.inserted)
	}
	for _, q := range f.queries {
		if strings.Contains(q, derivativeVaultSQLMark) {
			t.Fatalf("NOOP 경계에서 파생 판정 질의 실행됨: %q", q)
		}
	}
}

func TestDerivativeCost(t *testing.T) {
	body := "demo source body"
	f := derivativeFake("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", true)

	out, code := run(t, f, []string{"knowledge"}, derivativeJSONL("dv-cost", body))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("저장 1건: %d", len(f.inserted))
	}
	vaultQueries := 0
	for _, q := range f.queries {
		if !strings.Contains(q, derivativeVaultSQLMark) {
			continue
		}
		vaultQueries++
		if !strings.Contains(q, "path = $1") {
			t.Fatalf("전수 스캔성 vault 질의: %q", q)
		}
	}
	if vaultQueries != 1 {
		t.Fatalf("vault_files 질의 %d회, want 1(고유 path 조회)", vaultQueries)
	}
}

func TestDerivativeBatchedTagged(t *testing.T) {
	body := "demo source body"
	f := derivativeFake(body, true)
	jsonl := derivativeJSONL("dv-batch-a", body) + "\n" +
		`{"topic":"dv-batch-b","summary":"independent body","confidence":0.8,"source_path":"notes/demo-note.md"}`

	out, code := run(t, f, []string{"knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 2 {
		t.Fatalf("배치 저장 2건: %d", len(f.inserted))
	}
	tagged, untagged := 0, 0
	for _, rec := range f.inserted {
		tags, _ := rec["tags"].(string)
		if strings.Contains(tags, ":"+DerivativeTag+":") {
			tagged++
		} else {
			untagged++
		}
	}
	if tagged != 1 || untagged != 1 {
		t.Fatalf("배치 줄별 판정 태그=%d·일반=%d, want 1·1", tagged, untagged)
	}
}

func TestDerivativeRealShapeSummaryMatch(t *testing.T) {
	ctx := context.Background()
	runes := store.DefaultThresholds().VaultSummaryMaxRunes
	vaultSummary := "The demo source note opens with this paragraph."

	st := derivativeFake(vaultSummary, true)
	got, err := IsDerivativeOfSource(ctx, st, "demo-topic", vaultSummary, "notes/demo-note.md", runes)
	if err != nil || !got {
		t.Fatalf("본문 첫 문단 요약 일치 → true, got=%v err=%v", got, err)
	}

	f := derivativeFake(vaultSummary, true)
	out, code := run(t, f, []string{"knowledge"}, derivativeJSONL("dv-real-shape", vaultSummary))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("실물 형상 일치 시 저장 1건: %d", len(f.inserted))
	}
	tags, _ := f.inserted[0]["tags"].(string)
	if !strings.Contains(tags, ":"+DerivativeTag+":") {
		t.Fatalf("본문 요약 일치 시 파생 태그 누락: tags=%q", tags)
	}

	other := derivativeFake(vaultSummary, true)
	out, code = run(t, other, []string{"knowledge"},
		derivativeJSONL("dv-real-other", "A rewritten demo abstract unrelated to the note"))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if tags, _ := other.inserted[0]["tags"].(string); strings.Contains(tags, DerivativeTag) {
		t.Fatalf("다른 요약을 파생 오분류: tags=%q", tags)
	}
}

func TestDerivativeSummaryTruncationAligned(t *testing.T) {
	ctx := context.Background()
	runes := store.DefaultThresholds().VaultSummaryMaxRunes
	long := strings.Repeat("데모 원본 문장이다. ", 30)
	vaultSummary := render.TruncateRunes(long, runes)
	if len([]rune(vaultSummary)) != runes {
		t.Fatalf("픽스처 절단 검증: %d runes, want %d", len([]rune(vaultSummary)), runes)
	}

	st := derivativeFake(vaultSummary, true)
	got, err := IsDerivativeOfSource(ctx, st, "demo-topic", long, "notes/demo-note.md", runes)
	if err != nil || !got {
		t.Fatalf("절단 길이 정렬 시 전문 요약 일치 → true, got=%v err=%v", got, err)
	}

	got, err = IsDerivativeOfSource(ctx, st, "demo-topic", "completely unrelated demo content", "notes/demo-note.md", runes)
	if err != nil || got {
		t.Fatalf("무관한 내용 → false, got=%v err=%v", got, err)
	}
}

func TestDerivativeTagAppendNoCollision(t *testing.T) {
	cases := []struct{ tags, want string }{
		{":derivative:source:", ":derivative:source:"},
		{":derivative:playbook:source:", ":derivative:playbook:source:"},
		{":source:playbook:", ":derivative:playbook:source:"},
		{":derivative:playbook:", ":derivative:playbook:source:"},
		{":playbook:", ":derivative:playbook:source:"},
		{"", ":derivative:source:"},
		{":source:", ":derivative:source:"},
	}
	for _, c := range cases {
		if got := appendNormalizedTag(c.tags, DerivativeTag); got != c.want {
			t.Fatalf("appendNormalizedTag(%q, %q) = %q, want %q", c.tags, DerivativeTag, got, c.want)
		}
	}
}
