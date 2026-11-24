package doclint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const echoContractDoc = "docs/contracts/echo.md"

var echoFlowMarkers = []string{
	"if r.seen[h] {",
	"existsMeta, exists, err := lookupHash(r.ctx, r.st, h)",
	"scanEcho(r.ctx, r.st, n, r.th)",
	"store.Classify(r.kind, conf, r.floor)",
	"func (r *writeState) emitDuplicate()",
	"InsertBatch(r.ctx, r.recs)",
}

func sourceFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", rel, err)
	}
	return string(b)
}

func constValue(t *testing.T, src, pattern, name string) float64 {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s 기본값을 소스에서 찾지 못함: %s", name, pattern)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("%s 기본값 파싱 실패: %v", name, err)
	}
	return v
}

func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func TestEchoContractFrozen(t *testing.T) {
	doc := docStr(t, echoContractDoc)
	writeSrc := sourceFile(t, "lib/knowledge/write.go")

	pos := 0
	for _, m := range echoFlowMarkers {
		needle := "`" + m + "`"
		i := strings.Index(doc[pos:], needle)
		if i < 0 {
			t.Fatalf("계약 문서에 흐름 단계 마커 없음(순서 포함): %s", m)
		}
		pos += i
	}

	prev := -1
	for _, m := range echoFlowMarkers {
		i := strings.Index(writeSrc, m)
		if i < 0 {
			t.Fatalf("write.go에서 계약 마커 소실 — 문서 또는 코드 동기화 필요: %s", m)
		}
		if i <= prev {
			t.Fatalf("write.go 흐름 순서가 계약 문서와 불일치(드리프트): %q 가 %d 위치에서 순서 역전", m, i)
		}
		prev = i
	}

	for _, want := range []string{
		"NOOP > 삭제 > 갱신 > 추가",
		"NOOP=emitDuplicate",
		"삭제=maintain/invalidate",
		"갱신=approve/pending",
		"추가=Classify 통과",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("계약 문서에 위계 위임 어휘 누락: %s", want)
		}
	}

	echoSrc := sourceFile(t, "lib/knowledge/echo.go")
	thSrc := sourceFile(t, "lib/store/thresholds.go")

	dupTh := constValue(t, thSrc, `{"ConflictDupThreshold", "conflict_dup_threshold", ([0-9.]+),`, "ConflictDupThreshold")
	scanCap := constValue(t, thSrc, `{"SuggestScanCap", "suggest_scan_cap", ([0-9]+),`, "SuggestScanCap")
	minScore := constValue(t, echoSrc, `echoDefaultMinScore\s*=\s*([0-9.]+)`, "EchoMinScore")
	topicMatch := constValue(t, echoSrc, `echoDefaultTopicMatch\s*=\s*([0-9.]+)`, "EchoTopicMatch")
	topK := constValue(t, echoSrc, `echoDefaultTopK\s*=\s*([0-9]+)`, "EchoTopK")
	maxRunes := constValue(t, echoSrc, `echoInputMaxRunes\s*=\s*([0-9]+)`, "echoInputMaxRunes")

	tableIdx := strings.Index(doc, "경계 케이스")
	if tableIdx < 0 {
		t.Fatal("계약 문서에 경계 케이스 표 없음")
	}
	table := doc[tableIdx:]

	for _, pair := range []struct {
		name  string
		value float64
	}{
		{"ConflictDupThreshold", dupTh},
		{"SuggestScanCap", scanCap},
		{"EchoMinScore", minScore},
		{"EchoTopicMatch", topicMatch},
		{"EchoTopK", topK},
		{"echoInputMaxRunes", maxRunes},
	} {
		if !strings.Contains(table, trimFloat(pair.value)) {
			t.Fatalf("경계 케이스 표의 %s 기본값 %s 가 실물 소스와 불일치", pair.name, trimFloat(pair.value))
		}
	}

	for _, rel := range []string{"duplicates", "contradicts"} {
		if !strings.Contains(table, `"`+rel+`"`) {
			t.Fatalf("경계 케이스 표에 reason(relation) 값 %q 누락", rel)
		}
	}
}
