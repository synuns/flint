package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReadSuggestEligible(t *testing.T) {
	cases := []struct {
		rows   int
		asOf   string
		status string
		want   bool
	}{
		{0, "", "", true},
		{1, "", "", false},
		{3, "", "", false},
		{0, "abc123", "", false},
		{0, "", "active", false},
		{0, "abc123", "active", false},
	}
	for _, c := range cases {
		if got := readSuggestEligible(c.rows, c.asOf, c.status); got != c.want {
			t.Errorf("readSuggestEligible(%d,%q,%q)=%v, want %v",
				c.rows, c.asOf, c.status, got, c.want)
		}
	}
}

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
			return nil, nil
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

	g := &fakeStore{queryErr: errors.New("db down")}
	get2, set2 := metaKV(context.Background(), g)
	if _, err := get2("k"); err == nil {
		t.Error("getKV 전송 오류 미전파")
	}
	if err := set2("k", "v"); err == nil {
		t.Error("setKV 전송 오류 미전파")
	}
}
