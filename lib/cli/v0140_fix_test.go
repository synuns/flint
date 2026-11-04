// v0140_fix_test.go — v0.14.0 리뷰 수정 회귀(cli): P2 getKV 전송 오류 시
// read_stats 와이프 방지, P3 eligible 헬퍼 단일 소스, P4 metaKV 공유 헬퍼.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestBumpStatsGetKVErrorKeepsStats — getKV 전송 오류(err != nil) 시
// setKV 미호출·read_stats 미변경(기존 calls/zero 와이프 방지). 키 부재·JSON
// 파손은 기존 계약("0에서 재기작")대로 setKV를 호출한다. v0.15.0 룰링으로
// BumpZeroNear 단얱은 제거(BumpReadStats 통합 대상 — 승계는
// TestBumpReadStatsUnified).
func TestBumpStatsGetKVErrorKeepsStats(t *testing.T) {
	boom := errors.New("kv transport down")
	get := func(string) (string, error) { return "", boom }
	sets := 0
	set := func(k, v string) error { sets++; return nil }
	if err := BumpReadStats(true, false, get, set); !errors.Is(err, boom) {
		t.Errorf("BumpReadStats getKV 오류 미반환: %v", err)
	}
	if sets != 0 {
		t.Errorf("setKV %d회 호출, want 0(전송 오류 시 기존 stats 보존)", sets)
	}
}

// TestReadSuggestEligible — eligible 판정 단일 소스(P3): 현재 시점·
// 무상태필터·0행일 때만 참(폴백 게이트·제안 스캔 게이트·침묵 exit 공유).
func TestReadSuggestEligible(t *testing.T) {
	cases := []struct {
		rows   int
		asOf   string
		status string
		want   bool
	}{
		{0, "", "", true},              // eligible 제로히트
		{1, "", "", false},             // 히트
		{3, "", "", false},             // 히트(복수)
		{0, "abc123", "", false},       // --as-of(과거 시점)
		{0, "", "active", false},       // --status(상태 필터)
		{0, "abc123", "active", false}, // 둘 다
	}
	for _, c := range cases {
		if got := readSuggestEligible(c.rows, c.asOf, c.status); got != c.want {
			t.Errorf("readSuggestEligible(%d,%q,%q)=%v, want %v",
				c.rows, c.asOf, c.status, got, c.want)
		}
	}
}

// TestMetaKVRoundtrip — metaKV가 Store 위에 getKV/setKV 클로저를 올바르게
// 조립한다(P4): 부재 ("",nil)·set→get 왕복·전송 오류 전파.
func TestMetaKVRoundtrip(t *testing.T) {
	meta := map[string]string{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "INSERT INTO meta"):
			meta[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
			return nil, nil
		case len(args) == 1:
			if v, ok := meta[fmt.Sprint(args[0])]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil // 키 부재
		}
		return nil, nil
	}
	getKV, setKV := metaKV(context.Background(), f)
	if v, err := getKV("read_stats"); err != nil || v != "" {
		t.Fatalf("부재 getKV=(%q,%v), want (\"\",nil)", v, err)
	}
	if err := setKV("read_stats", `{"calls":1}`); err != nil {
		t.Fatalf("setKV 오류: %v", err)
	}
	if v, err := getKV("read_stats"); err != nil || v != `{"calls":1}` {
		t.Fatalf("왕복 getKV=(%q,%v), want 저장값 원문", v, err)
	}
	// 전송 오류 전파 — getKV·setKV 모두 query 오류를 그대로 반환한다.
	g := &fakeStore{queryErr: errors.New("db down")}
	get2, set2 := metaKV(context.Background(), g)
	if _, err := get2("k"); err == nil {
		t.Error("getKV 전송 오류 미전파")
	}
	if err := set2("k", "v"); err == nil {
		t.Error("setKV 전송 오류 미전파")
	}
}
