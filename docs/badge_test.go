package docs

import (
	"os"
	"regexp"
	"testing"
)

func TestReadmeBadgeIsLatest(t *testing.T) {
	re := regexp.MustCompile(`release-v[0-9]+\.[0-9]+\.[0-9]+`)
	var seen string
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindString(string(b))
		if m == "" {
			t.Errorf("%s release-v 배지 없음", p)
		}
		if seen != "" && m != seen {
			t.Errorf("EN/KO 배지 불일치: %s vs %s", seen, m)
		}
		seen = m
	}
}
