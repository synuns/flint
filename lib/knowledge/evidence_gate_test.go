package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceGateRequired(t *testing.T) {
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"eg-rule","summary":"store the demo rule","confidence":0.9,"tags":["playbook"]}`)
	if code != 3 {
		t.Fatalf("규칙형 미동봉 exit=%d, want 3; out=%s", code, out)
	}
	if fakeInserts(out) != 0 {
		t.Fatalf("미동봉 규칙형 저장됨")
	}

	withOrigin := `{"topic":"eg-rule2","summary":"store the demo rule","confidence":0.9,` +
		`"tags":["playbook"],"origin":"agent","status":"verified"}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, withOrigin)
	if code != 0 {
		t.Fatalf("origin·status 동봉 exit=%d, want 0; out=%s", code, out)
	}
	if fakeInserts(out) != 1 {
		t.Fatalf("동봉 규칙형 저장 1건 실패: out=%s", out)
	}

	withSourcePath := `{"topic":"eg-rule3","summary":"store the demo rule","confidence":0.8,` +
		`"tags":["playbook"],"source_path":"notes/demo-note.md","status":"verified"}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, withSourcePath)
	if code != 0 {
		t.Fatalf("source_path·status 동봉 exit=%d, want 0; out=%s", code, out)
	}

	patternOnly := `{"topic":"eg-rule4","summary":"Always run the demo check first","confidence":0.9}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, patternOnly)
	if code != 3 {
		t.Fatalf("문형 규칙형 미동봉 exit=%d, want 3; out=%s", code, out)
	}

	isRule, sig := IsRuleTypeKnowledge(map[string]any{
		"tags": []any{"vol:conditional"}, "summary": "plain summary"})
	if !isRule || sig != "tag:vol:conditional" {
		t.Fatalf("등록표 태그 판정: rule=%v sig=%q", isRule, sig)
	}
	isRule, sig = IsRuleTypeKnowledge(map[string]any{
		"tags": []any{"adr"}, "summary": "when the demo flag flips, rerun"})
	if !isRule || sig != "pattern:when " {
		t.Fatalf("등록표 문형 판정: rule=%v sig=%q", isRule, sig)
	}
	isRule, _ = IsRuleTypeKnowledge(map[string]any{
		"tags": []any{"adr"}, "summary": "plain factual demo summary"})
	if isRule {
		t.Fatal("사실형을 규칙형 판정")
	}
}

func TestEvidenceGateFactExempt(t *testing.T) {
	fact := `{"topic":"eg-fact","summary":"demo factual observation body","confidence":0.9}`
	out, code := run(t, &fakeStore{}, []string{"knowledge"}, fact)
	if code != 0 {
		t.Fatalf("사실형 면제 exit=%d, want 0; out=%s", code, out)
	}
	if fakeInserts(out) != 1 {
		t.Fatalf("사실형 저장 1건 실패: out=%s", out)
	}

	conditional := `{"topic":"eg-cond","summary":"demo conditional observation","confidence":0.9,` +
		`"tags":["vol:conditional"],"origin":"agent","status":"verified"}`
	out, code = run(t, &fakeStore{}, []string{"knowledge"}, conditional)
	if code != 0 {
		t.Fatalf("규칙형 동봉 저장 exit=%d, want 0; out=%s", code, out)
	}
}

func TestEvidenceGateStructuredOutput(t *testing.T) {
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"eg-struct","summary":"store the demo rule","confidence":0.9,"tags":["playbook"]}`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("출력 %d줄, want 5줄 계약: %q", len(lines), out)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("1행이 JSON 아님: %q: %v", lines[0], err)
	}
	if first["error"] != "schema" || first["field"] != "evidence" {
		t.Fatalf("1행 구조화 오류 계약 불일치: %v", first)
	}
	wantPrefixes := []string{"file: stdin:1", "signal: ", "missing: ", "fix: "}
	for i, pre := range wantPrefixes {
		if !strings.HasPrefix(lines[i+1], pre) {
			t.Fatalf("%d행 접두 %q 불일치: %q", i+2, pre, lines[i+1])
		}
	}
	if !strings.Contains(lines[3], "status") || !strings.Contains(lines[3], "origin") {
		t.Fatalf("missing 행에 누락 필드 미표기: %q", lines[3])
	}
}

func TestEvidenceGateSentenceLevelDetection(t *testing.T) {
	isRule, sig := IsRuleTypeKnowledge(map[string]any{
		"summary": "데모 배경 설명이다. If the demo gate opens, run the check"})
	if !isRule || sig != "pattern:if " {
		t.Fatalf("2번째 문장 문형 판정: rule=%v sig=%q", isRule, sig)
	}

	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"eg-sent","summary":"데모 배경 설명이다. If the demo gate opens, run the check","confidence":0.9}`)
	if code != 3 {
		t.Fatalf("2번째 문장 규칙형 미동봉 exit=%d, want 3; out=%s", code, out)
	}
	if fakeInserts(out) != 0 {
		t.Fatalf("미동봉 규칙형 저장됨")
	}

	isRule, sig = IsRuleTypeKnowledge(map[string]any{
		"summary": "데모 안내. Never run the demo check twice"})
	if !isRule || sig != "pattern:never " {
		t.Fatalf("NBSP 경계 문장 판정: rule=%v sig=%q", isRule, sig)
	}

	isRule, _ = IsRuleTypeKnowledge(map[string]any{
		"summary": "demo factual body. more factual text follows"})
	if isRule {
		t.Fatal("사실형 복문장을 규칙형 판정")
	}
}

func fakeInserts(out string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m["status"] == "committed" {
			n++
		}
	}
	return n
}
