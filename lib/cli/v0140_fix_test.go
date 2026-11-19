// v0140_fix_test.go — cli regressions: the single-source eligible
// helper and the shared metaKV helper. (The BumpReadStats wipe guard was
// abolished along with the read_stats blob — derived-path equivalence is covered by
// lib/ops/report_parity_test.go.)
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestReadSuggestEligible — the eligible determination is a single source (P3): true only for the current time,
// no stateful filters, and 0 rows (sharing the fallback gate, suggestion-scan gate, and silent exit).
func TestReadSuggestEligible(t *testing.T) {
	cases := []struct {
		rows   int
		asOf   string
		status string
		want   bool
	}{
		{0, "", "", true},              // eligible zero hit
		{1, "", "", false},             // hit
		{3, "", "", false},             // hit (multiple)
		{0, "abc123", "", false},       // --as-of (past point in time)
		{0, "", "active", false},       // --status (state filter)
		{0, "abc123", "active", false}, // both
	}
	for _, c := range cases {
		if got := readSuggestEligible(c.rows, c.asOf, c.status); got != c.want {
			t.Errorf("readSuggestEligible(%d,%q,%q)=%v, want %v",
				c.rows, c.asOf, c.status, got, c.want)
		}
	}
}

// TestMetaKVRoundtrip — metaKV correctly assembles getKV/setKV closures over a Store
// (P4): absent ("",nil), a set->get round trip, and transport-error propagation.
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
			return nil, nil // key absent
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
	// transport-error propagation — both getKV and setKV return query errors as-is.
	g := &fakeStore{queryErr: errors.New("db down")}
	get2, set2 := metaKV(context.Background(), g)
	if _, err := get2("k"); err == nil {
		t.Error("getKV 전송 오류 미전파")
	}
	if err := set2("k", "v"); err == nil {
		t.Error("setKV 전송 오류 미전파")
	}
}
