package vault

import (
	"context"
	"os"
	"testing"

	"github.com/synuns/flint/internal/testengine"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testengine.RunCleanup(context.Background())
	os.Exit(code)
}
