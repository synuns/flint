package docs

import (
	"os"
	"strings"
	"testing"
)

func TestMakefileDerivesVersion(t *testing.T) {
	b, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "git describe --tags") {
		t.Error("Makefile VERSION 태그 파생 누락")
	}
}
