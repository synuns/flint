package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeEvent struct {
	kind string
	text string
}

type fakeRow struct {
	scanErr error
	exists  bool
}

func (r fakeRow) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(dest) > 0 {
		if b, ok := dest[0].(*bool); ok {
			*b = r.exists
		}
	}
	return nil
}

func (r fakeRow) Err() error { return nil }

type fakePool struct {
	dsn     string
	events  *[]fakeEvent
	roleRow fakeRow
	execErr error
	closed  bool
}

func (p *fakePool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	*p.events = append(*p.events, fakeEvent{"exec", sql})
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	return pgconn.CommandTag{}, nil
}

func (p *fakePool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	*p.events = append(*p.events, fakeEvent{"query", sql})
	return p.roleRow
}

func (p *fakePool) Close() {
	if p.closed {
		return
	}
	p.closed = true
	*p.events = append(*p.events, fakeEvent{"close", p.dsn})
}

func installFakePools(t *testing.T, pools []*fakePool) *[]fakeEvent {
	t.Helper()
	events := &[]fakeEvent{}
	next := 0
	orig := poolOpenFn
	poolOpenFn = func(ctx context.Context, dsn string) (dbPool, error) {
		*events = append(*events, fakeEvent{"open", dsn})
		if next >= len(pools) {
			return nil, errors.New("engine: unexpected pool open")
		}
		p := pools[next]
		next++
		p.dsn = dsn
		p.events = events
		return p, nil
	}
	t.Cleanup(func() { poolOpenFn = orig })
	return events
}

func kindsWithText(events []fakeEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.kind+":"+e.text)
	}
	return out
}

func TestBootstrapOrderCreatesWhenMissing(t *testing.T) {
	super := &fakePool{roleRow: fakeRow{exists: false}}
	def := &fakePool{}
	events := installFakePools(t, []*fakePool{super, def})

	if err := BootstrapCredentials(context.Background()); err != nil {
		t.Fatalf("BootstrapCredentials: %v", err)
	}

	got := kindsWithText(*events)
	if len(got) != 8 {
		t.Fatalf("event count = %d (%v), want 8", len(got), got)
	}
	if got[0] != "open:"+bootstrapSuperuserDSN {
		t.Errorf("first open = %q, want superuser dsn", got[0])
	}
	if !strings.Contains(got[1], "pg_roles") {
		t.Errorf("second event = %q, want pg_roles lookup", got[1])
	}
	if !strings.HasPrefix(got[2], "exec:CREATE ROLE") {
		t.Errorf("third event = %q, want CREATE ROLE", got[2])
	}
	if !strings.Contains(got[3], "pg_database") {
		t.Errorf("fourth event = %q, want database lookup", got[3])
	}
	if !strings.HasPrefix(got[4], "exec:CREATE DATABASE") {
		t.Errorf("fifth event = %q, want CREATE DATABASE", got[4])
	}
	if got[5] != "open:"+bootstrapDefaultDSN {
		t.Errorf("sixth event = %q, want default dsn open", got[5])
	}
	if got[6] != "close:"+bootstrapDefaultDSN {
		t.Errorf("seventh event = %q, want default pool close", got[6])
	}
	if got[7] != "close:"+bootstrapSuperuserDSN {
		t.Errorf("eighth event = %q, want superuser pool close", got[7])
	}
	if !super.closed {
		t.Errorf("superuser pool not closed")
	}
}

func TestBootstrapExistingRoleAltersOnly(t *testing.T) {
	super := &fakePool{roleRow: fakeRow{exists: true}}
	def := &fakePool{}
	events := installFakePools(t, []*fakePool{super, def})

	if err := BootstrapCredentials(context.Background()); err != nil {
		t.Fatalf("BootstrapCredentials: %v", err)
	}

	var altered, created bool
	for _, e := range *events {
		if e.kind == "exec" {
			switch {
			case strings.HasPrefix(e.text, "ALTER ROLE"):
				altered = true
			case strings.HasPrefix(e.text, "CREATE ROLE"):
				created = true
			}
		}
	}
	if !altered {
		t.Errorf("ALTER ROLE not issued")
	}
	if created {
		t.Errorf("CREATE ROLE issued for existing role")
	}
	if !strings.Contains(eventsText(*events, "ALTER ROLE"), "PASSWORD") {
		t.Errorf("ALTER ROLE does not reset password: %q", eventsText(*events, "ALTER ROLE"))
	}
}

func eventsText(events []fakeEvent, prefix string) string {
	for _, e := range events {
		if e.kind == "exec" && strings.HasPrefix(e.text, prefix) {
			return e.text
		}
	}
	return ""
}

func TestBootstrapSuperuserConnectFailure(t *testing.T) {
	orig := poolOpenFn
	poolOpenFn = func(ctx context.Context, dsn string) (dbPool, error) {
		return nil, errors.New("dial fail")
	}
	t.Cleanup(func() { poolOpenFn = orig })

	err := BootstrapCredentials(context.Background())
	if err == nil {
		t.Fatalf("BootstrapCredentials succeeded, want error")
	}
	if !strings.Contains(err.Error(), "superuser") {
		t.Errorf("error = %v, want superuser context", err)
	}
}

func TestBootstrapRoleExecFailure(t *testing.T) {
	super := &fakePool{
		roleRow: fakeRow{exists: false},
		execErr: errors.New("permission denied"),
	}
	events := installFakePools(t, []*fakePool{super})

	err := BootstrapCredentials(context.Background())
	if err == nil {
		t.Fatalf("BootstrapCredentials succeeded, want error")
	}
	if !strings.Contains(err.Error(), "role") {
		t.Errorf("error = %v, want role context", err)
	}
	for _, e := range *events {
		if e.kind == "open" && strings.Contains(e.text, bootstrapDefaultDSN) {
			t.Errorf("default dsn opened despite role failure")
		}
	}
}

func TestBootstrapDefaultDSNConnectFailure(t *testing.T) {
	super := &fakePool{roleRow: fakeRow{exists: true}}
	installFakePools(t, []*fakePool{super})

	orig := poolOpenFn
	prev := poolOpenFn
	poolOpenFn = func(ctx context.Context, dsn string) (dbPool, error) {
		if strings.Contains(dsn, bootstrapDefaultDSN) || strings.Contains(dsn, "flint:pw") {
			return nil, errors.New("auth fail")
		}
		return prev(ctx, dsn)
	}
	t.Cleanup(func() { poolOpenFn = orig })

	err := BootstrapCredentials(context.Background())
	if err == nil {
		t.Fatalf("BootstrapCredentials succeeded, want error")
	}
	if !strings.Contains(err.Error(), "flint:pw") && !strings.Contains(err.Error(), "default") {
		t.Errorf("error = %v, want default dsn context", err)
	}
}
