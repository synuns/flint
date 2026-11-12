// v0140_fix_test.go — v0.14.0 리뷰 수정 회귀(cli): P3 eligible 헬퍼 단일
// 소스, P4 metaKV 공유 헬퍼. (P2 BumpReadStats 와이프 방지는 v0.42.0 T2
// read_stats blob 폐지로 함께 폐지 — 파생 경로 동등성은 lib/ops/
// report_parity_test.go가 담당한다.)
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

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
