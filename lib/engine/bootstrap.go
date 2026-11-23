package engine

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	bootstrapSuperuserDSN = "postgresql://postgres:password@127.0.0.1:5434/postgres"
	bootstrapRoleName     = "flint"
	bootstrapRolePassword = "pw"
	bootstrapDefaultDSN   = "postgresql://flint:pw@127.0.0.1:5434/flint"
)

type dbPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Close()
}

func dsnTarget(dsn string) string {
	host, port := dsnHostPort(dsn)
	if host == "" {
		return dsn
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

var poolOpenFn = func(ctx context.Context, dsn string) (dbPool, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("engine: open %s: %w", dsnTarget(dsn), err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("engine: ping %s: %w", dsnTarget(dsn), err)
	}
	return p, nil
}

func BootstrapCredentials(ctx context.Context) error {
	super, err := poolOpenFn(ctx, bootstrapSuperuserDSN)
	if err != nil {
		return fmt.Errorf("engine: superuser connect: %w", err)
	}
	defer super.Close()

	var exists bool
	lookupSQL := "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)"
	if err := super.QueryRow(ctx, lookupSQL, bootstrapRoleName).Scan(&exists); err != nil {
		return fmt.Errorf("engine: role lookup: %w", err)
	}

	if exists {
		alterSQL := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD '%s'", bootstrapRoleName, bootstrapRolePassword)
		if _, err := super.Exec(ctx, alterSQL); err != nil {
			return fmt.Errorf("engine: alter role: %w", err)
		}
	} else {
		createSQL := fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", bootstrapRoleName, bootstrapRolePassword)
		if _, err := super.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("engine: create role: %w", err)
		}
	}

	def, err := poolOpenFn(ctx, bootstrapDefaultDSN)
	if err != nil {
		return fmt.Errorf("engine: default dsn connect: %w", err)
	}
	def.Close()
	return nil
}
