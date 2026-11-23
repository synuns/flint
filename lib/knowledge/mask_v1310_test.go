package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestUnknownFlagEchoMasksTokensV1310(t *testing.T) {
	saved := SessionLabelFn
	SessionLabelFn = func([]string) string { return "mask-v1310" }
	t.Cleanup(func() { SessionLabelFn = saved })
	const secret = "--engine-url=postgresql://flint:pw@127.0.0.1:5434/db"
	cases := []struct {
		name string
		run  func(stdout *strings.Builder) int
	}{
		{"approve", func(o *strings.Builder) int {
			return RunApprove(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"compact", func(o *strings.Builder) int {
			return RunCompact(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"pending", func(o *strings.Builder) int {
			return RunPending(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"brief", func(o *strings.Builder) int {
			return RunBrief(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"hot", func(o *strings.Builder) int {
			return RunHot(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"import", func(o *strings.Builder) int {
			return RunImport(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
		{"import_migrate", func(o *strings.Builder) int {
			return RunImportMigrate(context.Background(), &fakeStore{}, []string{secret}, nil, o)
		}},
	}
	for _, c := range cases {
		var out strings.Builder
		code := c.run(&out)
		msg := out.String()
		if code != 3 {
			t.Errorf("%s: exit = %d, want 3 (%s)", c.name, code, msg)
		}
		if strings.Contains(msg, "flint:pw") || strings.Contains(msg, "127.0.0.1") {
			t.Errorf("%s: 비밀/호스트 노출: %s", c.name, msg)
		}
		if !strings.Contains(msg, "://***") {
			t.Errorf("%s: 마스킹 부재: %s", c.name, msg)
		}
	}
}
