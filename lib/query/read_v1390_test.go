package query

import (
	"fmt"
	"testing"
)

func recallSignalRow(id int64, topic, summary string, readCount int32) []any {
	return []any{id, topic, summary, "", "", "", "decision", 0.9, "", "active", readCount}
}

func TestReadRecallSignalOrdersRows(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic note", 2),
	}
	SetRecallCounts(map[int]int{2: 3})
	ReorderByFallbackCount(rows, TokenizeQuery("alpha"))
	if got := whyRowID(rows[0]); got != "2" {
		t.Fatalf("recall 3건 행이 상위여야 함: 상위 id=%s", got)
	}
	if got := whyRowID(rows[1]); got != "1" {
		t.Fatalf("recall 0건 행이 하위여야 함: 하위 id=%s", got)
	}
}

func TestReadNoSignalKeepsOrder(t *testing.T) {
	rows := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	ReorderByFallbackCount(rows, TokenizeQuery("alpha policy"))
	if got := whyRowID(rows[0]); got != "1" {
		t.Fatalf("신호 부재 시 기존 순위 유지: 상위 id=%s", got)
	}

	defer SetRecallCounts(nil)
	SetRecallCounts(map[int]int{99: 5})
	rows2 := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	ReorderByFallbackCount(rows2, TokenizeQuery("alpha policy"))
	if got := whyRowID(rows2[0]); got != "1" {
		t.Fatalf("미등록 id 행은 기존 순위 유지: 상위 id=%s", got)
	}
}

func TestReadRecallSignalWhyEvidence(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha demo", "alpha synthetic note", 2),
	}
	tokens := TokenizeQuery("alpha")

	SetRecallCounts(map[int]int{1: 3})
	ev := RRFWhyEvidence(rows, tokens)
	if len(ev) != 1 || len(ev[0].RRF) != 5 {
		t.Fatalf("recall 신호 편입 시 RRF 신호 수 = %d, want 5", len(ev[0].RRF))
	}
	last := ev[0].RRF[len(ev[0].RRF)-1]
	if last.Name != "recall" || last.Value != 3 || last.Rank != 1 {
		t.Fatalf("recall 신호 evidence = %+v, want Name=recall Value=3 Rank=1", last)
	}

	SetRecallCounts(nil)
	ev = RRFWhyEvidence(rows, tokens)
	if len(ev) != 1 || len(ev[0].RRF) != 4 {
		t.Fatalf("신호 부재 시 RRF 신호 수 = %d, want 4", len(ev[0].RRF))
	}
	for _, s := range ev[0].RRF {
		if s.Name == "recall" {
			t.Fatalf("신호 부재 시 recall 신호 노출: %+v", s)
		}
	}
}

func TestReadRecallSignalPreservesExistingSignalOrder(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	SetRecallCounts(map[int]int{1: 3, 2: 3})
	ReorderByFallbackCount(rows, TokenizeQuery("alpha policy"))
	if got := fmt.Sprint(whyRowID(rows[0]), whyRowID(rows[1])); got != "12" {
		t.Fatalf("recall 동등 시 기존 순위 유지: 순서 = %s, want 12", got)
	}
}
