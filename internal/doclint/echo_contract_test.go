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
	"r.echoCands = scanEcho(r.ctx, r.st, n, r.th)",
	"store.Classify(r.kind, conf, r.floor)",
	"func (r *writeState) emitDuplicate()",
	"InsertBatch(r.ctx, r.recs)",
}

func boundaryCell(table, name string) (string, bool) {
	for _, line := range strings.Split(table, "\n") {
		if !strings.Contains(line, name) {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			return "", false
		}
		return strings.TrimSpace(cells[3]), true
	}
	return "", false
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
		cell, ok := boundaryCell(table, pair.name)
		if !ok {
			t.Fatalf("경계 케이스 표에서 %s 행을 찾지 못함", pair.name)
		}
		if cell != trimFloat(pair.value) {
			t.Fatalf("경계 케이스 표의 %s 기본값 %q 가 실물 소스 %s 와 불일치(정확 비교)", pair.name, cell, trimFloat(pair.value))
		}
	}

	for _, rel := range []string{"duplicates", "contradicts"} {
		if !strings.Contains(table, `"`+rel+`"`) {
			t.Fatalf("경계 케이스 표에 reason(relation) 값 %q 누락", rel)
		}
	}
}

func TestEchoContractNewRecordRegion(t *testing.T) {
	doc := docStr(t, echoContractDoc)
	writeSrc := sourceFile(t, "lib/knowledge/write.go")

	dupScan := strings.Index(writeSrc, "scanEcho(r.ctx, r.st, n, r.th)")
	newScan := strings.Index(writeSrc, "r.echoCands = scanEcho(r.ctx, r.st, n, r.th)")
	classify := strings.Index(writeSrc, "store.Classify(r.kind, conf, r.floor)")
	if newScan < 0 {
		t.Fatal("write.go에서 신규 분기 echo 스캔 마커 소실")
	}
	if classify < 0 {
		t.Fatal("write.go에서 Classify 마커 소실")
	}
	if newScan <= dupScan {
		t.Fatalf("신규 분기 스캔 마커가 중복 분기 스캔보다 앞섬: new=%d dup=%d", newScan, dupScan)
	}
	if newScan >= classify {
		t.Fatalf("신규 분기에서 scanEcho·Classify 순서 역전: scan=%d classify=%d", newScan, classify)
	}

	docScan := strings.Index(doc, "`scanEcho(r.ctx, r.st, n, r.th)`")
	docNew := strings.Index(doc, "`r.echoCands = scanEcho(r.ctx, r.st, n, r.th)`")
	docClassify := strings.Index(doc, "`store.Classify(r.kind, conf, r.floor)`")
	if docNew < 0 {
		t.Fatal("계약 문서에 신규 분기 영역 마커 없음 — 계약 개정 필요")
	}
	if docNew <= docScan || docNew >= docClassify {
		t.Fatalf("계약 문서 신규 분기 마커 순서 불일치: scan=%d new=%d classify=%d", docScan, docNew, docClassify)
	}
}

func TestEchoBoundaryCellExact(t *testing.T) {
	synth := strings.Join([]string{
		"| 항목 | 판정 조건 | 기본값 | 근거 소스 |",
		"|---|---|---|---|",
		"| duplicates 임계 | multiset bigram dice ≥ ConflictDupThreshold | 0.6 | lib/store/thresholds.go |",
		"| contradicts 임계 | BigramDice(전체) ≥ EchoMinScore | 0.35 | lib/knowledge/echo.go |",
		"| 보고 상한 | EchoTopK | 5 | lib/knowledge/echo.go |",
		"| reason(relation) 필드 | 2분기 고정 | — | lib/knowledge/echo.go |",
	}, "\n")

	if cell, ok := boundaryCell(synth, "ConflictDupThreshold"); !ok || cell != "0.6" {
		t.Fatalf("ConflictDupThreshold 셀 추출: cell=%q ok=%v", cell, ok)
	}
	if cell, ok := boundaryCell(synth, "EchoMinScore"); !ok || cell != "0.35" {
		t.Fatalf("EchoMinScore 셀 추출: cell=%q ok=%v", cell, ok)
	}
	if cell, ok := boundaryCell(synth, "EchoTopK"); !ok || cell != "5" {
		t.Fatalf("EchoTopK 셀 추출: cell=%q ok=%v", cell, ok)
	}
	if _, ok := boundaryCell(synth, "NoSuchMarker"); ok {
		t.Fatal("미등재 마커가 매칭됨")
	}
	if cell, _ := boundaryCell(synth, "EchoTopK"); cell == trimFloat(6) {
		t.Fatalf("substring 오탐 재발: EchoTopK 셀 %q 가 6 과 일치 판정", cell)
	}
	if cell, _ := boundaryCell(synth, "EchoMinScore"); cell == trimFloat(0.3) {
		t.Fatalf("substring 오탐 재발: EchoMinScore 셀 %q 가 0.3 과 일치 판정", cell)
	}
}
